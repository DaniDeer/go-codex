package internal

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

func TestMatchTemplate_HappyPath(t *testing.T) {
	// {date} shares a segment with the literal ".json" suffix — MatchTemplate
	// must correctly stop the capture before the literal, not swallow it.
	vars, err := MatchTemplate("readings/{sensorID}/{date}.json", "readings/sensor-42/2024-01-15.json", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchTemplate: %v", err)
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

func TestMatchTemplate_LiteralAndVarSegments(t *testing.T) {
	vars, err := MatchTemplate("users/{id}/posts/{postId}", "users/42/posts/99", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchTemplate: %v", err)
	}
	if vars["id"] != "42" || vars["postId"] != "99" {
		t.Errorf("unexpected vars: %+v", vars)
	}
}

func TestMatchTemplate_SegmentCountMismatch(t *testing.T) {
	_, err := MatchTemplate("a/{x}/b", "a/1/b/extra", wrapMismatchTest)
	if err == nil {
		t.Fatal("expected mismatch error for extra segment")
	}
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected mismatchError, got %T: %v", err, err)
	}
}

func TestMatchTemplate_LiteralSegmentMismatch(t *testing.T) {
	_, err := MatchTemplate("a/{x}/b", "a/1/c", wrapMismatchTest)
	if err == nil {
		t.Fatal("expected mismatch error for literal segment mismatch")
	}
	var me mismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected mismatchError, got %T: %v", err, err)
	}
}

func TestMatchTemplate_MultipleVarsInOneSegment(t *testing.T) {
	vars, err := MatchTemplate("{year}-{month}.log", "2024-01.log", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchTemplate: %v", err)
	}
	if vars["year"] != "2024" || vars["month"] != "01" {
		t.Errorf("unexpected vars: %+v", vars)
	}
}

func TestMatchTemplate_NoVars(t *testing.T) {
	vars, err := MatchTemplate("static/path", "static/path", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchTemplate: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("expected no vars, got %+v", vars)
	}
}
