package pipeline_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/forge"
	pipeline "github.com/DaniDeer/go-codex/render/pipeline"
)

func TestRender_info_emitsAuthorAndApproval(t *testing.T) {
	reg := forge.NewRegistry("OEE Pipeline", "1.0.0").
		WithDescription("Signed OEE pipeline.").
		WithAuthor("OT Engineering").
		WithApproval("Quality Manager", "2024-03-01")

	spec := reg.Spec()
	out, err := pipeline.Render(spec)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	checks := map[string]string{
		"author":     "OT Engineering",
		"approvedBy": "Quality Manager",
		"approvedAt": "2024-03-01",
	}
	outStr := string(out)
	for key, want := range checks {
		if !strings.Contains(outStr, key) {
			t.Errorf("expected key %q in rendered output; got:\n%s", key, outStr)
		}
		if !strings.Contains(outStr, want) {
			t.Errorf("expected value %q in rendered output; got:\n%s", want, outStr)
		}
	}
}

func TestRender_info_omitsEmptyGovernanceFields(t *testing.T) {
	reg := forge.NewRegistry("Simple Pipeline", "1.0.0")
	spec := reg.Spec()
	out, err := pipeline.Render(spec)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	outStr := string(out)
	for _, key := range []string{"author", "approvedBy", "approvedAt"} {
		if strings.Contains(outStr, key) {
			t.Errorf("expected key %q to be absent when empty; got:\n%s", key, outStr)
		}
	}
}
