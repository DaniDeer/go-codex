package smb

import (
	"context"
	"testing"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

type smbWidget struct {
	Name  string
	Value int
}

func smbWidgetCodec() codex.Codec[smbWidget] {
	return codex.Struct[smbWidget](
		codex.RequiredField("name", codex.String(),
			func(w smbWidget) string { return w.Name },
			func(w *smbWidget, v string) { w.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(w smbWidget) int { return w.Value },
			func(w *smbWidget, v int) { w.Value = v }),
	)
}

type smbCondWriteSpy struct {
	stats.NoopObserver
	native bool
	called bool
}

func (s *smbCondWriteSpy) RecordFileConditionalWrite(_ string, native bool, _ string, _ time.Duration) {
	s.called = true
	s.native = native
}

// TestConditionalWrite_Observer_ReportsNativeFalse_OnSMBBackend mirrors
// adapters/s3's own TestConditionalWrite_Observer_ReportsNativeTrue_OnS3Backend
// exactly, completing the native-true/native-false pair across all 3
// Backends (adapters/file/adapters/smb both report native=false — a
// lock-file emulation; adapters/s3 reports native=true — a genuinely
// atomic server-side PutObject If-Match).
func TestConditionalWrite_Observer_ReportsNativeFalse_OnSMBBackend(t *testing.T) {
	backend := newBackendFromOps(newFakeShare())
	client := apifile.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	res := apifile.NewResource[smbWidget]("{name}.json", format.JSON(smbWidgetCodec()), apifile.PathParam{Name: "name"})
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}

	if _, err := client.Create(ctx, res.WithCreate(apifile.Create{}), smbWidget{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entry, exists, err := client.Exists(ctx, res.WithExists(apifile.Exists{}), vars)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatalf("want exists=true")
	}

	spy := &smbCondWriteSpy{}
	ctx2 := stats.WithObserver(ctx, spy)
	if _, err := client.Create(ctx2, res.WithCreate(apifile.Create{}), smbWidget{Name: "widget", Value: 2}, vars, apifile.WriteOptions{IfMatch: &entry.ETag}); err != nil {
		t.Fatalf("Create (conditional overwrite): %v", err)
	}
	if !spy.called {
		t.Fatalf("want RecordFileConditionalWrite called")
	}
	if spy.native {
		t.Fatalf("want native=false for adapters/smb's lock-file-emulated conditional write")
	}
}
