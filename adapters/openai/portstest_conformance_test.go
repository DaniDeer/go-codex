package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/openai"
	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports/portstest"
)

// TestPortstestConformance_IOAdapterRoundTrip runs [openai.CallAdapter]
// against ports/portstest's canonical IOAdapter conformance scenario —
// this package's own, concrete proof that it satisfies
// ports.IOAdapter's contract, independent of this package's own
// hand-written tests. D-0010's "API-defined conformance test suite"
// mechanism, ports-family instance — adapters/openai is the second
// adapter (alongside adapters/sql) to prove the IOAdapter scenario,
// deliberately structurally independent (an LLM call vs. a DB lookup).
//
// Mocked via httptest.NewServer (mirrors this package's own existing
// TestCallAdapter_Transform_HappyPath convention — no real OpenAI API
// key/network access needed): the fake server reads the outgoing chat
// request's user-message content (the fed [portstest.Reading.ID], via
// [llm.UserMessage]), looks up the matching [portstest.WantReadings]
// record, and replies with its JSON encoding as the assistant message
// content — which [portstest.ReadingCodec] then decodes directly as the
// call's typed Resp, closing the round trip.
func TestPortstestConformance_IOAdapterRoundTrip(t *testing.T) {
	byID := make(map[string]portstest.Reading, len(portstest.WantReadings))
	for _, r := range portstest.WantReadings {
		byID[r.ID] = r
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var id string
		for _, m := range req.Messages {
			if m.Role == "user" {
				id = m.Content
			}
		}
		reading, ok := byID[id]
		if !ok {
			http.Error(w, "unknown id: "+id, http.StatusBadRequest)
			return
		}
		content, _ := format.JSON(portstest.ReadingCodec).Marshal(reading)
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": string(content)}},
			},
		})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	call := llm.NewCall[portstest.Reading, portstest.Reading]("portstest-io",
		portstest.ReadingCodec, portstest.ReadingCodec,
		llm.UserMessage(func(r portstest.Reading) (string, error) { return r.ID, nil }),
	)
	handle, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("ClientHandle: %v", err)
	}
	adapter := openai.CallAdapter(srv.Client(), handle, openai.CallAdapterOptions{
		BaseURL: srv.URL, Model: "gpt-4o-mini", APIKey: "test-key",
	})

	portstest.AssertIOAdapterRoundTrip(t, portstest.ReadingCodec, portstest.ReadingCodec, adapter)
}
