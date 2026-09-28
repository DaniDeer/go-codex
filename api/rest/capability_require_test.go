package rest

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// fakeLeveledCapability simulates an adapter's own sealed Capability type
// implementing LeveledCapability (e.g. a future zeromqrest.QoS) — used
// instead of importing a real adapter package directly, since
// adapters/nethttp/adapters/chi both import api/rest, and importing
// either back from an internal `package rest` test file would be an
// import cycle.
type fakeLeveledCapability struct {
	name  string
	level int
}

func (f fakeLeveledCapability) CapabilityName() string { return f.name }
func (f fakeLeveledCapability) Level() int             { return f.level }

// fakeNamedCapability is a plain CapabilityName-only fake (no Level()) —
// used to simulate a presence-only-checked capability.
type fakeNamedCapability struct{ name string }

func (f fakeNamedCapability) CapabilityName() string { return f.name }

// ── shared codecs for capability tests only ───────────────────────────

type capTestReq struct{ X int }
type capTestResp struct{ Y int }

var capTestReqCodec = codex.Struct[capTestReq](
	codex.RequiredField("x", codex.Int(),
		func(r capTestReq) int { return r.X },
		func(r *capTestReq, v int) { r.X = v },
	),
)

var capTestRespCodec = codex.Struct[capTestResp](
	codex.RequiredField("y", codex.Int(),
		func(r capTestResp) int { return r.Y },
		func(r *capTestResp, v int) { r.Y = v },
	),
)

func TestQoSLevel_String(t *testing.T) {
	cases := []struct {
		level QoSLevel
		want  string
	}{
		{AtMostOnce, "at-most-once"},
		{AtLeastOnce, "at-least-once"},
		{ExactlyOnce, "exactly-once"},
	}
	for _, c := range cases {
		if got := c.level.String(); got != c.want {
			t.Errorf("QoSLevel(%d).String() = %q, want %q", c.level, got, c.want)
		}
	}
}

func TestRequireQoS_ProducesCapabilityRequirement(t *testing.T) {
	opt := RequireQoS(ExactlyOnce)
	cr, ok := opt.(CapabilityRequirement)
	if !ok {
		t.Fatalf("want CapabilityRequirement, got %T", opt)
	}
	if cr.Name != "QoS" {
		t.Errorf("want Name %q, got %q", "QoS", cr.Name)
	}
	if cr.MinLevel == nil || *cr.MinLevel != int(ExactlyOnce) {
		t.Fatalf("want MinLevel=%d, got %v", int(ExactlyOnce), cr.MinLevel)
	}
	if want := "requires at least exactly-once delivery"; cr.Description != want {
		t.Errorf("want Description %q, got %q", want, cr.Description)
	}
}

func TestRequireHWM_ProducesCapabilityRequirement(t *testing.T) {
	opt := RequireHWM(100)
	cr, ok := opt.(CapabilityRequirement)
	if !ok {
		t.Fatalf("want CapabilityRequirement, got %T", opt)
	}
	if cr.Name != "HWM" {
		t.Errorf("want Name %q, got %q", "HWM", cr.Name)
	}
	if cr.MinLevel == nil || *cr.MinLevel != 100 {
		t.Fatalf("want MinLevel=100, got %v", cr.MinLevel)
	}
}

func TestNewRoute_AcceptsRequireHelpers(t *testing.T) {
	route := NewRoute[capTestReq, capTestResp]("POST", "/cap/require", capTestReqCodec, capTestRespCodec,
		RequireQoS(AtLeastOnce), RequireHWM(50),
	)
	handle := route.ClientHandle()
	if len(handle.Requirements) != 2 {
		t.Fatalf("want 2 requirements, got %d: %+v", len(handle.Requirements), handle.Requirements)
	}
}

func TestCheckCapabilityCoverage_RequireQoS_SufficientLevel_Passes(t *testing.T) {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireQoS(AtLeastOnce))}
	supplied := []any{fakeLeveledCapability{name: "QoS", level: int(ExactlyOnce)}}
	if err := CheckCapabilityCoverage("t", declared, supplied); err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
}

func TestCheckCapabilityCoverage_RequireQoS_ExactLevel_Passes(t *testing.T) {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireQoS(AtLeastOnce))}
	supplied := []any{fakeLeveledCapability{name: "QoS", level: int(AtLeastOnce)}}
	if err := CheckCapabilityCoverage("t", declared, supplied); err != nil {
		t.Fatalf("want nil error (equal level), got %v", err)
	}
}

func TestCheckCapabilityCoverage_RequireQoS_InsufficientLevel_ReturnsTypedError(t *testing.T) {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireQoS(ExactlyOnce))}
	supplied := []any{fakeLeveledCapability{name: "QoS", level: int(AtMostOnce)}}
	err := CheckCapabilityCoverage("t", declared, supplied)
	var cce *CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T (%v)", err, err)
	}
	if len(cce.Insufficient) != 1 {
		t.Fatalf("want 1 insufficient entry, got %+v", cce.Insufficient)
	}
	m := cce.Insufficient[0]
	if m.Name != "QoS" || m.Required != int(ExactlyOnce) || m.Supplied != int(AtMostOnce) {
		t.Errorf("want {QoS %d %d}, got %+v", int(ExactlyOnce), int(AtMostOnce), m)
	}
}

