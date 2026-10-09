package rest_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
)

// This file proves LIVE, not just by reading the code, that
// rest.ErrorPatternAs/rest.Case/rest.HandleErrorPattern and
// reqreply.ErrorPatternAs/reqreply.Case/reqreply.HandleErrorPattern are
// structurally interchangeable — both are thin forwarding wrappers
// around the SAME shared internal/middleware implementation
// (docs/design/d-0009-internalize-shared-mechanics.md's Phase 4), so a value
// implementing [rest.ErrorPatternValuer] ALSO (trivially) implements
// [reqreply.ErrorPatternValuer] (both are Go type aliases to the
// identical internal/middleware.ErrorPatternValuer interface) and vice
// versa — a value built for one pattern's error-pattern machinery works
// unchanged against the other's.

type crossPatternPayload struct {
	Code string
}

// crossPatternValue implements BOTH rest.ErrorPatternValuer and
// reqreply.ErrorPatternValuer simultaneously via ONE method — proving
// they are the same interface, not just structurally compatible ones.
type crossPatternValue struct {
	value any
}

func (e crossPatternValue) Error() string          { return "cross-pattern error value" }
func (e crossPatternValue) ErrorPatternValue() any { return e.value }

func TestErrorPatternAs_CrossPattern_RestValueMatchedViaReqreply(t *testing.T) {
	// A value never touched by api/rest code — built, matched, and
	// type-asserted purely through api/reqreply's own wrapper.
	err := crossPatternValue{value: crossPatternPayload{Code: "rest-built"}}
	payload, ok := reqreply.ErrorPatternAs[crossPatternPayload](err)
	if !ok {
		t.Fatalf("want reqreply.ErrorPatternAs to match a value satisfying rest.ErrorPatternValuer too, got ok=false")
	}
	if payload.Code != "rest-built" {
		t.Errorf("code = %q, want rest-built", payload.Code)
	}
}

func TestErrorPatternAs_CrossPattern_ReqreplyValueMatchedViaRest(t *testing.T) {
	err := crossPatternValue{value: crossPatternPayload{Code: "reqreply-built"}}
	payload, ok := rest.ErrorPatternAs[crossPatternPayload](err)
	if !ok {
		t.Fatalf("want rest.ErrorPatternAs to match a value satisfying reqreply.ErrorPatternValuer too, got ok=false")
	}
	if payload.Code != "reqreply-built" {
		t.Errorf("code = %q, want reqreply-built", payload.Code)
	}
}

func TestHandleErrorPattern_CrossPattern_CaseInterchangeable(t *testing.T) {
	err := crossPatternValue{value: crossPatternPayload{Code: "shared"}}

	var gotViaRest, gotViaReqreply string
	handledRest := rest.HandleErrorPattern(err,
		rest.Case(func(p crossPatternPayload) { gotViaRest = p.Code }),
	)
	handledReqreply := reqreply.HandleErrorPattern(err,
		reqreply.Case(func(p crossPatternPayload) { gotViaReqreply = p.Code }),
	)

	if !handledRest || !handledReqreply {
		t.Fatalf("want both rest.HandleErrorPattern and reqreply.HandleErrorPattern to report handled=true, got rest=%v reqreply=%v", handledRest, handledReqreply)
	}
	if gotViaRest != "shared" || gotViaReqreply != "shared" {
		t.Errorf("gotViaRest=%q gotViaReqreply=%q, want both = shared", gotViaRest, gotViaReqreply)
	}
}
