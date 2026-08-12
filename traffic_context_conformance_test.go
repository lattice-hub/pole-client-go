package pole

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type trafficContextConformance struct {
	Valid []struct {
		Name  string `json:"name"`
		Input struct {
			Version int `json:"version"`
			Labels  struct {
				Campaign *string `json:"campaign"`
				Lane     *string `json:"lane"`
				Bucket   *uint16 `json:"bucket"`
			} `json:"labels"`
		} `json:"input"`
		ExistingBaggage []string `json:"existing_baggage"`
		ExpectedBaggage string   `json:"expected_baggage"`
	} `json:"valid"`
	SidecarReceive struct {
		Valid []struct {
			Name     string   `json:"name"`
			Baggage  []string `json:"baggage"`
			Expected struct {
				Version int `json:"version"`
				Labels  struct {
					Campaign *string `json:"campaign"`
					Lane     *string `json:"lane"`
					Bucket   *uint16 `json:"bucket"`
				} `json:"labels"`
			} `json:"expected"`
		} `json:"valid"`
		Invalid []struct {
			Name       string   `json:"name"`
			Baggage    []string `json:"baggage"`
			Diagnostic string   `json:"diagnostic"`
		} `json:"invalid"`
	} `json:"sidecar_receive"`
}

func TestTrafficContextVendoredConformance(t *testing.T) {
	const contractDirectory = "contract/traffic-context/v1"
	checksumData, err := os.ReadFile(filepath.Join(contractDirectory, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("ReadFile(SHA256SUMS) error = %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(checksumData)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("invalid checksum line %q", line)
		}
		contents, err := os.ReadFile(filepath.Join(contractDirectory, fields[1]))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", fields[1], err)
		}
		digest := sha256.Sum256(contents)
		if got := hex.EncodeToString(digest[:]); got != fields[0] {
			t.Fatalf("checksum %s = %s, want %s", fields[1], got, fields[0])
		}
	}

	contents, err := os.ReadFile(filepath.Join(contractDirectory, "conformance.json"))
	if err != nil {
		t.Fatalf("ReadFile(conformance.json) error = %v", err)
	}
	var conformance trafficContextConformance
	if err := json.Unmarshal(contents, &conformance); err != nil {
		t.Fatalf("Unmarshal(conformance.json) error = %v", err)
	}
	for _, vector := range conformance.Valid {
		t.Run(vector.Name, func(t *testing.T) {
			if vector.Input.Version != 1 {
				t.Fatalf("input version = %d, want 1", vector.Input.Version)
			}
			traffic, err := NewTrafficContext(TrafficContextInput{
				Campaign: vector.Input.Labels.Campaign,
				Lane:     vector.Input.Labels.Lane,
				Bucket:   vector.Input.Labels.Bucket,
			})
			if err != nil {
				t.Fatalf("NewTrafficContext() error = %v", err)
			}
			baggage, err := InjectTrafficContext(nil, vector.ExistingBaggage, traffic)
			if err != nil {
				t.Fatalf("InjectTrafficContext() error = %v", err)
			}
			if baggage == nil || *baggage != vector.ExpectedBaggage {
				t.Fatalf("InjectTrafficContext() = %#v, want %q", baggage, vector.ExpectedBaggage)
			}
		})
	}
	for _, vector := range conformance.SidecarReceive.Invalid {
		t.Run(vector.Name, func(t *testing.T) {
			_, err := ExtractTrafficContext(vector.Baggage)
			if err == nil {
				t.Fatal("ExtractTrafficContext() error = nil, want validation error")
			}
			var trafficError *TrafficContextError
			if !errors.As(err, &trafficError) {
				t.Fatalf("ExtractTrafficContext() error = %T, want *TrafficContextError", err)
			}
			if got := string(trafficError.Diagnostic); got != vector.Diagnostic {
				t.Fatalf("diagnostic = %q, want %q", got, vector.Diagnostic)
			}
		})
	}
	for _, vector := range conformance.SidecarReceive.Valid {
		t.Run(vector.Name, func(t *testing.T) {
			if vector.Expected.Version != 1 {
				t.Fatalf("expected version = %d, want 1", vector.Expected.Version)
			}
			traffic, err := ExtractTrafficContext(vector.Baggage)
			if err != nil {
				t.Fatalf("ExtractTrafficContext() error = %v", err)
			}
			want, err := NewTrafficContext(TrafficContextInput{
				Campaign: vector.Expected.Labels.Campaign,
				Lane:     vector.Expected.Labels.Lane,
				Bucket:   vector.Expected.Labels.Bucket,
			})
			if err != nil {
				t.Fatalf("NewTrafficContext() error = %v", err)
			}
			if !trafficContextsEqual(traffic, want) {
				t.Fatalf("ExtractTrafficContext() = %#v, want %#v", traffic, want)
			}
		})
	}
}

func trafficContextsEqual(left, right *TrafficContext) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftCampaign, leftHasCampaign := left.Campaign()
	rightCampaign, rightHasCampaign := right.Campaign()
	leftLane, leftHasLane := left.Lane()
	rightLane, rightHasLane := right.Lane()
	leftBucket, leftHasBucket := left.Bucket()
	rightBucket, rightHasBucket := right.Bucket()
	return leftCampaign == rightCampaign && leftHasCampaign == rightHasCampaign &&
		leftLane == rightLane && leftHasLane == rightHasLane &&
		leftBucket == rightBucket && leftHasBucket == rightHasBucket
}
