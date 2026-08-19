package templatematch

import (
	"errors"
	"testing"
)

type mismatchError struct {
	Template string
	Concrete string
}

func (e mismatchError) Error() string {
	return "mismatch: " + e.Template + " vs " + e.Concrete
}

func wrapMismatchTest(template, concrete string) error {
	return mismatchError{Template: template, Concrete: concrete}
}

// ── MatchNonWildcard ─────────────────────────────────────────────────────────
// Mirrors api/internal's TestMatchTemplate_* matrix exactly — MatchNonWildcard
// must behave IDENTICALLY to api/internal.MatchTemplate (regression guard for
// the shared-core refactor).

func TestMatchNonWildcard_HappyPath(t *testing.T) {
	// {date} shares a segment with the literal ".json" suffix — must
	// correctly stop the capture before the literal, not swallow it.
	vars, err := MatchNonWildcard("readings/{sensorID}/{date}.json", "readings/sensor-42/2024-01-15.json", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchNonWildcard: %v", err)
	}
	want := map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"}
	if len(vars) != len(want) {
		t.Fatalf("vars: want %+v, got %+v", want, vars)
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("vars[%q]: want %q, got %q", k, v, vars[k])
		}
	}
}

func TestMatchNonWildcard_LiteralAndVarSegments(t *testing.T) {
	vars, err := MatchNonWildcard("users/{id}/posts/{postId}", "users/42/posts/99", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchNonWildcard: %v", err)
	}
	if vars["id"] != "42" || vars["postId"] != "99" {
		t.Errorf("unexpected vars: %+v", vars)
	}
}

func TestMatchNonWildcard_SegmentCountMismatch(t *testing.T) {
	_, err := MatchNonWildcard("a/{x}/b", "a/1/b/extra", wrapMismatchTest)
	if err == nil {
		t.Fatal("expected mismatch error for extra segment")
	}
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected mismatchError, got %T: %v", err, err)
	}
}

func TestMatchNonWildcard_LiteralSegmentMismatch(t *testing.T) {
	_, err := MatchNonWildcard("a/{x}/b", "a/1/c", wrapMismatchTest)
	if err == nil {
		t.Fatal("expected mismatch error for literal segment mismatch")
	}
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected mismatchError, got %T: %v", err, err)
	}
}

func TestMatchNonWildcard_MultipleVarsInOneSegment(t *testing.T) {
	vars, err := MatchNonWildcard("{year}-{month}.log", "2024-01.log", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchNonWildcard: %v", err)
	}
	if vars["year"] != "2024" || vars["month"] != "01" {
		t.Errorf("unexpected vars: %+v", vars)
	}
}

func TestMatchNonWildcard_NoVars(t *testing.T) {
	vars, err := MatchNonWildcard("static/path", "static/path", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchNonWildcard: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("expected no vars, got %+v", vars)
	}
}

// ── MatchMQTTWildcard ────────────────────────────────────────────────────────
// Mirrors adapters/mqtt's/adapters/mqtt5's TestTopicVarsFromMessage_* matrix —
// MatchMQTTWildcard must behave IDENTICALLY to both packages' pre-refactor
// matchTopicTemplate (regression guard for the shared-core refactor).

