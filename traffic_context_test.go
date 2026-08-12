package pole

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"google.golang.org/grpc/metadata"
)

func stringPointer(value string) *string {
	return &value
}

func uint16Pointer(value uint16) *uint16 {
	return &value
}

func TestTrafficContextExtractInjectAndValidation(t *testing.T) {
	traffic, err := NewTrafficContext(TrafficContextInput{
		Campaign: stringPointer("checkout v2"),
		Lane:     stringPointer("灰度"),
		Bucket:   uint16Pointer(7),
	})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}

	baggage, err := InjectTrafficContext(context.Background(), []string{
		"vendor=value;property=one",
		"latticehub.traffic.version=0,latticehub.traffic.lane=stale",
	}, traffic)
	if err != nil {
		t.Fatalf("InjectTrafficContext() error = %v", err)
	}
	if baggage == nil {
		t.Fatal("InjectTrafficContext() = nil, want baggage")
	}
	want := "vendor=value;property=one,latticehub.traffic.version=1,latticehub.traffic.campaign=checkout%20v2,latticehub.traffic.lane=%E7%81%B0%E5%BA%A6,latticehub.traffic.bucket=7"
	if *baggage != want {
		t.Fatalf("InjectTrafficContext() = %q, want %q", *baggage, want)
	}

	extracted, err := ExtractTrafficContext([]string{*baggage})
	if err != nil {
		t.Fatalf("ExtractTrafficContext() error = %v", err)
	}
	if !reflect.DeepEqual(extracted, traffic) {
		t.Fatalf("ExtractTrafficContext() = %#v, want %#v", extracted, traffic)
	}

	for _, value := range []string{
		"latticehub.traffic.lane=gray",
		"latticehub.traffic.version=1,latticehub.traffic.lane=%67ray",
		"latticehub.traffic.version=1,latticehub.traffic.lane=gray,latticehub.traffic.lane=other",
	} {
		if _, err := ExtractTrafficContext([]string{value}); err == nil {
			t.Fatalf("ExtractTrafficContext(%q) error = nil, want validation error", value)
		}
	}
}

func TestTrafficContextContextScopeAndExplicitPrecedence(t *testing.T) {
	current, err := NewTrafficContext(TrafficContextInput{Lane: stringPointer("current")})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	child, err := AttachTrafficContext(context.Background(), current)
	if err != nil {
		t.Fatalf("AttachTrafficContext() error = %v", err)
	}
	if got := CurrentTrafficContext(context.Background()); got != nil {
		t.Fatalf("CurrentTrafficContext(parent) = %#v, want nil", got)
	}
	if got := CurrentTrafficContext(child); !reflect.DeepEqual(got, current) {
		t.Fatalf("CurrentTrafficContext(child) = %#v, want %#v", got, current)
	}

	explicit, err := NewTrafficContext(TrafficContextInput{Lane: stringPointer("explicit")})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	baggage, err := InjectTrafficContext(child, nil, explicit)
	if err != nil {
		t.Fatalf("InjectTrafficContext() error = %v", err)
	}
	if baggage == nil || *baggage != "latticehub.traffic.version=1,latticehub.traffic.lane=explicit" {
		t.Fatalf("InjectTrafficContext() = %#v, want explicit context", baggage)
	}

	baggage, err = InjectTrafficContext(context.Background(), []string{"latticehub.traffic.version=1"}, nil)
	if err != nil {
		t.Fatalf("InjectTrafficContext() cleanup error = %v", err)
	}
	if baggage != nil {
		t.Fatalf("InjectTrafficContext() cleanup = %#v, want nil", baggage)
	}
}

