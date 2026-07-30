package pole

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestNewTargetEnvelopeNormalizesAllFields(t *testing.T) {
	options := TargetEnvelopeOptions{
		Protocol:         "\u2003grpc\u2003",
		Group:            " group ",
		ServiceVersion:   " v1 ",
		Method:           " GetOrder ",
		OriginalEndpoint: " orders.internal:8080 ",
	}

	envelope, err := NewTargetEnvelope(" default ", " orders ", options)
	if err != nil {
		t.Fatalf("NewTargetEnvelope() error = %v", err)
	}

	got := []string{
		envelope.Namespace(),
		envelope.Service(),
		envelope.Protocol(),
		envelope.Group(),
		envelope.ServiceVersion(),
		envelope.Method(),
		envelope.OriginalEndpoint(),
	}
	want := []string{"default", "orders", "grpc", "group", "v1", "GetOrder", "orders.internal:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized fields = %#v, want %#v", got, want)
	}

	options.Protocol = "changed"
	if envelope.Protocol() != "grpc" {
		t.Fatalf("envelope changed after options mutation: %q", envelope.Protocol())
	}
}

func TestNewTargetEnvelopeRejectsInvalidRequiredFields(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		service   string
	}{
		{name: "empty namespace", namespace: "", service: "orders"},
		{name: "whitespace service", namespace: "default", service: "\u2003 \t"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTargetEnvelope(test.namespace, test.service, TargetEnvelopeOptions{}); err == nil {
				t.Fatal("NewTargetEnvelope() error = nil, want validation error")
			}
		})
	}
}

func TestNewTargetEnvelopeRejectsControlCharactersInEveryField(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		service   string
		options   TargetEnvelopeOptions
	}{
		{name: "namespace U+000A", namespace: "def\nault", service: "orders"},
		{name: "service U+0085", namespace: "default", service: "ord\u0085ers"},
		{name: "trailing U+000A", namespace: "default\n", service: "orders"},
		{name: "leading U+0085", namespace: "default", service: "\u0085orders"},
		{name: "protocol", namespace: "default", service: "orders", options: TargetEnvelopeOptions{Protocol: "gr\npc"}},
		{name: "group", namespace: "default", service: "orders", options: TargetEnvelopeOptions{Group: "gro\u0085up"}},
		{name: "service version", namespace: "default", service: "orders", options: TargetEnvelopeOptions{ServiceVersion: "v\n1"}},
		{name: "method", namespace: "default", service: "orders", options: TargetEnvelopeOptions{Method: "Get\u0085Order"}},
		{name: "original endpoint", namespace: "default", service: "orders", options: TargetEnvelopeOptions{OriginalEndpoint: "orders.\ninternal:8080"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTargetEnvelope(test.namespace, test.service, test.options); err == nil {
				t.Fatal("NewTargetEnvelope() error = nil, want Unicode Cc validation error")
			}
		})
	}
}

func TestNewTargetEnvelopeAcceptsValidOriginalEndpoints(t *testing.T) {
	endpoints := []string{
		"orders.internal:8080",
		"127.0.0.1:8080",
		"[::1]:8080",
		"orders.internal:1",
		"orders.internal:65535",
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			envelope, err := NewTargetEnvelope("default", "orders", TargetEnvelopeOptions{OriginalEndpoint: endpoint})
			if err != nil {
				t.Fatalf("NewTargetEnvelope() error = %v", err)
			}
			if envelope.OriginalEndpoint() != endpoint {
				t.Fatalf("OriginalEndpoint() = %q, want %q", envelope.OriginalEndpoint(), endpoint)
			}
		})
	}
}

func TestNewTargetEnvelopeRejectsInvalidOriginalEndpoints(t *testing.T) {
	endpoints := []string{
		"orders.internal",
		"orders.internal:",
		":8080",
		"orders.internal:0",
		"orders.internal:65536",
		"orders.internal:eighty",
		"orders.internal:+80",
		"::1:8080",
		"[orders.internal]:8080",
		"[::1]",
		"bad host:80",
		"bad\u2003host:80",
		"evil/path:80",
		"evil\\path:80",
		"evil[host:80",
		"evil]host:80",
		"evil@host:80",
		"evil?host:80",
		"evil#host:80",
	}

	for _, endpoint := range endpoints {
		t.Run(strings.ReplaceAll(endpoint, "/", "_"), func(t *testing.T) {
			if _, err := NewTargetEnvelope("default", "orders", TargetEnvelopeOptions{OriginalEndpoint: endpoint}); err == nil {
				t.Fatal("NewTargetEnvelope() error = nil, want endpoint validation error")
			}
		})
	}
}