func TestCheckCapabilityCoverage_RequireHWM_InsufficientLevel_ReturnsTypedError(t *testing.T) {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireHWM(100))}
	supplied := []any{fakeLeveledCapability{name: "HWM", level: 10}}
	err := CheckCapabilityCoverage("t", declared, supplied)
	var cce *CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T (%v)", err, err)
	}
	if len(cce.Insufficient) != 1 || cce.Insufficient[0].Required != 100 || cce.Insufficient[0].Supplied != 10 {
		t.Fatalf("want 1 insufficient entry {HWM 100 10}, got %+v", cce.Insufficient)
	}
}

func TestCheckCapabilityCoverage_MissingRequirement_ReturnsTypedError(t *testing.T) {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireQoS(AtLeastOnce))}
	err := CheckCapabilityCoverage("t", declared, nil)
	var cce *CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T (%v)", err, err)
	}
	if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
		t.Errorf("want Missing=[QoS], got %v", cce.Missing)
	}
	if len(cce.Insufficient) != 0 {
		t.Errorf("want 0 insufficient entries, got %v", cce.Insufficient)
	}
}

func TestCheckCapabilityCoverage_PresenceOnly_Passes(t *testing.T) {
	declared := []CapabilityRequirement{{Name: "Custom"}}
	// A plain, non-leveled fake capability named "Custom" — presence
	// alone must be sufficient since MinLevel is nil.
	supplied := []any{fakeNamedCapability{name: "Custom"}}
	if err := CheckCapabilityCoverage("t", declared, supplied); err != nil {
		t.Fatalf("want nil error (presence-only), got %v", err)
	}
}

func TestCheckCapabilityCoverage_NonLeveledSuppliedAgainstLeveledRequirement(t *testing.T) {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireQoS(AtLeastOnce))}
	// Supplied capability matches by Name but does NOT implement
	// LeveledCapability at all.
	supplied := []any{fakeNamedCapability{name: "QoS"}}
	err := CheckCapabilityCoverage("t", declared, supplied)
	var cce *CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T (%v)", err, err)
	}
	if len(cce.Insufficient) != 1 || cce.Insufficient[0].Supplied != -1 {
		t.Fatalf("want 1 insufficient entry with Supplied=-1, got %+v", cce.Insufficient)
	}
}

func TestCapabilityCoverageError_LogValue(t *testing.T) {
	err := &CapabilityCoverageError{
		Route:        "POST /compute/x",
		Missing:      []string{"HWM"},
		Insufficient: []LevelMismatch{{Name: "QoS", Required: 2, Supplied: 0}},
	}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"route", "missing", "insufficient"} {
		if !keys[want] {
			t.Errorf("want key %q in LogValue group, got keys %v", want, keys)
		}
	}
}

func TestCapabilityCoverageError_ErrorsAs(t *testing.T) {
	base := &CapabilityCoverageError{Route: "t", Missing: []string{"QoS"}}
	wrapped := fmt.Errorf("wrapped: %w", base)
	var cce *CapabilityCoverageError
	if !errors.As(wrapped, &cce) {
		t.Fatal("want errors.As to reach *CapabilityCoverageError through wrapping")
	}
	if cce.Route != "t" {
		t.Errorf("want Route %q, got %q", "t", cce.Route)
	}
}

func ExampleRequireQoS() {
	route := NewRoute[capTestReq, capTestResp]("POST", "/cap/reading", capTestReqCodec, capTestRespCodec,
		RequireQoS(AtLeastOnce),
	)
	handle := route.ClientHandle()
	fmt.Println(handle.Requirements[0].Name)
	// Output: QoS
}

func ExampleRequireHWM() {
	declared := []CapabilityRequirement{mustCapabilityRequirement(RequireHWM(100))}
	// A supplied capability below the declared minimum is caught, not
	// silently accepted.
	supplied := []any{fakeLeveledCapability{name: "HWM", level: 10}}
	err := CheckCapabilityCoverage("POST /queue/depth", declared, supplied)
	fmt.Println(err)
	// Output: api/rest: route "POST /queue/depth" declares capabilities [] missing, [{HWM 100 10}] insufficient
}

// mustCapabilityRequirement asserts opt is a CapabilityRequirement (every
// RequireXxx helper returns one) — a small test-only helper avoiding
// repeated type-assertion boilerplate.
func mustCapabilityRequirement(opt RouteOpt) CapabilityRequirement {
	cr, ok := opt.(CapabilityRequirement)
	if !ok {
		panic(fmt.Sprintf("want CapabilityRequirement, got %T", opt))
	}
	return cr
}
