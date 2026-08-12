package pole

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel/baggage"
)

const (
	// HeaderBaggage is the standard W3C Baggage carrier name.
	HeaderBaggage = "baggage"

	trafficBaggagePrefix    = "latticehub.traffic."
	trafficBaggageVersion   = trafficBaggagePrefix + "version"
	trafficBaggageCampaign  = trafficBaggagePrefix + "campaign"
	trafficBaggageLane      = trafficBaggagePrefix + "lane"
	trafficBaggageBucket    = trafficBaggagePrefix + "bucket"
	trafficBaggageLimit     = 180
	trafficBaggageByteLimit = 8192
)

type trafficContextKey struct{}

// TrafficContextDiagnostic identifies a stable TrafficContext contract failure.
type TrafficContextDiagnostic string

const (
	TrafficContextDiagnosticUnknownReservedKey TrafficContextDiagnostic = "UNKNOWN_RESERVED_KEY"
	TrafficContextDiagnosticInvalidBaggage     TrafficContextDiagnostic = "INVALID_BAGGAGE"
	TrafficContextDiagnosticDuplicateVersion   TrafficContextDiagnostic = "DUPLICATE_VERSION"
	TrafficContextDiagnosticDuplicateCampaign  TrafficContextDiagnostic = "DUPLICATE_CAMPAIGN"
	TrafficContextDiagnosticDuplicateLane      TrafficContextDiagnostic = "DUPLICATE_LANE"
	TrafficContextDiagnosticDuplicateBucket    TrafficContextDiagnostic = "DUPLICATE_BUCKET"
	TrafficContextDiagnosticMissingVersion     TrafficContextDiagnostic = "MISSING_VERSION"
	TrafficContextDiagnosticUnsupportedVersion TrafficContextDiagnostic = "UNSUPPORTED_VERSION"
	TrafficContextDiagnosticInvalidBucket      TrafficContextDiagnostic = "INVALID_BUCKET"
	TrafficContextDiagnosticNonCanonicalValue  TrafficContextDiagnostic = "NON_CANONICAL_VALUE"
	TrafficContextDiagnosticLabelTooLarge      TrafficContextDiagnostic = "LABEL_TOO_LARGE"
	TrafficContextDiagnosticTooManyMembers     TrafficContextDiagnostic = "TOO_MANY_MEMBERS"
	TrafficContextDiagnosticBaggageTooLarge    TrafficContextDiagnostic = "BAGGAGE_TOO_LARGE"
)

// TrafficContextError carries a stable diagnostic and a human-readable message.
type TrafficContextError struct {
	Diagnostic TrafficContextDiagnostic
	Message    string
}

func (err *TrafficContextError) Error() string {
	return err.Message
}

func trafficContextError(diagnostic TrafficContextDiagnostic, format string, arguments ...any) error {
	return &TrafficContextError{Diagnostic: diagnostic, Message: fmt.Sprintf(format, arguments...)}
}

// TrafficContext contains the optional v1 traffic labels for one request chain.
type TrafficContext struct {
	campaign *string
	lane     *string
	bucket   *uint16
}

// TrafficContextInput is the public optional-label input for TrafficContext.
type TrafficContextInput struct {
	Campaign *string
	Lane     *string
	Bucket   *uint16
}

// NewTrafficContext validates and creates an immutable TrafficContext.
func NewTrafficContext(input TrafficContextInput) (*TrafficContext, error) {
	context := &TrafficContext{}
	if input.Campaign != nil {
		value, err := validateTrafficLabel("campaign", *input.Campaign)
		if err != nil {
			return nil, err
		}
		context.campaign = &value
	}
	if input.Lane != nil {
		value, err := validateTrafficLabel("lane", *input.Lane)
		if err != nil {
			return nil, err
		}
		context.lane = &value
	}
	if input.Bucket != nil {
		if *input.Bucket > 9999 {
			return nil, fmt.Errorf("bucket must be between 0 and 9999")
		}
		value := *input.Bucket
		context.bucket = &value
	}
	return context, nil
}

// Campaign returns the optional campaign label.
func (traffic *TrafficContext) Campaign() (string, bool) {
	if traffic == nil || traffic.campaign == nil {
		return "", false
	}
	return *traffic.campaign, true
}

// Lane returns the optional lane label.
func (traffic *TrafficContext) Lane() (string, bool) {
	if traffic == nil || traffic.lane == nil {
		return "", false
	}
	return *traffic.lane, true
}

// Bucket returns the optional bucket label.
func (traffic *TrafficContext) Bucket() (uint16, bool) {
	if traffic == nil || traffic.bucket == nil {
		return 0, false
	}
	return *traffic.bucket, true
}