func TestHeadersEncodesCompleteEnvelopeInContractOrder(t *testing.T) {
	envelope, err := NewTargetEnvelope("default", "orders", TargetEnvelopeOptions{
		Protocol:         "grpc",
		Group:            "production",
		ServiceVersion:   "v1",
		Method:           "GetOrder",
		OriginalEndpoint: "orders.internal:8080",
	})
	if err != nil {
		t.Fatalf("NewTargetEnvelope() error = %v", err)
	}

	want := []Header{
		{Name: HeaderEnvelopeVersion, Value: "1"},
		{Name: HeaderNamespace, Value: "default"},
		{Name: HeaderService, Value: "orders"},
		{Name: HeaderProtocol, Value: "grpc"},
		{Name: HeaderGroup, Value: "production"},
		{Name: HeaderServiceVersion, Value: "v1"},
		{Name: HeaderMethod, Value: "GetOrder"},
		{Name: HeaderOriginalEndpoint, Value: "orders.internal:8080"},
	}
	got, err := envelope.Headers()
	if err != nil {
		t.Fatalf("Headers() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Headers() = %#v, want %#v", got, want)
	}
}

func TestHeadersOmitsEmptyOptionalFields(t *testing.T) {
	envelope, err := NewTargetEnvelope("default", "orders", TargetEnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewTargetEnvelope() error = %v", err)
	}

	want := []Header{
		{Name: HeaderEnvelopeVersion, Value: "1"},
		{Name: HeaderNamespace, Value: "default"},
		{Name: HeaderService, Value: "orders"},
	}
	got, err := envelope.Headers()
	if err != nil {
		t.Fatalf("Headers() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Headers() = %#v, want %#v", got, want)
	}
}

func TestHeadersRejectsZeroAndNilEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		envelope *TargetEnvelope
	}{
		{name: "zero value", envelope: &TargetEnvelope{}},
		{name: "nil", envelope: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers, err := test.envelope.Headers()
			if err == nil {
				t.Fatal("Headers() error = nil, want validation error")
			}
			if headers != nil {
				t.Fatalf("Headers() = %#v, want nil", headers)
			}
		})
	}
}

func TestEncodeHTTPHeadersOverwritesAllInternalHeaders(t *testing.T) {
	envelope, err := NewTargetEnvelope("default", "orders", TargetEnvelopeOptions{Protocol: "grpc"})
	if err != nil {
		t.Fatalf("NewTargetEnvelope() error = %v", err)
	}

	base := http.Header{"X-Unrelated": {"preserved"}}
	for _, name := range internalHeaderNames {
		base[name] = []string{"stale", "forged"}
	}

	encoded, err := envelope.EncodeHTTPHeaders(base)
	if err != nil {
		t.Fatalf("EncodeHTTPHeaders() error = %v", err)
	}
	if encoded.Get("X-Unrelated") != "preserved" {
		t.Fatalf("unrelated header = %q, want preserved", encoded.Get("X-Unrelated"))
	}
	headers, err := envelope.Headers()
	if err != nil {
		t.Fatalf("Headers() error = %v", err)
	}
	for _, header := range headers {
		values := encoded.Values(header.Name)
		if !reflect.DeepEqual(values, []string{header.Value}) {
			t.Fatalf("%s = %#v, want [%q]", header.Name, values, header.Value)
		}
	}
	for _, name := range []string{
		HeaderGroup,
		HeaderServiceVersion,
		HeaderMethod,
		HeaderOriginalEndpoint,
	} {
		if values := encoded.Values(name); len(values) != 0 {
			t.Fatalf("%s = %#v, want omitted", name, values)
		}
	}
	if got := base[HeaderNamespace]; !reflect.DeepEqual(got, []string{"stale", "forged"}) {
		t.Fatalf("base header mutated: %#v", got)
	}
}

func TestEncodeHTTPHeadersAcceptsNilBase(t *testing.T) {
	envelope, err := NewTargetEnvelope("default", "orders", TargetEnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewTargetEnvelope() error = %v", err)
	}

	encoded, err := envelope.EncodeHTTPHeaders(nil)
	if err != nil {
		t.Fatalf("EncodeHTTPHeaders() error = %v", err)
	}
	if encoded.Get(HeaderEnvelopeVersion) != EnvelopeVersion {
		t.Fatalf("%s = %q, want %q", HeaderEnvelopeVersion, encoded.Get(HeaderEnvelopeVersion), EnvelopeVersion)
	}
}

func TestEncodeHTTPHeadersRejectsZeroEnvelopeWithoutMutatingBase(t *testing.T) {
	base := http.Header{
		"X-Unrelated":   {"preserved"},
		HeaderNamespace: {"untrusted"},
	}

	encoded, err := (&TargetEnvelope{}).EncodeHTTPHeaders(base)
	if err == nil {
		t.Fatal("EncodeHTTPHeaders() error = nil, want validation error")
	}
	if encoded != nil {
		t.Fatalf("EncodeHTTPHeaders() = %#v, want nil", encoded)
	}
	if !reflect.DeepEqual(base, http.Header{
		"X-Unrelated":   {"preserved"},
		HeaderNamespace: {"untrusted"},
	}) {
		t.Fatalf("base header mutated: %#v", base)
	}
}

func TestDefaultSidecarEndpoint(t *testing.T) {
	const want = "http://127.0.0.1:15001"
	if DefaultSidecarEndpoint != want {
		t.Fatalf("DefaultSidecarEndpoint = %q, want %q", DefaultSidecarEndpoint, want)
	}
}