func TestTrafficContextUsesOpenTelemetryBaggageStorage(t *testing.T) {
	vendor, err := baggage.NewMember("vendor", "value")
	if err != nil {
		t.Fatalf("baggage.NewMember() error = %v", err)
	}
	base, err := baggage.New(vendor)
	if err != nil {
		t.Fatalf("baggage.New() error = %v", err)
	}
	parent := baggage.ContextWithBaggage(context.Background(), base)
	traffic, err := NewTrafficContext(TrafficContextInput{Lane: stringPointer("gray")})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	attached, err := AttachTrafficContext(parent, traffic)
	if err != nil {
		t.Fatalf("AttachTrafficContext() error = %v", err)
	}
	if got := baggage.FromContext(attached).Member(trafficBaggageLane).Value(); got != "gray" {
		t.Fatalf("OTel baggage lane = %q, want gray", got)
	}
	if got := baggage.FromContext(attached).Member("vendor").Value(); got != "value" {
		t.Fatalf("OTel baggage vendor = %q, want value", got)
	}
	reset := ResetTrafficContext(attached)
	if got := baggage.FromContext(reset).Member(trafficBaggageLane).Key(); got != "" {
		t.Fatalf("ResetTrafficContext() retained OTel baggage key %q", got)
	}
	if got := CurrentTrafficContext(reset); got != nil {
		t.Fatalf("CurrentTrafficContext(ResetTrafficContext()) = %#v, want nil", got)
	}

	otelBaggage, err := baggage.New(
		mustBaggageMember(t, trafficBaggageVersion, "1"),
		mustBaggageMember(t, trafficBaggageCampaign, "checkout-v2"),
	)
	if err != nil {
		t.Fatalf("baggage.New() error = %v", err)
	}
	if got := CurrentTrafficContext(baggage.ContextWithBaggage(context.Background(), otelBaggage)); got == nil {
		t.Fatal("CurrentTrafficContext() = nil, want OTel Baggage fallback")
	} else if campaign, found := got.Campaign(); !found || campaign != "checkout-v2" {
		t.Fatalf("CurrentTrafficContext() campaign = %q, %t", campaign, found)
	}
}

func TestTrafficContextOpenTelemetryBaggageUsesRawDomainValues(t *testing.T) {
	traffic, err := NewTrafficContext(TrafficContextInput{
		Campaign: stringPointer("checkout v2"),
		Lane:     stringPointer("灰度%20literal"),
	})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	attached, err := AttachTrafficContext(context.Background(), traffic)
	if err != nil {
		t.Fatalf("AttachTrafficContext() error = %v", err)
	}
	otelBaggage := baggage.FromContext(attached)
	if got := otelBaggage.Member(trafficBaggageCampaign).String(); got != "latticehub.traffic.campaign=checkout%20v2" {
		t.Fatalf("OTel campaign member = %q", got)
	}
	if got := otelBaggage.Member(trafficBaggageLane).String(); got != "latticehub.traffic.lane=%E7%81%B0%E5%BA%A6%2520literal" {
		t.Fatalf("OTel lane member = %q", got)
	}
	if got := CurrentTrafficContext(baggage.ContextWithBaggage(context.Background(), otelBaggage)); !reflect.DeepEqual(got, traffic) {
		t.Fatalf("CurrentTrafficContext() = %#v, want %#v", got, traffic)
	}
}

func mustBaggageMember(t *testing.T, key, value string) baggage.Member {
	t.Helper()
	member, err := baggage.NewMemberRaw(key, value)
	if err != nil {
		t.Fatalf("baggage.NewMemberRaw(%q, %q) error = %v", key, value, err)
	}
	return member
}

func TestTrafficContextAllowsEmptyLabelsAndCleansReservedBaggage(t *testing.T) {
	empty, err := NewTrafficContext(TrafficContextInput{})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	if campaign, found := empty.Campaign(); found || campaign != "" {
		t.Fatalf("Campaign() = %q, %t; want empty", campaign, found)
	}
	extracted, err := ExtractTrafficContext([]string{"latticehub.traffic.version=1"})
	if err != nil {
		t.Fatalf("ExtractTrafficContext() error = %v", err)
	}
	if extracted == nil {
		t.Fatal("ExtractTrafficContext() = nil, want empty TrafficContext")
	}
	baggage, err := InjectTrafficContext(nil, []string{
		"vendor=value,latticehub.traffic.version=1,latticehub.traffic.lane=stale",
	}, empty)
	if err != nil {
		t.Fatalf("InjectTrafficContext() error = %v", err)
	}
	if baggage == nil || *baggage != "vendor=value" {
		t.Fatalf("InjectTrafficContext() = %#v, want foreign baggage only", baggage)
	}
	baggage, err = InjectTrafficContext(nil, nil, empty)
	if err != nil {
		t.Fatalf("InjectTrafficContext() empty error = %v", err)
	}
	if baggage != nil {
		t.Fatalf("InjectTrafficContext() = %#v, want carrier deletion", baggage)
	}
}

