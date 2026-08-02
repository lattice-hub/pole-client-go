package pole

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/metadata"
)

const (
	// HeaderTargetNamespace identifies the Pole namespace of a business request.
	HeaderTargetNamespace = "latticehub-target-namespace"
	// HeaderTargetService identifies the Pole service of a business request.
	HeaderTargetService = "latticehub-target-service"
)

var targetServiceMetadataNames = [...]string{
	HeaderTargetNamespace,
	HeaderTargetService,
}

// Header is one canonical TargetService metadata pair.
type Header struct {
	Name  string
	Value string
}

// TargetService is an immutable, normalized logical Pole service identity.
type TargetService struct {
	namespace string
	service   string
}

// NewTargetService constructs a normalized and validated TargetService v1 value.
func NewTargetService(namespace, service string) (*TargetService, error) {
	normalizedNamespace, err := normalizeTargetServiceField("namespace", namespace)
	if err != nil {
		return nil, err
	}
	normalizedService, err := normalizeTargetServiceField("service", service)
	if err != nil {
		return nil, err
	}
	return &TargetService{namespace: normalizedNamespace, service: normalizedService}, nil
}

func normalizeTargetServiceField(name, value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must contain only Unicode scalar values", name)
	}
	for _, character := range value {
		if unicode.Is(unicode.Cc, character) {
			return "", fmt.Errorf("%s must not contain Unicode Cc control characters", name)
		}
	}
	normalized := strings.TrimFunc(value, isContractWhitespace)
	if normalized == "" {
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

// Namespace returns the normalized Pole namespace.
func (target *TargetService) Namespace() string {
	return target.namespace
}

// Service returns the normalized Pole logical service name.
func (target *TargetService) Service() string {
	return target.service
}

func (target *TargetService) validate() error {
	if target == nil {
		return fmt.Errorf("TargetService must not be nil")
	}
	validated, err := NewTargetService(target.namespace, target.service)
	if err != nil {
		return fmt.Errorf("invalid TargetService: %w", err)
	}
	if *validated != *target {
		return fmt.Errorf("invalid TargetService: fields must be normalized")
	}
	return nil
}

// Metadata returns TargetService metadata in deterministic contract order.
func (target *TargetService) Metadata() ([]Header, error) {
	if err := target.validate(); err != nil {
		return nil, err
	}
	return []Header{
		{Name: HeaderTargetNamespace, Value: encodeMetadataValue(target.namespace)},
		{Name: HeaderTargetService, Value: encodeMetadataValue(target.service)},
	}, nil
}

// EncodeHTTPHeaders returns a copy of base with TargetService HTTP headers replaced.
func (target *TargetService) EncodeHTTPHeaders(base http.Header) (http.Header, error) {
	metadataPairs, err := target.Metadata()
	if err != nil {
		return nil, err
	}
	encoded := base.Clone()
	if encoded == nil {
		encoded = make(http.Header)
	}
	removeTargetServiceHTTPHeaders(encoded)
	for _, pair := range metadataPairs {
		encoded.Set(pair.Name, pair.Value)
	}
	return encoded, nil
}

// EncodeGRPCMetadata returns a copy of base with TargetService gRPC metadata replaced.
func (target *TargetService) EncodeGRPCMetadata(base metadata.MD) (metadata.MD, error) {
	metadataPairs, err := target.Metadata()
	if err != nil {
		return nil, err
	}
	encoded := base.Copy()
	if encoded == nil {
		encoded = make(metadata.MD)
	}
	removeTargetServiceGRPCMetadata(encoded)
	for _, pair := range metadataPairs {
		encoded.Set(pair.Name, pair.Value)
	}
	return encoded, nil
}

// EncodeAttachments returns a copy of base with TargetService attachment values replaced.
// It is suitable for protocol clients, such as Dubbo, that expose string attachments.
func (target *TargetService) EncodeAttachments(base map[string]string) (map[string]string, error) {
	metadataPairs, err := target.Metadata()
	if err != nil {
		return nil, err
	}
	encoded := make(map[string]string, len(base)+len(metadataPairs))
	for name, value := range base {
		if !isTargetServiceMetadataName(name) {
			encoded[name] = value
		}
	}
	for _, pair := range metadataPairs {
		encoded[pair.Name] = pair.Value
	}
	return encoded, nil
}

func removeTargetServiceHTTPHeaders(headers http.Header) {
	for name := range headers {
		if isTargetServiceMetadataName(name) {
			delete(headers, name)
		}
	}
}

func removeTargetServiceGRPCMetadata(values metadata.MD) {
	for name := range values {
		if isTargetServiceMetadataName(name) {
			delete(values, name)
		}
	}
}

func isTargetServiceMetadataName(name string) bool {
	for _, expected := range targetServiceMetadataNames {
		if strings.EqualFold(name, expected) {
			return true
		}
	}
	return false
}

func encodeMetadataValue(value string) string {
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