// AttachTrafficContext returns a derived Go context with the supplied TrafficContext.
// Retaining the parent context is the scope reset operation for Go callers.
func AttachTrafficContext(parent context.Context, traffic *TrafficContext) (context.Context, error) {
	if parent == nil {
		parent = context.Background()
	}
	validated, err := cloneTrafficContext(traffic)
	if err != nil {
		return nil, err
	}
	withTrafficContext := context.WithValue(parent, trafficContextKey{}, validated)
	return withOpenTelemetryTrafficContext(withTrafficContext, validated)
}

// ResetTrafficContext returns a derived context without a TrafficContext value.
func ResetTrafficContext(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	reset := context.WithValue(parent, trafficContextKey{}, (*TrafficContext)(nil))
	withBaggage, err := withOpenTelemetryTrafficContext(reset, nil)
	if err != nil {
		return reset
	}
	return withBaggage
}

// CurrentTrafficContext reads the optional TrafficContext from a Go context.
func CurrentTrafficContext(parent context.Context) *TrafficContext {
	if parent == nil {
		return nil
	}
	if traffic, found := parent.Value(trafficContextKey{}).(*TrafficContext); found {
		return traffic
	}
	return trafficContextFromOpenTelemetryBaggage(baggage.FromContext(parent))
}

// ExtractTrafficContext decodes TrafficContext reserved members without changing storage.
func ExtractTrafficContext(values []string) (*TrafficContext, error) {
	members, err := parseBaggageMembers(values)
	if err != nil {
		return nil, err
	}
	var campaign, lane *string
	var bucket *uint16
	versionFound := false
	labelFound := false
	for _, member := range members {
		if !isTrafficBaggageName(member.name) {
			if isTrafficBaggagePrefix(member.name) {
				return nil, trafficContextError(TrafficContextDiagnosticUnknownReservedKey, "unknown TrafficContext baggage member %q", member.name)
			}
			continue
		}
		if len(member.properties) != 0 {
			return nil, trafficContextError(TrafficContextDiagnosticInvalidBaggage, "TrafficContext baggage member %q must not have properties", member.name)
		}
		switch member.name {
		case trafficBaggageVersion:
			if versionFound {
				return nil, duplicateTrafficContextError(member.name)
			}
			if member.value != "1" {
				return nil, trafficContextError(TrafficContextDiagnosticUnsupportedVersion, "unsupported TrafficContext version %q", member.value)
			}
			versionFound = true
		case trafficBaggageCampaign:
			if campaign != nil {
				return nil, duplicateTrafficContextError(member.name)
			}
			value, err := decodeCanonicalTrafficLabel(member.value)
			if err != nil {
				return nil, err
			}
			campaign = &value
			labelFound = true
		case trafficBaggageLane:
			if lane != nil {
				return nil, duplicateTrafficContextError(member.name)
			}
			value, err := decodeCanonicalTrafficLabel(member.value)
			if err != nil {
				return nil, err
			}
			lane = &value
			labelFound = true
		case trafficBaggageBucket:
			if bucket != nil {
				return nil, duplicateTrafficContextError(member.name)
			}
			value, err := decodeTrafficBucket(member.value)
			if err != nil {
				return nil, err
			}
			bucket = &value
			labelFound = true
		}
	}
	if !labelFound {
		if versionFound {
			return NewTrafficContext(TrafficContextInput{})
		}
		return nil, nil
	}
	if !versionFound {
		return nil, trafficContextError(TrafficContextDiagnosticMissingVersion, "TrafficContext labels require version")
	}
	return NewTrafficContext(TrafficContextInput{Campaign: campaign, Lane: lane, Bucket: bucket})
}

// InjectTrafficContext returns one Baggage header value or nil when the carrier must be deleted.
// An explicit TrafficContext wins over the value attached to parent.
func InjectTrafficContext(parent context.Context, existing []string, explicit *TrafficContext) (*string, error) {
	traffic := explicit
	if traffic == nil {
		traffic = CurrentTrafficContext(parent)
	}
	if traffic != nil {
		validated, err := cloneTrafficContext(traffic)
		if err != nil {
			return nil, err
		}
		traffic = validated
	}

	members, err := parseBaggageMembers(existing)
	if err != nil {
		return nil, err
	}
	encoded := make([]string, 0, len(members)+4)
	for _, member := range members {
		if isTrafficBaggagePrefix(member.name) {
			continue
		}
		encoded = append(encoded, member.raw)
	}
	if traffic != nil && trafficHasLabels(traffic) {
		encoded = append(encoded, trafficBaggageVersion+"=1")
		if value, found := traffic.Campaign(); found {
			encoded = append(encoded, trafficBaggageCampaign+"="+encodeTrafficLabel(value))
		}
		if value, found := traffic.Lane(); found {
			encoded = append(encoded, trafficBaggageLane+"="+encodeTrafficLabel(value))
		}
		if value, found := traffic.Bucket(); found {
			encoded = append(encoded, trafficBaggageBucket+"="+strconv.FormatUint(uint64(value), 10))
		}
	}
	if len(encoded) == 0 {
		return nil, nil
	}
	if len(encoded) > trafficBaggageLimit {
		return nil, fmt.Errorf("baggage has %d members, maximum is %d", len(encoded), trafficBaggageLimit)
	}
	result := strings.Join(encoded, ",")
	if len(result) > trafficBaggageByteLimit {
		return nil, fmt.Errorf("baggage exceeds %d bytes", trafficBaggageByteLimit)
	}
	return &result, nil
}

