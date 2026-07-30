package pole

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type conformanceContract struct {
	Contract                string                    `json:"contract"`
	ContractVersion         string                    `json:"contract_version"`
	EnvelopeVersion         string                    `json:"envelope_version"`
	Valid                   []conformanceVector       `json:"valid"`
	Invalid                 []conformanceVector       `json:"invalid"`
	LanguageSpecificInvalid []languageInvalidVector   `json:"language_specific_invalid"`
	SidecarReceive          sidecarReceiveConformance `json:"sidecar_receive"`
}

type conformanceVector struct {
	Name            string            `json:"name"`
	Input           envelopeInput     `json:"input"`
	BaseHeaders     [][]string        `json:"base_headers"`
	Normalized      map[string]string `json:"normalized"`
	ExpectedHeaders [][]string        `json:"expected_headers"`
	Diagnostic      string            `json:"diagnostic"`
}

type envelopeInput struct {
	Namespace        string `json:"namespace"`
	Service          string `json:"service"`
	Protocol         string `json:"protocol"`
	Group            string `json:"group"`
	ServiceVersion   string `json:"service_version"`
	Method           string `json:"method"`
	OriginalEndpoint string `json:"original_endpoint"`
}

type languageInvalidVector struct {
	Name           string   `json:"name"`
	Field          string   `json:"field"`
	UTF16CodeUnits []string `json:"utf16_code_units"`
	Diagnostic     string   `json:"diagnostic"`
}

type sidecarReceiveConformance struct {
	Valid []struct {
		Name             string            `json:"name"`
		Headers          [][]string        `json:"headers"`
		ExpectedEnvelope map[string]string `json:"expected_envelope"`
	} `json:"valid"`
	Invalid []struct {
		Name       string     `json:"name"`
		Headers    [][]string `json:"headers"`
		Diagnostic string     `json:"diagnostic"`
	} `json:"invalid"`
}

func TestContractValidVectors(t *testing.T) {
	contract := loadConformanceContract(t)
	if len(contract.Valid) != 9 {
		t.Fatalf("valid vector count = %d, want 9", len(contract.Valid))
	}

	for _, vector := range contract.Valid {
		t.Run(vector.Name, func(t *testing.T) {
			envelope, err := envelopeFromInput(vector.Input)
			if err != nil {
				t.Fatalf("NewTargetEnvelope() error = %v", err)
			}

			normalized := normalizedEnvelope(envelope)
			if !reflect.DeepEqual(normalized, vector.Normalized) {
				t.Fatalf("normalized = %#v, want %#v", normalized, vector.Normalized)
			}

			internal, err := envelope.Headers()
			if err != nil {
				t.Fatalf("Headers() error = %v", err)
			}
			actualPairs := retainedBasePairs(vector.BaseHeaders)
			for _, header := range internal {
				actualPairs = append(actualPairs, []string{header.Name, header.Value})
			}
			if !reflect.DeepEqual(actualPairs, vector.ExpectedHeaders) {
				t.Fatalf("header pairs = %#v, want %#v", actualPairs, vector.ExpectedHeaders)
			}

			base := http.Header{}
			for _, pair := range vector.BaseHeaders {
				base.Add(pair[0], pair[1])
			}
			encoded, err := envelope.EncodeHTTPHeaders(base)
			if err != nil {
				t.Fatalf("EncodeHTTPHeaders() error = %v", err)
			}
			assertEncodedHeaders(t, encoded, vector.ExpectedHeaders)
		})
	}
}

func TestContractInvalidVectors(t *testing.T) {
	contract := loadConformanceContract(t)
	if len(contract.Invalid) != 20 {
		t.Fatalf("invalid vector count = %d, want 20", len(contract.Invalid))
	}

	for _, vector := range contract.Invalid {
		t.Run(vector.Name, func(t *testing.T) {
			if _, err := envelopeFromInput(vector.Input); err == nil {
				t.Fatal("NewTargetEnvelope() error = nil, want validation error")
			}
		})
	}
}

func TestContractLanguageSpecificInvalidVectors(t *testing.T) {
	contract := loadConformanceContract(t)
	if len(contract.LanguageSpecificInvalid) != 2 {
		t.Fatalf(
			"language-specific invalid vector count = %d, want 2",
			len(contract.LanguageSpecificInvalid),
		)
	}

	for _, vector := range contract.LanguageSpecificInvalid {
		t.Run(vector.Name, func(t *testing.T) {
			invalid := invalidUTF8FromUTF16CodeUnits(t, vector.UTF16CodeUnits)
			input := envelopeInput{Namespace: "default", Service: "orders"}
			switch vector.Field {
			case "namespace":
				input.Namespace = invalid
			case "service":
				input.Service = invalid
			default:
				t.Fatalf("unsupported language-specific field %q", vector.Field)
			}
			if _, err := envelopeFromInput(input); err == nil {
				t.Fatal("NewTargetEnvelope() error = nil, want Unicode scalar validation error")
			}
		})
	}
}