func TestTargetServiceInjectsTrafficContextAcrossCarriers(t *testing.T) {
	target, err := NewTargetService("default", "orders")
	if err != nil {
		t.Fatalf("NewTargetService() error = %v", err)
	}
	traffic, err := NewTrafficContext(TrafficContextInput{Lane: stringPointer("gray")})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	requestContext, err := AttachTrafficContext(context.Background(), traffic)
	if err != nil {
		t.Fatalf("AttachTrafficContext() error = %v", err)
	}

	headers, err := target.EncodeHTTPHeadersWithTrafficContext(requestContext, http.Header{
		"BAGGAGE":                     {"vendor=value,latticehub.traffic.version=0"},
		"Latticehub-Target-Namespace": {"forged"},
		"Latticehub-Target-Service":   {"forged"},
		"X-Request-ID":                {"request-123"},
	}, nil)
	if err != nil {
		t.Fatalf("EncodeHTTPHeadersWithTrafficContext() error = %v", err)
	}
	if got := headers.Get("baggage"); got != "vendor=value,latticehub.traffic.version=1,latticehub.traffic.lane=gray" {
		t.Fatalf("HTTP baggage = %q", got)
	}
	if got := headers.Get(HeaderTargetService); got != "orders" {
		t.Fatalf("HTTP TargetService = %q", got)
	}
	for name := range headers {
		if strings.EqualFold(name, HeaderBaggage) && name != "Baggage" {
			t.Fatalf("HTTP Baggage retained non-canonical key %q", name)
		}
	}

	grpcMetadata, err := target.EncodeGRPCMetadataWithTrafficContext(requestContext, metadata.MD{
		"BAGGAGE": {"vendor=value"},
	}, nil)
	if err != nil {
		t.Fatalf("EncodeGRPCMetadataWithTrafficContext() error = %v", err)
	}
	if got := grpcMetadata.Get("baggage"); !reflect.DeepEqual(got, []string{"vendor=value,latticehub.traffic.version=1,latticehub.traffic.lane=gray"}) {
		t.Fatalf("gRPC baggage = %#v", got)
	}
	for name := range grpcMetadata {
		if strings.EqualFold(name, HeaderBaggage) && name != HeaderBaggage {
			t.Fatalf("gRPC Baggage retained non-canonical key %q", name)
		}
	}

	attachments, err := target.EncodeAttachmentsWithTrafficContext(requestContext, map[string]string{
		"baggage": "vendor=value",
	}, nil)
	if err != nil {
		t.Fatalf("EncodeAttachmentsWithTrafficContext() error = %v", err)
	}
	if got := attachments["baggage"]; got != "vendor=value,latticehub.traffic.version=1,latticehub.traffic.lane=gray" {
		t.Fatalf("Dubbo baggage = %q", got)
	}
}

func TestTrafficContextBaggageParserSupportsW3CExternalSyntax(t *testing.T) {
	traffic, err := NewTrafficContext(TrafficContextInput{Lane: stringPointer("gray")})
	if err != nil {
		t.Fatalf("NewTrafficContext() error = %v", err)
	}
	baggage, err := InjectTrafficContext(nil, []string{
		" \tvendor \t= \t; property \t= \tvalue\t ",
		"\tother=\t",
	}, traffic)
	if err != nil {
		t.Fatalf("InjectTrafficContext() error = %v", err)
	}
	if baggage == nil {
		t.Fatal("InjectTrafficContext() = nil, want baggage")
	}
	want := "vendor \t= \t; property \t= \tvalue,other=,latticehub.traffic.version=1,latticehub.traffic.lane=gray"
	if *baggage != want {
		t.Fatalf("InjectTrafficContext() = %q, want %q", *baggage, want)
	}

	extracted, err := ExtractTrafficContext([]string{
		"\tlatticehub.traffic.version \t= \t1 ",
		" latticehub.traffic.lane = gray\t",
	})
	if err != nil {
		t.Fatalf("ExtractTrafficContext() error = %v", err)
	}
	if lane, found := extracted.Lane(); !found || lane != "gray" {
		t.Fatalf("ExtractTrafficContext() lane = %q, %t", lane, found)
	}

	for _, value := range []string{
		"vendor=hello world",
		"vendor=value; bad property=value",
		"vendor=\"value",
	} {
		if _, err := InjectTrafficContext(nil, []string{value}, traffic); err == nil {
			t.Fatalf("InjectTrafficContext(%q) error = nil, want validation error", value)
		}
	}
	caseSensitive, err := InjectTrafficContext(nil, []string{"LatticeHub.Traffic.Lane=foreign"}, traffic)
	if err != nil {
		t.Fatalf("InjectTrafficContext() case-sensitive foreign key error = %v", err)
	}
	if caseSensitive == nil || *caseSensitive != "LatticeHub.Traffic.Lane=foreign,latticehub.traffic.version=1,latticehub.traffic.lane=gray" {
		t.Fatalf("InjectTrafficContext() case-sensitive foreign key = %#v", caseSensitive)
	}
	if _, err := ExtractTrafficContext([]string{strings.Repeat("a", 8192), "b"}); err == nil {
		t.Fatal("ExtractTrafficContext() error = nil, want combined input limit error")
	}
}
