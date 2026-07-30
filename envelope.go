// Package pole provides the framework-independent core of the Pole Go Thin SDK.
package pole

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	EnvelopeVersion        = "1"
	DefaultSidecarEndpoint = "http://127.0.0.1:15001"

	HeaderEnvelopeVersion  = "x-pole-target-envelope-version"
	HeaderNamespace        = "x-pole-target-namespace"
	HeaderService          = "x-pole-target-service"
	HeaderProtocol         = "x-pole-target-protocol"
	HeaderGroup            = "x-pole-target-group"
	HeaderServiceVersion   = "x-pole-target-service-version"
	HeaderMethod           = "x-pole-target-method"
	HeaderOriginalEndpoint = "x-pole-original-endpoint"
)

var internalHeaderNames = [...]string{
	HeaderEnvelopeVersion,
	HeaderNamespace,
	HeaderService,
	HeaderProtocol,
	HeaderGroup,
	HeaderServiceVersion,
	HeaderMethod,
	HeaderOriginalEndpoint,
}

// TargetEnvelopeOptions contains optional TargetEnvelope v1 fields.
// NewTargetEnvelope copies and normalizes every value.
type TargetEnvelopeOptions struct {
	Protocol         string
	Group            string
	ServiceVersion   string
	Method           string
	OriginalEndpoint string
}

// TargetEnvelope is an immutable, normalized TargetEnvelope v1 value.
// Its fields are intentionally private and can only be read through accessors.
type TargetEnvelope struct {
	namespace        string
	service          string
	protocol         string
	group            string
	serviceVersion   string
	method           string
	originalEndpoint string
}

// Header is one encoded TargetEnvelope HTTP header.
type Header struct {
	Name  string
	Value string
}

// NewTargetEnvelope constructs a normalized and validated TargetEnvelope v1.
func NewTargetEnvelope(namespace, service string, options TargetEnvelopeOptions) (*TargetEnvelope, error) {
	envelope := &TargetEnvelope{}
	values := []struct {
		name     string
		value    string
		required bool
		target   *string
	}{
		{name: "namespace", value: namespace, required: true, target: &envelope.namespace},
		{name: "service", value: service, required: true, target: &envelope.service},
		{name: "protocol", value: options.Protocol, target: &envelope.protocol},
		{name: "group", value: options.Group, target: &envelope.group},
		{name: "service_version", value: options.ServiceVersion, target: &envelope.serviceVersion},
		{name: "method", value: options.Method, target: &envelope.method},
		{name: "original_endpoint", value: options.OriginalEndpoint, target: &envelope.originalEndpoint},
	}

	for _, field := range values {
		value, err := normalizeField(field.name, field.value, field.required)
		if err != nil {
			return nil, err
		}
		*field.target = value
	}

	if envelope.originalEndpoint != "" {
		if err := validateOriginalEndpoint(envelope.originalEndpoint); err != nil {
			return nil, err
		}
	}

	return envelope, nil
}

func normalizeField(name, value string, required bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must contain only Unicode scalar values", name)
	}
	for _, character := range value {
		if unicode.Is(unicode.Cc, character) {
			return "", fmt.Errorf("%s must not contain Unicode Cc control characters", name)
		}
	}
	normalized := strings.TrimFunc(value, isContractWhitespace)
	if required && normalized == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return normalized, nil
}

func isContractWhitespace(character rune) bool {
	return character >= 0x0009 && character <= 0x000D ||
		character == 0x0020 ||
		character == 0x0085 ||
		character == 0x00A0 ||
		character == 0x1680 ||
		character >= 0x2000 && character <= 0x200A ||
		character == 0x2028 ||
		character == 0x2029 ||
		character == 0x202F ||
		character == 0x205F ||
		character == 0x3000
}

