package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/stats"
)

// CallAdapterOptions configures [CallAdapter].
type CallAdapterOptions struct {
	// BaseURL defaults to "https://api.openai.com/v1". Point at any
	// OpenAI-compatible endpoint (Azure OpenAI, Ollama, vLLM, LM Studio, ...).
	BaseURL string

	// Model is the model identifier sent on every request (e.g. "gpt-4o-mini").
	Model string

	// APIKey is sent as `Authorization: Bearer <APIKey>`. Use CredentialFunc
	// instead for per-request/rotating credentials.
	APIKey string
	// CredentialFunc, if set, is called per request and takes priority over
	// APIKey — mirrors [nethttp.CallOptions.CredentialFunc]'s role, adapted
	// to a single bearer-token string (OpenAI's wire format has no
	// structured security-requirement negotiation to pass through).
	CredentialFunc func(ctx context.Context) (string, error)

	// Temperature, MaxTokens: optional, nil/0 = provider default.
	Temperature *float64
	MaxTokens   *int

	// MaxRetries bounds the re-prompt-on-invalid-completion loop (default 0
	// = no retry; the first codec-validation failure is returned as-is).
	// On failure, the adapter appends the invalid assistant response plus a
	// new user message describing the validation error, then re-sends the
	// full conversation — up to MaxRetries additional attempts.
	MaxRetries int

	Observer stats.Observer
}

// ── wire format (OpenAI Chat Completions — also used by Azure OpenAI, Ollama,
// vLLM, LM Studio, Groq, and other OpenAI-compatible providers) ────────────

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type jsonSchemaFormat struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type responseFormat struct {
	Type       string           `json:"type"`
	JSONSchema jsonSchemaFormat `json:"json_schema"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

// complete performs the full completion round trip for one request,
// including the retry-on-invalid-completion loop. It never touches
// gstream/ports directly — binding.go wraps this per stream item.
func complete[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	handle *llm.CallHandle[Req, Resp],
	req Req,
	opts CallAdapterOptions,
) (Resp, error) {
	var zero Resp

	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	userContent, err := handle.EncodeRequest(req)
	if err != nil {
		obs.RecordRequest("llm", handle.Name, 0, 0)
		return zero, err
	}

	messages := []chatMessage{
		{Role: "system", Content: handle.SystemPrompt},
		{Role: "user", Content: userContent},
	}

	maxAttempts := opts.MaxRetries + 1
	var lastDecodeErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		start := time.Now()

		body := chatRequest{
			Model:    opts.Model,
			Messages: messages,
			ResponseFormat: &responseFormat{
				Type: "json_schema",
				JSONSchema: jsonSchemaFormat{
					Name:   handle.Name,
					Schema: handle.ResponseSchema,
					Strict: true,
				},
			},
			Temperature: opts.Temperature,
			MaxTokens:   opts.MaxTokens,
		}

		reqBytes, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			obs.RecordRequest("llm", handle.Name, 0, time.Since(start))
			return zero, RequestBuildError{Err: marshalErr}
		}

		httpReq, buildErr := http.NewRequestWithContext(ctx, http.MethodPost,
			baseURL+"/chat/completions", bytes.NewReader(reqBytes))
		if buildErr != nil {
			obs.RecordRequest("llm", handle.Name, 0, time.Since(start))
			return zero, RequestBuildError{Err: buildErr}
		}
		httpReq.Header.Set("Content-Type", "application/json")

		token := opts.APIKey
		if opts.CredentialFunc != nil {
			var credErr error
			token, credErr = opts.CredentialFunc(ctx)
			if credErr != nil {
				obs.RecordRequest("llm", handle.Name, 0, time.Since(start))
				return zero, credErr
			}
		}
		if token != "" {
			httpReq.Header.Set("Authorization", "Bearer "+token)
		}

		resp, doErr := client.Do(httpReq)
		if doErr != nil {
			obs.RecordRequest("llm", handle.Name, 0, time.Since(start))
			return zero, RequestError{Model: opts.Model, Err: doErr}
		}

		statusCode := resp.StatusCode
		respBytes, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
			return zero, ResponseBodyError{Err: readErr}
		}
		if closeErr != nil {
			obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
			return zero, ResponseBodyError{Err: closeErr}
		}

		if statusCode < 200 || statusCode >= 300 {
			obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
			return zero, UnexpectedStatusError{Model: opts.Model, StatusCode: statusCode, Body: string(respBytes)}
		}

		var parsed chatResponse
		if unmarshalErr := json.Unmarshal(respBytes, &parsed); unmarshalErr != nil {
			obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
			return zero, ResponseBodyError{Err: unmarshalErr}
		}
		if len(parsed.Choices) == 0 {
			obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
			return zero, NoChoicesError{Model: opts.Model}
		}

		content := parsed.Choices[0].Message.Content
		decoded, decodeErr := handle.DecodeResponse([]byte(content))
		if decodeErr != nil {
			stats.ReportErrors(obs, "response", decodeErr)
			obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
			lastDecodeErr = decodeErr
			if opts.MaxRetries == 0 {
				// No retry configured: return the first decode failure as-is
				// (a plain llm.ResponseDecodeError), not wrapped in
				// RetriesExhaustedError — that type specifically signals
				// "retries were attempted and still failed."
				return zero, decodeErr
			}
			if attempt+1 < maxAttempts {
				messages = append(messages,
					chatMessage{Role: "assistant", Content: content},
					chatMessage{Role: "user", Content: fmt.Sprintf(
						"Your last response did not match the required schema: %v. Please try again.", decodeErr)},
				)
				continue
			}
			return zero, RetriesExhaustedError{Model: opts.Model, Attempts: maxAttempts, LastErr: lastDecodeErr}
		}

		obs.RecordRequest("llm", handle.Name, statusCode, time.Since(start))
		return decoded, nil
	}

	// Unreachable: maxAttempts >= 1 always, so the loop always returns.
	return zero, RetriesExhaustedError{Model: opts.Model, Attempts: maxAttempts, LastErr: lastDecodeErr}
}