func cloneTrafficContext(traffic *TrafficContext) (*TrafficContext, error) {
	if traffic == nil {
		return nil, fmt.Errorf("TrafficContext must not be nil")
	}
	return NewTrafficContext(TrafficContextInput{
		Campaign: traffic.campaign,
		Lane:     traffic.lane,
		Bucket:   traffic.bucket,
	})
}

func trafficHasLabels(traffic *TrafficContext) bool {
	return traffic != nil && (traffic.campaign != nil || traffic.lane != nil || traffic.bucket != nil)
}

func withOpenTelemetryTrafficContext(parent context.Context, traffic *TrafficContext) (context.Context, error) {
	members := baggage.FromContext(parent).Members()
	retained := make([]baggage.Member, 0, len(members)+4)
	for _, member := range members {
		if !isTrafficBaggagePrefix(member.Key()) {
			retained = append(retained, member)
		}
	}
	if trafficHasLabels(traffic) {
		version, err := baggage.NewMemberRaw(trafficBaggageVersion, "1")
		if err != nil {
			return nil, fmt.Errorf("create TrafficContext version baggage member: %w", err)
		}
		retained = append(retained, version)
		if value, found := traffic.Campaign(); found {
			member, err := baggage.NewMemberRaw(trafficBaggageCampaign, value)
			if err != nil {
				return nil, fmt.Errorf("create TrafficContext campaign baggage member: %w", err)
			}
			retained = append(retained, member)
		}
		if value, found := traffic.Lane(); found {
			member, err := baggage.NewMemberRaw(trafficBaggageLane, value)
			if err != nil {
				return nil, fmt.Errorf("create TrafficContext lane baggage member: %w", err)
			}
			retained = append(retained, member)
		}
		if value, found := traffic.Bucket(); found {
			member, err := baggage.NewMemberRaw(trafficBaggageBucket, strconv.FormatUint(uint64(value), 10))
			if err != nil {
				return nil, fmt.Errorf("create TrafficContext bucket baggage member: %w", err)
			}
			retained = append(retained, member)
		}
	}
	updated, err := baggage.New(retained...)
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry baggage: %w", err)
	}
	return baggage.ContextWithBaggage(parent, updated), nil
}

func trafficContextFromOpenTelemetryBaggage(otelBaggage baggage.Baggage) *TrafficContext {
	members := otelBaggage.Members()
	if len(members) == 0 {
		return nil
	}
	values := make([]string, 0, len(members))
	for _, member := range members {
		values = append(values, member.String())
	}
	traffic, err := ExtractTrafficContext(values)
	if err != nil {
		return nil
	}
	return traffic
}

func validateTrafficLabel(name, value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must contain only UTF-8", name)
	}
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > 128 {
		return "", trafficContextError(TrafficContextDiagnosticLabelTooLarge, "%s must not exceed 128 UTF-8 bytes", name)
	}
	for _, character := range value {
		if unicode.Is(unicode.Cc, character) {
			return "", fmt.Errorf("%s must not contain control characters", name)
		}
	}
	if strings.TrimFunc(value, isContractWhitespace) != value {
		return "", fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	return value, nil
}

type baggageMember struct {
	raw        string
	name       string
	value      string
	properties []string
}

