package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// anonymousTagsListServer serves a fixed tags/list response with NO
// WWW-Authenticate challenge (anonymous pull, no auth handshake needed) —
// the minimal registry double needed to exercise genericClient.GetTags
// end-to-end, without pulling in the full Bearer/Basic auth-challenge
// flow auth_test.go's fixtures cover separately.
func anonymousTagsListServer(t *testing.T, repo string, tags []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		quoted := make([]string, len(tags))
		for i, tag := range tags {
			quoted[i] = `"` + tag + `"`
		}
		_, _ = w.Write([]byte(`{"name":"` + repo + `","tags":[` + strings.Join(quoted, ",") + `]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewGenericClient_GetTags_ReturnsExpectedTagsList(t *testing.T) {
	srv := anonymousTagsListServer(t, "org/repo", []string{"1.0.0", "2.0.0"})
	host := strings.TrimPrefix(srv.URL, "http://")
	httpClient := httpsToHTTPClient()

	client := NewGenericClient(httpClient, host)
	got, err := client.GetTags(context.Background(), "org/repo")
	if err != nil {
		t.Fatalf("Client.GetTags: %v", err)
	}
	want := []string{"1.0.0", "2.0.0"}
	if len(got.Tags) != len(want) {
		t.Fatalf("Client.GetTags(...).Tags = %v, want %v", got.Tags, want)
	}
	for i := range want {
		if string(got.Tags[i]) != want[i] {
			t.Errorf("Tags[%d]: want %v, got %v", i, want[i], got.Tags[i])
		}
	}
}

func TestNewGenericClient_GetImageMetadata_PropagatesInvalidPlatform(t *testing.T) {
	// Offline-safe: an invalid platform selector fails PlatformSelectorCodec.Decode
	// before any HTTP call is attempted, exactly like GetImageMetadataForHost
	// itself.
	client := NewGenericClient(nil, "example.invalid")
	_, err := client.GetImageMetadata(context.Background(), "org/repo", "latest", "not a valid platform selector!!")
	if err == nil {
		t.Fatal("Client.GetImageMetadata: want error for invalid platform, got nil")
	}
}

// countingObserver counts RecordRequest calls — the minimal stats.Observer
// double needed for TestNewGenericClient_BaseOptsAppliedBeforePerCallOpts.
type countingObserver struct {
	stats.NoopObserver
	count int
}

func (o *countingObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {
	o.count++
}

func TestNewGenericClient_CtxObserverOverridesBaseObserver(t *testing.T) {
	// GetTags/GetImageMetadata no longer accept a per-call opts parameter
	// (Client is a shared interface dockerhub.Client/ghcr.Client/
	// mcr.Client/genericClient all satisfy, so it cannot carry a
	// package-specific Option type) — a per-call Observer override is
	// reached via ctx instead (stats.WithObserver(ctx, obs) before
	// calling), the SAME "explicit per-call ctx wins" precedence
	// rest.Client.Call itself already gives every caller.
	srv := anonymousTagsListServer(t, "org/repo", []string{"1.0.0"})
	host := strings.TrimPrefix(srv.URL, "http://")
	httpClient := httpsToHTTPClient()

	var baseObs, callObs countingObserver
	client := NewGenericClient(httpClient, host, WithObserver(&baseObs))
	ctx := stats.WithObserver(context.Background(), &callObs)
	if _, err := client.GetTags(ctx, "org/repo"); err != nil {
		t.Fatalf("Client.GetTags: %v", err)
	}
	if baseObs.count != 0 {
		t.Errorf("base Observer: want 0 calls (overridden by ctx), got %d", baseObs.count)
	}
	if callObs.count == 0 {
		t.Error("ctx Observer: want at least 1 call, got 0")
	}
}
