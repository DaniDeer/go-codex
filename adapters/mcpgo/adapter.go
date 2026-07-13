package mcpgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/stats"
)

// HandlerFunc[In, Out] is the typed application handler for MCP tools.
// ctx is the request context. in is the decoded, validated input value.
// Return an error to surface it as a tool error to the LLM client.
type HandlerFunc[In, Out any] func(ctx context.Context, in In) (Out, error)

// ResourceHandlerFunc[T] is the typed application handler for MCP resources.
// uri is the concrete resource URI from the client request (placeholders are
// already substituted). Use [apimcp.ResourceHandle.ValidateURIVars] with
// vars extracted from uri if additional per-variable validation is needed.
type ResourceHandlerFunc[T any] func(ctx context.Context, uri string) (T, error)

// PromptHandlerFunc is the application handler for MCP prompts.
// args is the key-value map of prompt arguments provided by the client.
// Return a slice of [PromptMessage] to send back to the LLM.
type PromptHandlerFunc func(ctx context.Context, args map[string]string) ([]PromptMessage, error)

// PromptMessage is a single message in a prompt response.
// Role must be "user" or "assistant".
type PromptMessage struct {
	Role    string
	Content string
}

// Options configures the behaviour of [RegisterTool], [RegisterResource],
// and [RegisterPrompt].
type Options struct {
	// Observer, when non-nil, receives per-call lifecycle events: latency,
	// status, and per-field validation errors.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// ---------------------------------------------------------------------------
// Tool
// ---------------------------------------------------------------------------

// ToolHandler builds an [mcp.Tool] descriptor and a [server.ToolHandlerFunc]
// from the given [apimcp.ToolHandle] and application handler.
//
// The returned handler validates the incoming arguments via the codec before
// calling fn. Input errors (decode/validation failures) are returned to the
// LLM as [mcp.CallToolResult] with IsError: true. Output encode errors are
// returned as protocol-level errors.
//
// Use [RegisterTool] to wire both to an [server.MCPServer] in one step. Use
// ToolHandler directly when you need to manage registration yourself (e.g.
// with [mcptest.Server]).
func ToolHandler[In, Out any](
	handle *apimcp.ToolHandle[In, Out],
	fn HandlerFunc[In, Out],
	opts Options,
) (mcp.Tool, server.ToolHandlerFunc) {
	tool := mcp.NewTool(handle.Name, mcp.WithDescription(handle.Description))
	if len(handle.InputSchema) > 0 {
		tool.RawInputSchema = handle.InputSchema
	}

	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Resolve observer per-call: explicit opts.Observer beats context observer.
		obs := opts.Observer
		if obs == nil {
			obs = stats.ObserverFromContext(ctx)
		}
		start := time.Now()
		var err error
		if to, ok := obs.(stats.TraceObserver); ok {
			ctx = to.StartSpan(ctx, "mcp.tool", handle.Name)
			defer func() { to.EndSpan(ctx, err) }()
		}

		// Decode + validate input from the MCP arguments.
		var args any
		if e := req.BindArguments(&args); e != nil {
			err = e
			stats.ReportErrors(obs, "input", e)
			obs.RecordRequest("tool", handle.Name, 400, time.Since(start))
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %s", e)), nil
		}

		input, err := handle.Decode(args)
		if err != nil {
			stats.ReportErrors(obs, "input", err)
			obs.RecordRequest("tool", handle.Name, 400, time.Since(start))
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Call the application handler.
		output, err := fn(ctx, input)
		if err != nil {
			obs.RecordRequest("tool", handle.Name, 500, time.Since(start))
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Encode the output via codec (validates output constraints).
		data, err := handle.Encode(output)
		if err != nil {
			// Output encode error is a server-side contract violation — return
			// as protocol error, not a tool error (client cannot recover).
			obs.RecordRequest("tool", handle.Name, 500, time.Since(start))
			var toe apimcp.ToolOutputError
			if errors.As(err, &toe) {
				return nil, fmt.Errorf("tool %q: output contract violation: %w", handle.Name, err)
			}
			return nil, err
		}
		err = nil

		obs.RecordRequest("tool", handle.Name, 200, time.Since(start))
		return mcp.NewToolResultStructured(json.RawMessage(data), string(data)), nil
	}

	return tool, handler
}

// RegisterTool wires a [apimcp.ToolHandle] and application handler to an
// [server.MCPServer]. It is equivalent to:
//
//	tool, h := mcpgo.ToolHandler(handle, fn, opts)
//	s.AddTool(tool, h)
func RegisterTool[In, Out any](
	s *server.MCPServer,
	handle *apimcp.ToolHandle[In, Out],
	fn HandlerFunc[In, Out],
	opts Options,
) {
	tool, handler := ToolHandler(handle, fn, opts)
	s.AddTool(tool, handler)
}

// ---------------------------------------------------------------------------
// Resource
// ---------------------------------------------------------------------------

// ResourceHandler builds the mcp resource descriptor(s) and handler function
// from the given [apimcp.ResourceHandle] and application handler.
//
// If the URI template contains {varName} placeholders, a resource template is
// returned (isTemplate=true) and the caller should use
// [server.MCPServer.AddResourceTemplate]. Otherwise a plain resource is
// returned (isTemplate=false) and the caller should use
// [server.MCPServer.AddResource].
//
// Use [RegisterResource] to handle both cases automatically.
func ResourceHandler[T any](
	handle *apimcp.ResourceHandle[T],
	fn ResourceHandlerFunc[T],
	opts Options,
) (resource mcp.Resource, template mcp.ResourceTemplate, isTemplate bool, handler server.ResourceHandlerFunc) {
	isTemplate = len(handle.URITemplate) > 0 && containsPlaceholder(handle.URITemplate)

	handlerFn := func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		// Resolve observer per-call: explicit opts.Observer beats context observer.
		obs := opts.Observer
		if obs == nil {
			obs = stats.ObserverFromContext(ctx)
		}
		start := time.Now()
		var err error
		if to, ok := obs.(stats.TraceObserver); ok {
			ctx = to.StartSpan(ctx, "mcp.resource", handle.URITemplate)
			defer func() { to.EndSpan(ctx, err) }()
		}
		result, err := fn(ctx, req.Params.URI)
		if err != nil {
			obs.RecordRequest("resource", handle.URITemplate, 500, time.Since(start))
			return nil, err
		}
		data, err := handle.Encode(result)
		if err != nil {
			obs.RecordRequest("resource", handle.URITemplate, 500, time.Since(start))
			return nil, err
		}
		obs.RecordRequest("resource", handle.URITemplate, 200, time.Since(start))
		return []mcp.ResourceContents{mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: handle.MimeType,
			Text:     string(data),
		}}, nil
	}

	if isTemplate {
		tmpl := mcp.NewResourceTemplate(
			handle.URITemplate,
			handle.Name,
			mcp.WithTemplateDescription(handle.Description),
			mcp.WithTemplateMIMEType(handle.MimeType),
		)
		return mcp.Resource{}, tmpl, true, handlerFn
	}

	res := mcp.NewResource(
		handle.URITemplate,
		handle.Name,
		mcp.WithResourceDescription(handle.Description),
		mcp.WithMIMEType(handle.MimeType),
	)
	return res, mcp.ResourceTemplate{}, false, handlerFn
}