func validateOriginalEndpoint(endpoint string) error {
	host, port, err := splitOriginalEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("original_endpoint must be host:port or [ipv6]:port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("original_endpoint host must not be empty")
	}
	if err := validateOriginalEndpointHost(host); err != nil {
		return err
	}
	if port == "" {
		return fmt.Errorf("original_endpoint port must not be empty")
	}
	if len(port) > 1 && port[0] == '0' {
		return fmt.Errorf("original_endpoint port must not contain leading zeroes")
	}
	for _, character := range port {
		if character < '0' || character > '9' {
			return fmt.Errorf("original_endpoint port must be decimal")
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("original_endpoint port must be in range 1..65535")
	}
	return nil
}

func validateOriginalEndpointHost(host string) error {
	for _, character := range host {
		if isContractWhitespace(character) {
			return fmt.Errorf("original_endpoint host must not contain Unicode whitespace")
		}
		switch character {
		case '/', '\\', '[', ']', '@', '?', '#', '%':
			return fmt.Errorf("original_endpoint host must not contain %q", character)
		}
	}
	return nil
}

func splitOriginalEndpoint(endpoint string) (string, string, error) {
	if strings.HasPrefix(endpoint, "[") {
		closingBracket := strings.LastIndex(endpoint, "]")
		if closingBracket < 0 || closingBracket+1 >= len(endpoint) || endpoint[closingBracket+1] != ':' {
			return "", "", fmt.Errorf("invalid bracketed endpoint")
		}
		host := endpoint[1:closingBracket]
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() {
			return "", "", fmt.Errorf("brackets are only valid for IPv6 hosts")
		}
		return host, endpoint[closingBracket+2:], nil
	}

	if strings.Count(endpoint, ":") != 1 {
		return "", "", fmt.Errorf("non-bracketed host must not contain a colon")
	}
	host, port, _ := strings.Cut(endpoint, ":")
	return host, port, nil
}

// Namespace returns the normalized Pole namespace.
func (envelope *TargetEnvelope) Namespace() string {
	return envelope.namespace
}

// Service returns the normalized Pole logical service name.
func (envelope *TargetEnvelope) Service() string {
	return envelope.service
}

// Protocol returns the normalized protocol value.
func (envelope *TargetEnvelope) Protocol() string {
	return envelope.protocol
}

// Group returns the normalized protocol service group.
func (envelope *TargetEnvelope) Group() string {
	return envelope.group
}

// ServiceVersion returns the normalized target service version.
func (envelope *TargetEnvelope) ServiceVersion() string {
	return envelope.serviceVersion
}

// Method returns the normalized RPC method or HTTP method.
func (envelope *TargetEnvelope) Method() string {
	return envelope.method
}

// OriginalEndpoint returns the normalized endpoint selected by the framework.
func (envelope *TargetEnvelope) OriginalEndpoint() string {
	return envelope.originalEndpoint
}

func (envelope *TargetEnvelope) validate() error {
	if envelope == nil {
		return fmt.Errorf("TargetEnvelope must not be nil")
	}

	validated, err := NewTargetEnvelope(
		envelope.namespace,
		envelope.service,
		TargetEnvelopeOptions{
			Protocol:         envelope.protocol,
			Group:            envelope.group,
			ServiceVersion:   envelope.serviceVersion,
			Method:           envelope.method,
			OriginalEndpoint: envelope.originalEndpoint,
		},
	)
	if err != nil {
		return fmt.Errorf("invalid TargetEnvelope: %w", err)
	}
	if *validated != *envelope {
		return fmt.Errorf("invalid TargetEnvelope: fields must be normalized")
	}
	return nil
}

// Headers returns TargetEnvelope headers in deterministic contract order.
// Empty optional fields are omitted.
func (envelope *TargetEnvelope) Headers() ([]Header, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}

	headers := make([]Header, 0, len(internalHeaderNames))
	headers = append(headers,
		Header{Name: HeaderEnvelopeVersion, Value: EnvelopeVersion},
		Header{Name: HeaderNamespace, Value: encodeHeaderValue(envelope.namespace)},
		Header{Name: HeaderService, Value: encodeHeaderValue(envelope.service)},
	)
	appendOptionalHeader := func(name, value string) {
		if value != "" {
			headers = append(headers, Header{Name: name, Value: encodeHeaderValue(value)})
		}
	}
	appendOptionalHeader(HeaderProtocol, envelope.protocol)
	appendOptionalHeader(HeaderGroup, envelope.group)
	appendOptionalHeader(HeaderServiceVersion, envelope.serviceVersion)
	appendOptionalHeader(HeaderMethod, envelope.method)
	appendOptionalHeader(HeaderOriginalEndpoint, envelope.originalEndpoint)
	return headers, nil
}

func encodeHeaderValue(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(value))
	for _, current := range []byte(value) {
		if current >= 0x20 && current <= 0x7E && current != '%' && current != ',' {
			encoded.WriteByte(current)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[current>>4])
		encoded.WriteByte(hexadecimal[current&0x0F])
	}
	return encoded.String()
}

// EncodeHTTPHeaders returns a copy of base with all internal TargetEnvelope
// headers replaced by values from this envelope. The caller's map is unchanged.
func (envelope *TargetEnvelope) EncodeHTTPHeaders(base http.Header) (http.Header, error) {
	headers, err := envelope.Headers()
	if err != nil {
		return nil, err
	}

	encoded := base.Clone()
	if encoded == nil {
		encoded = make(http.Header)
	}
	for existingName := range encoded {
		for _, internalName := range internalHeaderNames {
			if strings.EqualFold(existingName, internalName) {
				delete(encoded, existingName)
				break
			}
		}
	}
	for _, header := range headers {
		encoded.Set(header.Name, header.Value)
	}
	return encoded, nil
}
