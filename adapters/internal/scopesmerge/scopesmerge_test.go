package scopesmerge_test

import (
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
)

type fakeOut struct {
	GrantedScopes map[string][]string
}

type noScopesOut struct {
	Value string
}

// docs/design/d-0007-declarative-middleware-layering.md's "Prerequisite for
// Phase 2 (api/events)": the merge helper's own unit test matrix —
// legacy-only grants, bound-only grants, both merged, neither present —
// mirrors REST's adapters/internal/httpsecurity.MergeMiddlewareHandlerGrants
// test matrix.

func TestMergeHandlerGrants_LegacyOnly(t *testing.T) {
	granted := map[string][]string{"bearer": {"read:sensors"}}
	scopesmerge.MergeHandlerGrants(granted, nil, nil)
	want := map[string][]string{"bearer": {"read:sensors"}}
	if !reflect.DeepEqual(granted, want) {
		t.Errorf("granted = %v, want %v (legacy-only grants must survive untouched)", granted, want)
	}
}

func TestMergeHandlerGrants_BoundOnly(t *testing.T) {
	granted := map[string][]string{}
	satisfies := [][]string{{"bearer"}}
	outs := []any{fakeOut{GrantedScopes: map[string][]string{"bearer": {"write:sensors"}}}}
	scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	want := map[string][]string{"bearer": {"write:sensors"}}
	if !reflect.DeepEqual(granted, want) {
		t.Errorf("granted = %v, want %v", granted, want)
	}
}

func TestMergeHandlerGrants_Both(t *testing.T) {
	granted := map[string][]string{"bearer": {"read:sensors"}}
	satisfies := [][]string{{"apiKey"}}
	outs := []any{fakeOut{GrantedScopes: map[string][]string{"apiKey": {"admin"}}}}
	scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	want := map[string][]string{"bearer": {"read:sensors"}, "apiKey": {"admin"}}
	if !reflect.DeepEqual(granted, want) {
		t.Errorf("granted = %v, want %v", granted, want)
	}
}

func TestMergeHandlerGrants_Neither(t *testing.T) {
	granted := map[string][]string{}
	scopesmerge.MergeHandlerGrants(granted, nil, nil)
	if len(granted) != 0 {
		t.Errorf("granted = %v, want empty", granted)
	}
}

func TestMergeHandlerGrants_EmptySatisfies_Skipped(t *testing.T) {
	granted := map[string][]string{}
	satisfies := [][]string{nil} // general-purpose, no Security
	outs := []any{fakeOut{GrantedScopes: map[string][]string{"bearer": {"read"}}}}
	scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	if len(granted) != 0 {
		t.Errorf("granted = %v, want empty — empty Satisfies must contribute nothing", granted)
	}
}

func TestMergeHandlerGrants_NilOut_Skipped(t *testing.T) {
	granted := map[string][]string{}
	satisfies := [][]string{{"bearer"}}
	outs := []any{nil} // Subscribe's ORIGINAL 1-return shape, HasOut false
	scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	if len(granted) != 0 {
		t.Errorf("granted = %v, want empty — nil Out (no HasOut) must contribute nothing", granted)
	}
}

func TestMergeHandlerGrants_NoGrantedScopesField_Skipped(t *testing.T) {
	granted := map[string][]string{}
	satisfies := [][]string{{"bearer"}}
	outs := []any{noScopesOut{Value: "x"}}
	scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	if len(granted) != 0 {
		t.Errorf("granted = %v, want empty — Out with no GrantedScopes field must contribute nothing", granted)
	}
}
