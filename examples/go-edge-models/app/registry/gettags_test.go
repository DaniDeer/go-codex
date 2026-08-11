package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
)

func TestNewGetTagsToolHandler_PropagatesInvalidImageURL(t *testing.T) {
	// No network round trip needed: ParseImageRef fails before any HTTP
	// call is made for a malformed ImageURL, exactly like GetTags itself
	// (see models/docker/registry's TestImageRefCodec_RejectsInvalidShape
	// for the same underlying codec's own coverage) — this keeps the test
	// offline-safe, consistent with this package's IO-free-by-default test
	// philosophy.
	handler := NewGetTagsToolHandler(http.DefaultClient)
	_, err := handler(context.Background(), regmodels.GetTagsToolReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("handler error = %v, want ImageRefParseError", err)
	}
}

func TestGetTagsFiltered_PropagatesInvalidImageURL(t *testing.T) {
	// Same offline-safe early-exit as NewGetTagsToolHandler's own test
	// above: ParseImageRef fails before any HTTP call is attempted.
	_, err := GetTagsFiltered(context.Background(), http.DefaultClient, "INVALID IMAGE REF!!", nil)
	if err == nil {
		t.Fatal("GetTagsFiltered: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("GetTagsFiltered error = %v, want ImageRefParseError", err)
	}
}

// anonymousTagsListServer serves a fixed tags/list response with NO
// WWW-Authenticate challenge (anonymous pull, no auth handshake needed) —
// the minimal registry double needed to exercise GetTagsFiltered's own
// filter-application logic end-to-end, without pulling in the full
// Bearer/Basic auth-challenge flow auth_test.go's fixtures cover
// separately.
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

func TestGetTagsFiltered_AppliesFilterToRealCall(t *testing.T) {
	srv := anonymousTagsListServer(t, "org/repo", []string{"1.0.0", "latest", "2.0.0"})
	registryHost := strings.TrimPrefix(srv.URL, "http://")

	list, err := GetTagsFiltered(context.Background(), httpsToHTTPClient(),
		registryHost+"/org/repo", []docker.FilterTagsOpt{docker.WithLimit(2)})
	if err != nil {
		t.Fatalf("GetTagsFiltered: %v", err)
	}
	want := []docker.Tag{"2.0.0", "1.0.0"}
	if len(list.Tags) != len(want) || list.Tags[0] != want[0] || list.Tags[1] != want[1] {
		t.Errorf("GetTagsFiltered(...).Tags = %v, want %v", list.Tags, want)
	}
}

func TestNewGetTagsToolHandler_AppliesLimitAndSortFromReq(t *testing.T) {
	srv := anonymousTagsListServer(t, "org/repo", []string{"1.0.0", "latest", "2.0.0"})
	registryHost := strings.TrimPrefix(srv.URL, "http://")

	handler := NewGetTagsToolHandler(httpsToHTTPClient())
	list, err := handler(context.Background(), regmodels.GetTagsToolReq{
		ImageURL: registryHost + "/org/repo",
		Limit:    1,
		Sort:     "alphabetical",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	want := []docker.Tag{"1.0.0"}
	if len(list.Tags) != 1 || list.Tags[0] != want[0] {
		t.Errorf("handler(...).Tags = %v, want %v", list.Tags, want)
	}
}

func TestNewGetTagsToolHandler_UnrecognizedSortFallsBackToVersionDesc(t *testing.T) {
	srv := anonymousTagsListServer(t, "org/repo", []string{"1.0.0", "2.0.0"})
	registryHost := strings.TrimPrefix(srv.URL, "http://")

	handler := NewGetTagsToolHandler(httpsToHTTPClient())
	list, err := handler(context.Background(), regmodels.GetTagsToolReq{
		ImageURL: registryHost + "/org/repo",
		// Sort deliberately left empty — must fall back to SortByVersionDesc,
		// not error, since GetTagsToolReqCodec's own sortModeConstraint
		// already guarantees "" reaches the handler unmodified.
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	want := []docker.Tag{"2.0.0", "1.0.0"}
	if len(list.Tags) != 2 || list.Tags[0] != want[0] || list.Tags[1] != want[1] {
		t.Errorf("handler(...).Tags = %v, want %v", list.Tags, want)
	}
}
