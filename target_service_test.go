package pole

import (
	"net/http"
	"reflect"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestNewTargetServiceNormalizesAndEncodesCanonicalMetadata(t *testing.T) {
	target, err := NewTargetService(" 生产 ", " 订单%,服务 ")
	if err != nil {
		t.Fatalf("NewTargetService() error = %v", err)
	}
	if target.Namespace() != "生产" || target.Service() != "订单%,服务" {
		t.Fatalf("normalized TargetService = (%q, %q)", target.Namespace(), target.Service())
	}
	metadataPairs, err := target.Metadata()
	if err != nil {
		t.Fatalf("Metadata() error = %v", err)
	}
	want := []Header{
		{Name: HeaderTargetNamespace, Value: "%E7%94%9F%E4%BA%A7"},
		{Name: HeaderTargetService, Value: "%E8%AE%A2%E5%8D%95%25%2C%E6%9C%8D%E5%8A%A1"},
	}
	if !reflect.DeepEqual(metadataPairs, want) {
		t.Fatalf("Metadata() = %#v, want %#v", metadataPairs, want)
	}
}

func TestNewTargetServiceRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		service   string
	}{
		{name: "empty namespace", namespace: "\u3000", service: "orders"},
		{name: "empty service", namespace: "default", service: "\u3000"},
		{name: "namespace control", namespace: "def\nault", service: "orders"},
		{name: "service control", namespace: "default", service: "ord\u0085ers"},
		{name: "invalid UTF-8", namespace: string([]byte{0xff}), service: "orders"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTargetService(test.namespace, test.service); err == nil {
				t.Fatal("NewTargetService() error = nil, want validation error")
			}
		})
	}
}

func TestTargetServiceOverwritesUntrustedMetadataWithoutMutatingInputs(t *testing.T) {
	target, err := NewTargetService("default", "orders")
	if err != nil {
		t.Fatalf("NewTargetService() error = %v", err)
	}
	headers := http.Header{
		"X-Request-ID":                {"request-123"},
		"Latticehub-Target-Namespace": {"forged", "stale"},
		HeaderTargetService:           {"forged"},
	}
	encodedHeaders, err := target.EncodeHTTPHeaders(headers)
	if err != nil {
		t.Fatalf("EncodeHTTPHeaders() error = %v", err)
	}
	if got := encodedHeaders.Values(HeaderTargetNamespace); !reflect.DeepEqual(got, []string{"default"}) {
		t.Fatalf("namespace values = %#v", got)
	}
	if got := encodedHeaders.Values(HeaderTargetService); !reflect.DeepEqual(got, []string{"orders"}) {
		t.Fatalf("service values = %#v", got)
	}
	if got := headers.Values("Latticehub-Target-Namespace"); !reflect.DeepEqual(got, []string{"forged", "stale"}) {
		t.Fatalf("input headers changed: %#v", headers)
	}

	baseMetadata := metadata.MD{
		"Latticehub-Target-Namespace": {"forged"},
		HeaderTargetService:           {"forged"},
	}
	encodedMetadata, err := target.EncodeGRPCMetadata(baseMetadata)
	if err != nil {
		t.Fatalf("EncodeGRPCMetadata() error = %v", err)
	}
	if got := encodedMetadata.Get(HeaderTargetNamespace); !reflect.DeepEqual(got, []string{"default"}) {
		t.Fatalf("gRPC namespace values = %#v", got)
	}
	if got := encodedMetadata.Get(HeaderTargetService); !reflect.DeepEqual(got, []string{"orders"}) {
		t.Fatalf("gRPC service values = %#v", got)
	}
	if got := baseMetadata["Latticehub-Target-Namespace"]; !reflect.DeepEqual(got, []string{"forged"}) {
		t.Fatalf("input gRPC metadata changed: %#v", baseMetadata)
	}

	attachments, err := target.EncodeAttachments(map[string]string{
		"Latticehub-Target-Namespace": "forged",
		HeaderTargetService:           "forged",
		"trace-id":                    "trace-123",
	})
	if err != nil {
		t.Fatalf("EncodeAttachments() error = %v", err)
	}
	if want := map[string]string{
		HeaderTargetNamespace: "default",
		HeaderTargetService:   "orders",
		"trace-id":            "trace-123",
	}; !reflect.DeepEqual(attachments, want) {
		t.Fatalf("attachments = %#v, want %#v", attachments, want)
	}
}

func TestTargetServiceRejectsZeroAndNilValues(t *testing.T) {
	for _, target := range []*TargetService{nil, {}} {
		metadataPairs, err := target.Metadata()
		if err == nil || metadataPairs != nil {
			t.Fatalf("Metadata() = %#v, %v; want nil validation error", metadataPairs, err)
		}
	}
}