// RegisterResource wires a [apimcp.ResourceHandle] and application handler to
// an [server.MCPServer]. URI templates (containing {varName}) are registered
// via [server.MCPServer.AddResourceTemplate]; literal URIs via
// [server.MCPServer.AddResource].
func RegisterResource[T any](
	s *server.MCPServer,
	handle *apimcp.ResourceHandle[T],
	fn ResourceHandlerFunc[T],
	opts Options,
) {
	res, tmpl, isTemplate, handler := ResourceHandler(handle, fn, opts)
	if isTemplate {
		s.AddResourceTemplate(tmpl, server.ResourceTemplateHandlerFunc(handler))
	} else {
		s.AddResource(res, handler)
	}
}

// ---------------------------------------------------------------------------
// Prompt
// ---------------------------------------------------------------------------

// PromptHandler builds an [mcp.Prompt] descriptor and a [server.PromptHandlerFunc]
// from the given [apimcp.PromptHandle] and application handler.
//
// The returned handler validates the incoming arguments via [PromptHandle.ValidateArgs]
// before calling fn. Validation errors are returned as protocol-level errors.
//
// Use [RegisterPrompt] to wire both to an [server.MCPServer] in one step.
func PromptHandler(
	handle *apimcp.PromptHandle,
	fn PromptHandlerFunc,
	opts Options,
) (mcp.Prompt, server.PromptHandlerFunc) {
	promptOpts := []mcp.PromptOption{mcp.WithPromptDescription(handle.Description)}
	for _, arg := range handle.Args {
		argOpts := []mcp.ArgumentOption{mcp.ArgumentDescription(arg.Description)}
		if arg.Required {
			argOpts = append(argOpts, mcp.RequiredArgument())
		}
		promptOpts = append(promptOpts, mcp.WithArgument(arg.Name, argOpts...))
	}
	prompt := mcp.NewPrompt(handle.Name, promptOpts...)

	handler := func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		// Resolve observer per-call: explicit opts.Observer beats context observer.
		obs := opts.Observer
		if obs == nil {
			obs = stats.ObserverFromContext(ctx)
		}
		start := time.Now()
		var err error
		if to, ok := obs.(stats.TraceObserver); ok {
			ctx = to.StartSpan(ctx, "mcp.prompt", handle.Name)
			defer func() { to.EndSpan(ctx, err) }()
		}
		args := req.Params.Arguments

		// Validate arguments via declared PromptArg codecs.
		if e := handle.ValidateArgs(args); e != nil {
			err = e
			stats.ReportErrors(obs, "prompt.args", e)
			obs.RecordRequest("prompt", handle.Name, 400, time.Since(start))
			return nil, fmt.Errorf("prompt %q: %w", handle.Name, e)
		}

		messages, err := fn(ctx, args)
		if err != nil {
			obs.RecordRequest("prompt", handle.Name, 500, time.Since(start))
			return nil, err
		}

		obs.RecordRequest("prompt", handle.Name, 200, time.Since(start))
		return buildPromptResult(messages), nil
	}

	return prompt, handler
}

// RegisterPrompt wires a [apimcp.PromptHandle] and application handler to
// an [server.MCPServer].
func RegisterPrompt(
	s *server.MCPServer,
	handle *apimcp.PromptHandle,
	fn PromptHandlerFunc,
	opts Options,
) {
	prompt, handler := PromptHandler(handle, fn, opts)
	s.AddPrompt(prompt, handler)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildPromptResult converts go-codex PromptMessages to an mcp.GetPromptResult.
func buildPromptResult(msgs []PromptMessage) *mcp.GetPromptResult {
	mcpMsgs := make([]mcp.PromptMessage, len(msgs))
	for i, m := range msgs {
		role := mcp.RoleUser
		if m.Role == "assistant" {
			role = mcp.RoleAssistant
		}
		mcpMsgs[i] = mcp.PromptMessage{
			Role:    role,
			Content: mcp.NewTextContent(m.Content),
		}
	}
	return &mcp.GetPromptResult{Messages: mcpMsgs}
}

// containsPlaceholder reports whether s contains at least one {varName} placeholder.
func containsPlaceholder(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			return true
		}
	}
	return false
}
