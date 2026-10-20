package s3_test

import (
	"context"
	"testing"
	"time"

	adapters3 "github.com/DaniDeer/go-codex/adapters/s3"
	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

type widget struct {
	Name  string
	Value int
}

func widgetCodec() codex.Codec[widget] {
	return codex.Struct[widget](
		codex.RequiredField("name", codex.String(),
			func(w widget) string { return w.Name },
			func(w *widget, v string) { w.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(w widget) int { return w.Value },
			func(w *widget, v int) { w.Value = v }),
	)
}

type condWriteSpy struct {
	stats.NoopObserver
	native bool
	called bool
}

func (s *condWriteSpy) RecordFileConditionalWrite(_ string, native bool, _ string, _ time.Duration) {
	s.called = true
	s.native = native
}

// TestConditionalWrite_Observer_ReportsNativeTrue_OnS3Backend closes the
// test row deferred during api/file's own Stage 1 retrofit (see
// docs/design/d-0011-file-api-backend-transparency.md's "Conditional writes" Unit test plan) —
// proves adapters/s3's genuinely-native conditional write is reported
// via stats.FileObserver.RecordFileConditionalWrite with native=true,
// completing the native/approximated pair alongside the filesystem
// backend's own native=false proof (api/file/client_test.go,
// ports/file_test.go).
func TestConditionalWrite_Observer_ReportsNativeTrue_OnS3Backend(t *testing.T) {
	fake := newFakeS3("bucket")
	backend := adapters3.NewBackend(fake, "bucket")
	client := apifile.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	res := apifile.NewResource[widget]("{name}.json", format.JSON(widgetCodec()), apifile.PathParam{Name: "name"})
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}

	if _, err := client.Create(ctx, res.WithCreate(apifile.Create{}), widget{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entry, exists, err := client.Exists(ctx, res.WithExists(apifile.Exists{}), vars)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatalf("want exists=true")
	}

	spy := &condWriteSpy{}
	ctx2 := stats.WithObserver(ctx, spy)
	if _, err := client.Create(ctx2, res.WithCreate(apifile.Create{}), widget{Name: "widget", Value: 2}, vars, apifile.WriteOptions{IfMatch: &entry.ETag}); err != nil {
		t.Fatalf("Create (conditional overwrite): %v", err)
	}
	if !spy.called {
		t.Fatalf("want RecordFileConditionalWrite called")
	}
	if !spy.native {
		t.Fatalf("want native=true for adapters/s3's genuinely atomic conditional write")
	}
}