func TestMatchMQTTWildcard_SingleVar(t *testing.T) {
	vars, err := MatchMQTTWildcard("sensors/{sensorID}/measurements", "sensors/f47ac10b/measurements", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "f47ac10b" {
		t.Fatalf("want sensorID=f47ac10b, got %q", vars["sensorID"])
	}
}

func TestMatchMQTTWildcard_MultipleVars(t *testing.T) {
	vars, err := MatchMQTTWildcard("buildings/{buildingID}/rooms/{roomID}/sensors/{sensorID}", "buildings/b1/rooms/r2/sensors/s3", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["buildingID"] != "b1" || vars["roomID"] != "r2" || vars["sensorID"] != "s3" {
		t.Fatalf("unexpected vars: %+v", vars)
	}
}

func TestMatchMQTTWildcard_StaticTopic(t *testing.T) {
	vars, err := MatchMQTTWildcard("user/created", "user/created", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vars) != 0 {
		t.Fatalf("want empty map, got %v", vars)
	}
}

func TestMatchMQTTWildcard_PlusSingleLevelWildcard(t *testing.T) {
	vars, err := MatchMQTTWildcard("sensors/+/measurements", "sensors/abc123/measurements", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, exists := vars["+"]; exists {
		t.Fatal("want + not captured, but got entry in map")
	}
	if len(vars) != 0 {
		t.Fatalf("want empty map, got %v", vars)
	}
}

func TestMatchMQTTWildcard_HashMultiLevelWildcard(t *testing.T) {
	vars, err := MatchMQTTWildcard("sensors/{sensorID}/#", "sensors/abc123/measurements/raw/v1", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "abc123" {
		t.Fatalf("want sensorID=abc123, got %q", vars["sensorID"])
	}
	if vars["#"] != "measurements/raw/v1" {
		t.Fatalf("want #=measurements/raw/v1, got %q", vars["#"])
	}
}

func TestMatchMQTTWildcard_Mismatch_ExtraSegments(t *testing.T) {
	_, err := MatchMQTTWildcard("sensors/{sensorID}/data", "sensors/abc/extra/data", wrapMismatchTest)
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("want mismatchError, got %T: %v", err, err)
	}
}

func TestMatchMQTTWildcard_Mismatch_WrongLiteral(t *testing.T) {
	_, err := MatchMQTTWildcard("sensors/{sensorID}/data", "devices/abc/data", wrapMismatchTest)
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("want mismatchError, got %T: %v", err, err)
	}
}

func TestMatchMQTTWildcard_Mismatch_FewerSegments(t *testing.T) {
	_, err := MatchMQTTWildcard("sensors/{sensorID}/data", "sensors/abc", wrapMismatchTest)
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("want mismatchError, got %T: %v", err, err)
	}
}

// ── MatchDottedWildcard ──────────────────────────────────────────────────────
// Mirrors MatchMQTTWildcard's own test matrix exactly — same algorithm,
// "." delimiter instead of "/".

func TestMatchDottedWildcard_NamedVar(t *testing.T) {
	vars, err := MatchDottedWildcard("properties.desired.modules.{moduleName}", "properties.desired.modules.factory-gw", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["moduleName"] != "factory-gw" {
		t.Fatalf("want moduleName=factory-gw, got %q", vars["moduleName"])
	}
}

func TestMatchDottedWildcard_AnonymousWildcard(t *testing.T) {
	vars, err := MatchDottedWildcard("modules.{moduleName}.env.+", "modules.factory-gw.env.API_URL", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["moduleName"] != "factory-gw" {
		t.Fatalf("want moduleName=factory-gw, got %q", vars["moduleName"])
	}
	if _, exists := vars["+"]; exists {
		t.Fatal("want + not captured, but got entry in map")
	}
}

func TestMatchDottedWildcard_TrailingHashCatchAll_MatchesZeroOrMore(t *testing.T) {
	// Zero remaining segments — "#" must still match (bare module key).
	vars, err := MatchDottedWildcard("modules.{moduleName}.#", "modules.factory-gw", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error (zero remaining): %v", err)
	}
	if vars["#"] != "" {
		t.Fatalf("want #=\"\" for zero remaining segments, got %q", vars["#"])
	}

	// Multiple remaining segments — "#" captures the whole opaque tail.
	vars, err = MatchDottedWildcard("modules.{moduleName}.#", "modules.factory-gw.env.API_URL", wrapMismatchTest)
	if err != nil {
		t.Fatalf("unexpected error (multiple remaining): %v", err)
	}
	if vars["moduleName"] != "factory-gw" {
		t.Fatalf("want moduleName=factory-gw, got %q", vars["moduleName"])
	}
	if vars["#"] != "env.API_URL" {
		t.Fatalf("want #=env.API_URL, got %q", vars["#"])
	}
}

func TestMatchDottedWildcard_LiteralMismatch(t *testing.T) {
	_, err := MatchDottedWildcard("modules.{moduleName}.env", "devices.factory-gw.env", wrapMismatchTest)
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("want mismatchError, got %T: %v", err, err)
	}
}
