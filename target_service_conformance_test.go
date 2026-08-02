package pole

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type targetServiceConformance struct {
	Valid []struct {
		Name     string       `json:"name"`
		Input    serviceInput `json:"input"`
		Expected [][]string   `json:"expected_metadata"`
	} `json:"valid"`
	Invalid []struct {
		Name  string       `json:"name"`
		Input serviceInput `json:"input"`
	} `json:"invalid"`
}

type serviceInput struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
}

func TestTargetServiceConformance(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("contract", "target-service", "v1", "conformance.json"))
	if err != nil {
		t.Fatalf("read conformance asset: %v", err)
	}
	var vectors targetServiceConformance
	if err := json.Unmarshal(contents, &vectors); err != nil {
		t.Fatalf("decode conformance asset: %v", err)
	}
	for _, vector := range vectors.Valid {
		t.Run("valid/"+vector.Name, func(t *testing.T) {
			target, err := NewTargetService(vector.Input.Namespace, vector.Input.Service)
			if err != nil {
				t.Fatalf("NewTargetService() error = %v", err)
			}
			metadataPairs, err := target.Metadata()
			if err != nil {
				t.Fatalf("Metadata() error = %v", err)
			}
			actual := make([][]string, 0, len(metadataPairs))
			for _, pair := range metadataPairs {
				actual = append(actual, []string{pair.Name, pair.Value})
			}
			if !reflect.DeepEqual(actual, vector.Expected) {
				t.Fatalf("metadata = %#v, want %#v", actual, vector.Expected)
			}
		})
	}
	for _, vector := range vectors.Invalid {
		t.Run("invalid/"+vector.Name, func(t *testing.T) {
			if _, err := NewTargetService(vector.Input.Namespace, vector.Input.Service); err == nil {
				t.Fatal("NewTargetService() error = nil, want validation error")
			}
		})
	}
}
