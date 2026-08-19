package iothub

import (
	"errors"
	"testing"
)

func TestRouteCodec_EncodeDecodeRoundTrip_BrokeredEndpoint(t *testing.T) {
	route := Route{
		From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
		To:   NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
	}
	raw, err := RouteCodec.Encode(route)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := `FROM /messages/modules/factory-mqtt-gateway-1/outputs/telemetry INTO BrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest")`
	if raw != want {
		t.Errorf("Encode = %q, want %q", raw, want)
	}
	got, err := RouteCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != route {
		t.Errorf("round-trip = %+v, want %+v", got, route)
	}
}

func TestRouteCodec_EncodeDecodeRoundTrip_Upstream(t *testing.T) {
	route := Route{From: "/messages/modules/factory-normalizer/outputs/output", To: UpstreamTarget}
	raw, err := RouteCodec.Encode(route)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := `FROM /messages/modules/factory-normalizer/outputs/output INTO $upstream`
	if raw != want {
		t.Errorf("Encode = %q, want %q", raw, want)
	}
	got, err := RouteCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != route {
		t.Errorf("round-trip = %+v, want %+v", got, route)
	}
}

func TestRouteCodec_DecodeRejectsMalformedString(t *testing.T) {
	_, err := RouteCodec.Decode("not a route at all")
	if err == nil {
		t.Fatal("Decode: want error for malformed route string, got nil")
	}
	var invalidErr InvalidRouteError
	if !errors.As(err, &invalidErr) {
		t.Errorf("Decode error = %v, want InvalidRouteError", err)
	}
}

func TestRouteCodec_EncodeRejectsEmptyFrom(t *testing.T) {
	_, err := RouteCodec.Encode(Route{To: UpstreamTarget})
	if err == nil {
		t.Error("Encode: want error for empty From, got nil")
	}
}

func TestRouteCodec_EncodeRejectsEmptyBrokeredEndpoint(t *testing.T) {
	_, err := RouteCodec.Encode(Route{From: "/messages/modules/x/outputs/y", To: RouteTarget{Kind: RouteTargetBrokeredEndpoint}})
	if err == nil {
		t.Error("Encode: want error for empty BrokeredEndpoint target, got nil")
	}
}

func TestInvalidRouteError_LogValue(t *testing.T) {
	err := InvalidRouteError{Raw: "garbage"}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
	lv := err.LogValue()
	found := false
	for _, a := range lv.Group() {
		if a.Key == "raw" && a.Value.String() == "garbage" {
			found = true
		}
	}
	if !found {
		t.Error("LogValue missing raw attribute")
	}
}

func TestRouteNameCodec_EncodeDecodeRoundTrip(t *testing.T) {
	name := RouteName("factory-mqtt-to-ingest")
	raw, err := RouteNameCodec.Encode(name)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if raw != "properties.desired.routes.factory-mqtt-to-ingest" {
		t.Errorf("Encode = %v, want properties.desired.routes.factory-mqtt-to-ingest", raw)
	}
	got, err := RouteNameCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != name {
		t.Errorf("round-trip = %q, want %q", got, name)
	}
}

func TestRouteNameCodec_RejectsWrongPrefix(t *testing.T) {
	_, err := RouteNameCodec.Decode("properties.desired.modules.factory-cache")
	if err == nil {
		t.Error("Decode: want error for wrong key prefix, got nil")
	}
}

func TestRoutesCodec_EncodeDecodeRoundTrip(t *testing.T) {
	routes := Routes{
		"factory-mqtt-to-ingest": {
			From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
			To:   NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
		},
		"factory-normalizer-upstream": {
			From: "/messages/modules/factory-normalizer/outputs/output",
			To:   UpstreamTarget,
		},
	}
	raw, err := RoutesCodec.Encode(routes)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := RoutesCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(routes) {
		t.Fatalf("round-trip len = %d, want %d", len(got), len(routes))
	}
	for name, want := range routes {
		if got[name] != want {
			t.Errorf("route %q = %+v, want %+v", name, got[name], want)
		}
	}
}

func TestLayeredModulesContentCodec_WithoutEdgeHub_DecodesEmptyRoutes(t *testing.T) {
	raw := map[string]any{
		"$edgeAgent": map[string]any{},
	}
	got, err := LayeredModulesContentCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.EdgeHub) != 0 {
		t.Errorf("EdgeHub = %v, want empty when $edgeHub absent", got.EdgeHub)
	}
}

func TestLayeredModulesContentCodec_WithEdgeHub_RoundTrip(t *testing.T) {
	mc := LayeredModulesContent{
		EdgeAgent: Modules{},
		EdgeHub: Routes{
			"factory-mqtt-to-ingest": {
				From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
				To:   NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
			},
		},
	}
	raw, err := LayeredModulesContentCodec.Encode(mc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := LayeredModulesContentCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.EdgeHub) != 1 || got.EdgeHub["factory-mqtt-to-ingest"] != mc.EdgeHub["factory-mqtt-to-ingest"] {
		t.Errorf("EdgeHub round-trip = %+v, want %+v", got.EdgeHub, mc.EdgeHub)
	}
}
