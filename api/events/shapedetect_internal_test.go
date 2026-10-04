package events

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/route"
)

// docs/design/d-0007-declarative-middleware-layering.md's "Prerequisite for
// Phase 2 (api/events)": direct, package-internal unit tests for
// isBoundSubscribeMWShapeWithOut — pinning the exact return-arity-based
// detection logic, independent of the mqtt5 end-to-end test (which
// proves the dispatch WORKS but not which shape-detection branch fired).

type shapeDetectT struct{ Value string }
type shapeDetectIn struct{ Token string }
type shapeDetectOut struct{ GrantedScopes map[string][]string }

func TestIsBoundSubscribeMWShapeWithOut_DetectsTwoReturnShape(t *testing.T) {
	fn := func(ctx context.Context, msg *shapeDetectT, in shapeDetectIn) (shapeDetectOut, error) {
		return shapeDetectOut{}, nil
	}
	if !isBoundSubscribeMWShapeWithOut[shapeDetectT](fn) {
		t.Error("want true for the 2-return bound shape")
	}
}

func TestIsBoundSubscribeMWShapeWithOut_RejectsOneReturnShape(t *testing.T) {
	fn := func(ctx context.Context, msg *shapeDetectT, in shapeDetectIn) error {
		return nil
	}
	if isBoundSubscribeMWShapeWithOut[shapeDetectT](fn) {
		t.Error("want false for the ORIGINAL 1-return shape — must not be misdetected as the new shape")
	}
}

func TestIsBoundSubscribeMWShapeWithOut_RejectsLegacyMqttZeromqShape(t *testing.T) {
	// mqtt/zeromq's legacy subscribe-security Fn: func(ctx, *T,
	// []route.SecurityRequirement) error — 1-return, rejected by NumOut.
	fn := func(ctx context.Context, msg *shapeDetectT, secReqs []route.SecurityRequirement) error {
		return nil
	}
	if isBoundSubscribeMWShapeWithOut[shapeDetectT](fn) {
		t.Error("want false for mqtt/zeromq's legacy 1-return shape")
	}
}

// TestIsBoundSubscribeMWShapeWithOut_RejectsMqtt5LegacyShape confirms the
// 2nd-param-type check correctly rejects mqtt5's OWN legacy subscribe-
// security Fn, which is ALSO 3-in/2-out but has a concrete adapter type
// (not *T) as its 2nd param.
func TestIsBoundSubscribeMWShapeWithOut_RejectsMqtt5LegacyShape(t *testing.T) {
	type fakePublish struct{}
	fn := func(ctx context.Context, msg *fakePublish, secReqs []route.SecurityRequirement) (map[string][]string, error) {
		return nil, nil
	}
	if isBoundSubscribeMWShapeWithOut[shapeDetectT](fn) {
		t.Error("want false — 2nd param is *fakePublish, not *shapeDetectT")
	}
}

func TestIsBoundSubscribeMWShape_StillDetectsOriginalShape(t *testing.T) {
	fn := func(ctx context.Context, msg *shapeDetectT, in shapeDetectIn) error {
		return nil
	}
	if !isBoundSubscribeMWShape[shapeDetectT](fn) {
		t.Error("want true for the ORIGINAL 1-return bound shape — must remain unaffected by the new shape's addition")
	}
}
