package llm

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/jsonschema"
)

// CallHandle is the protocol-agnostic runtime object returned by
// [Call.Register]/[Call.ClientHandle]. [adapters/openai] (or any future
// provider adapter) uses it; CallHandle itself never touches HTTP.
//
// CallHandle mirrors [rest.RouteHandle]/[events.ChannelHandle]/
// [reqreply.RouteHandle]/[mcp.ToolHandle]: it is a value that callers pass
// around and store. No magic, no global state.
type CallHandle[Req, Resp any] struct {
	// Name is the Call's name, as passed to [NewCall].
	Name string

	// SystemPrompt is the fully resolved system prompt (the file has already
	// been read if [SystemPromptFile] was used).
	SystemPrompt string

	// EncodeRequest renders req into the LLM's user-turn content string —
	// the default JSON encoding, or the [UserMessage] override.
	EncodeRequest func(req Req) (string, error)

	// ResponseSchema is the JSON Schema derived from the response codec —
	// passed to the provider as response_format/json_schema by the adapter.
	ResponseSchema json.RawMessage

	// DecodeResponse parses the LLM's raw completion content (already
	// constrained by ResponseSchema at the API level) through the response
	// codec — applying every Refine constraint exactly like any other
	// go-codex boundary. Errors are wrapped as [ResponseDecodeError].
	DecodeResponse func(raw []byte) (Resp, error)
}

// ClientHandle builds a [CallHandle] without registering against a
// [Builder] — for a Call used standalone, with no shared spec accumulation.
// Mirrors [rest.Route.ClientHandle]/[reqreply.Route.ClientHandle].
//
// Returns [SystemPromptFileError] if [SystemPromptFile] was used and its
// path cannot be read.
func (c Call[Req, Resp]) ClientHandle() (*CallHandle[Req, Resp], error) {
	h, _, err := c.build()
	return h, err
}

// Register validates the Call, resolves the system prompt (reading
// [SystemPromptFile] if used), renders request/response JSON Schemas, adds a
// [CallSpec] entry to b, and returns a [CallHandle]. Mirrors
// [rest.Route.Register]/[events.Subscriber.Register]/[reqreply.Route.Register].
//
// Returns an error if the Call's name is empty or already registered with b.
// Returns [SystemPromptFileError] if [SystemPromptFile] was used and its
// path cannot be read.
func (c Call[Req, Resp]) Register(b *Builder) (*CallHandle[Req, Resp], error) {
	if c.name == "" {
		return nil, fmt.Errorf("llm: call name must not be empty")
	}
	if _, dup := b.callNames[c.name]; dup {
		return nil, fmt.Errorf("llm: call %q already registered", c.name)
	}

	h, cb, err := c.build()
	if err != nil {
		return nil, err
	}

	reqSchema, err := jsonschema.Schema(c.reqCodec.Schema)
	if err != nil {
		return nil, fmt.Errorf("llm: call %q: render request schema: %w", c.name, err)
	}

	b.callNames[c.name] = struct{}{}
	b.calls = append(b.calls, CallSpec{
		Name:           c.name,
		Description:    cb.meta.Description,
		Tags:           cb.meta.Tags,
		SystemPrompt:   h.SystemPrompt,
		RequestSchema:  reqSchema,
		ResponseSchema: h.ResponseSchema,
	})

	return h, nil
}

// build resolves opts, the system prompt, and produces the CallHandle. Also
// returns the resolved callBuilder so Register can read CallMeta for its
// CallSpec entry.
func (c Call[Req, Resp]) build() (*CallHandle[Req, Resp], *callBuilder, error) {
	var cb callBuilder
	for _, opt := range c.opts {
		opt.applyCall(&cb)
	}

	systemPrompt := cb.systemPrompt
	if cb.hasSystemPromptFile {
		content, err := os.ReadFile(cb.systemPromptFile)
		if err != nil {
			return nil, nil, SystemPromptFileError{Path: cb.systemPromptFile, Err: err}
		}
		systemPrompt = string(content)
	}
	if cb.includeReqSchema {
		reqSchema, err := jsonschema.Schema(c.reqCodec.Schema)
		if err == nil && reqSchema != nil {
			systemPrompt += "\n\n```json\n" + string(reqSchema) + "\n```"
		}
	}

	jsonReq := format.JSON(c.reqCodec)
	jsonResp := format.JSON(c.respCodec)

	encodeRequest := func(req Req) (string, error) {
		b, err := jsonReq.Marshal(req)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if cb.userMessage != nil {
		fn, ok := cb.userMessage.(func(Req) (string, error))
		if !ok {
			return nil, nil, fmt.Errorf("llm: call %q: UserMessage type mismatch: want func(%T) (string, error), got %T",
				c.name, *new(Req), cb.userMessage)
		}
		encodeRequest = fn
	}

	respSchema, err := jsonschema.Schema(c.respCodec.Schema)
	if err != nil {
		return nil, nil, fmt.Errorf("llm: call %q: render response schema: %w", c.name, err)
	}

	name := c.name
	decodeResponse := func(raw []byte) (Resp, error) {
		v, err := jsonResp.Unmarshal(raw)
		if err != nil {
			var zero Resp
			return zero, ResponseDecodeError{Name: name, Raw: raw, Err: err}
		}
		return v, nil
	}

	return &CallHandle[Req, Resp]{
		Name:           c.name,
		SystemPrompt:   systemPrompt,
		EncodeRequest:  encodeRequest,
		ResponseSchema: respSchema,
		DecodeResponse: decodeResponse,
	}, &cb, nil
}