func parseBaggageMembers(values []string) ([]baggageMember, error) {
	inputBytes := 0
	for index, value := range values {
		if index > 0 {
			inputBytes++
		}
		inputBytes += len(value)
	}
	if inputBytes > trafficBaggageByteLimit {
		return nil, trafficContextError(TrafficContextDiagnosticBaggageTooLarge, "baggage input exceeds %d bytes", trafficBaggageByteLimit)
	}
	members := make([]baggageMember, 0)
	for _, value := range values {
		for _, rawMember := range strings.Split(value, ",") {
			member := trimBaggageOWS(rawMember)
			if member == "" {
				return nil, fmt.Errorf("baggage contains an empty member")
			}
			parts := strings.Split(member, ";")
			separator := strings.IndexByte(parts[0], '=')
			if separator < 0 {
				return nil, fmt.Errorf("invalid baggage member %q", rawMember)
			}
			name := trimBaggageOWS(parts[0][:separator])
			memberValue := trimBaggageOWS(parts[0][separator+1:])
			if !isBaggageToken(name) || !isBaggageOctetValue(memberValue) {
				return nil, fmt.Errorf("invalid baggage member %q", rawMember)
			}
			properties := make([]string, 0, len(parts)-1)
			for _, rawProperty := range parts[1:] {
				property := trimBaggageOWS(rawProperty)
				if property == "" {
					return nil, fmt.Errorf("invalid baggage property in %q", rawMember)
				}
				propertySeparator := strings.IndexByte(property, '=')
				propertyName := property
				propertyValue := ""
				if propertySeparator >= 0 {
					propertyName = trimBaggageOWS(property[:propertySeparator])
					propertyValue = trimBaggageOWS(property[propertySeparator+1:])
				}
				if !isBaggageToken(propertyName) || !isBaggageOctetValue(propertyValue) {
					return nil, fmt.Errorf("invalid baggage property in %q", rawMember)
				}
				properties = append(properties, rawProperty)
			}
			members = append(members, baggageMember{raw: member, name: name, value: memberValue, properties: properties})
			if len(members) > trafficBaggageLimit {
				return nil, trafficContextError(TrafficContextDiagnosticTooManyMembers, "baggage has more than %d members", trafficBaggageLimit)
			}
		}
	}
	return members, nil
}

func trimBaggageOWS(value string) string {
	return strings.Trim(value, " \t")
}

func isTrafficBaggageName(name string) bool {
	return name == trafficBaggageVersion || name == trafficBaggageCampaign || name == trafficBaggageLane || name == trafficBaggageBucket
}

func duplicateTrafficContextError(name string) error {
	diagnostic := TrafficContextDiagnosticInvalidBaggage
	switch name {
	case trafficBaggageVersion:
		diagnostic = TrafficContextDiagnosticDuplicateVersion
	case trafficBaggageCampaign:
		diagnostic = TrafficContextDiagnosticDuplicateCampaign
	case trafficBaggageLane:
		diagnostic = TrafficContextDiagnosticDuplicateLane
	case trafficBaggageBucket:
		diagnostic = TrafficContextDiagnosticDuplicateBucket
	}
	return trafficContextError(diagnostic, "duplicate TrafficContext baggage member %q", name)
}

func isTrafficBaggagePrefix(name string) bool {
	return strings.HasPrefix(name, trafficBaggagePrefix)
}

func isBaggageToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character > 0x7f || !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || strings.ContainsRune("!#$%&'*+-.^_`|~", character)) {
			return false
		}
	}
	return true
}

func isBaggageOctetValue(value string) bool {
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current == 0x21 || current >= 0x23 && current <= 0x2b || current >= 0x2d && current <= 0x3a || current >= 0x3c && current <= 0x5b || current >= 0x5d && current <= 0x7e {
			continue
		}
		return false
	}
	return true
}

func decodeCanonicalTrafficLabel(value string) (string, error) {
	decoded := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		current := value[index]
		if isUnreserved(current) {
			decoded = append(decoded, current)
			continue
		}
		if current != '%' || index+2 >= len(value) || !isUpperHex(value[index+1]) || !isUpperHex(value[index+2]) {
			return "", trafficContextError(TrafficContextDiagnosticNonCanonicalValue, "value is not canonical percent encoding")
		}
		decoded = append(decoded, fromHex(value[index+1])<<4|fromHex(value[index+2]))
		index += 2
	}
	label := string(decoded)
	validated, err := validateTrafficLabel("label", label)
	if err != nil {
		return "", err
	}
	if encodeTrafficLabel(validated) != value {
		return "", trafficContextError(TrafficContextDiagnosticNonCanonicalValue, "value is not canonical percent encoding")
	}
	return validated, nil
}

func decodeTrafficBucket(value string) (uint16, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, trafficContextError(TrafficContextDiagnosticInvalidBucket, "invalid bucket %q", value)
	}
	for _, current := range value {
		if current < '0' || current > '9' {
			return 0, trafficContextError(TrafficContextDiagnosticInvalidBucket, "invalid bucket %q", value)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed > 9999 {
		return 0, trafficContextError(TrafficContextDiagnosticInvalidBucket, "invalid bucket %q", value)
	}
	return uint16(parsed), nil
}

func encodeTrafficLabel(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(value))
	for _, current := range []byte(value) {
		if isUnreserved(current) {
			encoded.WriteByte(current)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[current>>4])
		encoded.WriteByte(hexadecimal[current&0x0F])
	}
	return encoded.String()
}

func isUnreserved(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-' || value == '.' || value == '_' || value == '~'
}

func isUpperHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'F'
}

func fromHex(value byte) byte {
	if value >= '0' && value <= '9' {
		return value - '0'
	}
	return value - 'A' + 10
}