func TestVendoredContractAssetsAndSidecarReceiveStructure(t *testing.T) {
	contract := loadConformanceContract(t)
	if contract.Contract != "pole-target-envelope" ||
		contract.ContractVersion != "1.0.0" ||
		contract.EnvelopeVersion != "1" {
		t.Fatalf("unexpected contract identity: %#v", contract)
	}
	if len(contract.SidecarReceive.Valid) != 2 || len(contract.SidecarReceive.Invalid) != 8 {
		t.Fatalf(
			"sidecar_receive vector counts = (%d, %d), want (2, 8)",
			len(contract.SidecarReceive.Valid),
			len(contract.SidecarReceive.Invalid),
		)
	}
	for _, vector := range contract.SidecarReceive.Valid {
		if vector.Name == "" || len(vector.Headers) == 0 || len(vector.ExpectedEnvelope) == 0 {
			t.Fatalf("incomplete valid sidecar_receive vector: %#v", vector)
		}
	}
	for _, vector := range contract.SidecarReceive.Invalid {
		if vector.Name == "" || len(vector.Headers) == 0 || vector.Diagnostic == "" {
			t.Fatalf("incomplete invalid sidecar_receive vector: %#v", vector)
		}
	}

	schema, err := os.ReadFile(filepath.Join("contract", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument map[string]any
	if err := json.Unmarshal(schema, &schemaDocument); err != nil {
		t.Fatalf("schema.json is invalid: %v", err)
	}
	verifyContractChecksums(t)
}

func loadConformanceContract(t *testing.T) conformanceContract {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("contract", "conformance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract conformanceContract
	if err := json.Unmarshal(content, &contract); err != nil {
		t.Fatalf("conformance.json is invalid: %v", err)
	}
	return contract
}

func envelopeFromInput(input envelopeInput) (*TargetEnvelope, error) {
	return NewTargetEnvelope(input.Namespace, input.Service, TargetEnvelopeOptions{
		Protocol:         input.Protocol,
		Group:            input.Group,
		ServiceVersion:   input.ServiceVersion,
		Method:           input.Method,
		OriginalEndpoint: input.OriginalEndpoint,
	})
}

func normalizedEnvelope(envelope *TargetEnvelope) map[string]string {
	normalized := map[string]string{
		"namespace": envelope.Namespace(),
		"service":   envelope.Service(),
	}
	optional := []struct {
		name  string
		value string
	}{
		{name: "protocol", value: envelope.Protocol()},
		{name: "group", value: envelope.Group()},
		{name: "service_version", value: envelope.ServiceVersion()},
		{name: "method", value: envelope.Method()},
		{name: "original_endpoint", value: envelope.OriginalEndpoint()},
	}
	for _, field := range optional {
		if field.value != "" {
			normalized[field.name] = field.value
		}
	}
	return normalized
}

func retainedBasePairs(base [][]string) [][]string {
	retained := make([][]string, 0, len(base))
	for _, pair := range base {
		if !isInternalHeader(pair[0]) {
			retained = append(retained, []string{pair[0], pair[1]})
		}
	}
	return retained
}

func isInternalHeader(name string) bool {
	for _, internal := range internalHeaderNames {
		if strings.EqualFold(name, internal) {
			return true
		}
	}
	return false
}

func assertEncodedHeaders(t *testing.T, encoded http.Header, expected [][]string) {
	t.Helper()
	expectedMap := make(map[string][]string, len(expected))
	for _, pair := range expected {
		canonicalName := http.CanonicalHeaderKey(pair[0])
		expectedMap[canonicalName] = append(expectedMap[canonicalName], pair[1])
	}
	if !reflect.DeepEqual(map[string][]string(encoded), expectedMap) {
		t.Fatalf("encoded headers = %#v, want %#v", encoded, expectedMap)
	}
}

func invalidUTF8FromUTF16CodeUnits(t *testing.T, codeUnits []string) string {
	t.Helper()
	var value []byte
	for _, encoded := range codeUnits {
		codeUnit, err := strconv.ParseUint(encoded, 16, 16)
		if err != nil {
			t.Fatalf("invalid UTF-16 code unit %q: %v", encoded, err)
		}
		value = append(value,
			byte(0xE0|codeUnit>>12),
			byte(0x80|(codeUnit>>6)&0x3F),
			byte(0x80|codeUnit&0x3F),
		)
	}
	return string(value)
}

func verifyContractChecksums(t *testing.T) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("contract", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("SHA256SUMS line count = %d, want 2", len(lines))
	}
	expectedFiles := map[string]bool{
		"schema.json":      false,
		"conformance.json": false,
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid SHA256SUMS line %q", line)
		}
		if seen, exists := expectedFiles[parts[1]]; !exists || seen {
			t.Fatalf("unexpected or duplicate checksum file %q", parts[1])
		}
		expectedFiles[parts[1]] = true
		asset, err := os.ReadFile(filepath.Join("contract", parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		actual := sha256.Sum256(asset)
		if hex.EncodeToString(actual[:]) != parts[0] {
			t.Fatalf("%s checksum mismatch", parts[1])
		}
	}
	for fileName, seen := range expectedFiles {
		if !seen {
			t.Fatalf("SHA256SUMS is missing %s", fileName)
		}
	}

	version, err := os.ReadFile(filepath.Join("contract", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	versionText := string(version)
	for _, expected := range []string{
		"contract=pole-target-envelope\n",
		"version=1.0.0\n",
		"tag=thin-sdk-contract-v1.0.0\n",
		"commit=f45b0396b4680fe588a93086ceb2934d3e157d04\n",
	} {
		if !strings.Contains(versionText, expected) {
			t.Fatalf("VERSION is missing %q", strings.TrimSpace(expected))
		}
	}
}

func Example_contractIdentity() {
	contract := conformanceContract{
		Contract:        "pole-target-envelope",
		ContractVersion: "1.0.0",
	}
	fmt.Println(contract.Contract, contract.ContractVersion)
	// Output: pole-target-envelope 1.0.0
}
