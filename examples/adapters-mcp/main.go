// Package main demonstrates the go-codex MCP server adapter.
//
// This example shows the three-layer workflow applied to MCP:
//
//   - Layer 1 — Codecs: define CalcInput, CalcOutput, Item, and their codecs.
//   - Layer 2 — api/mcp: declare Tool, Resource, Prompt as package-level values,
//     register with a Builder, and inspect the static MCPSpec.
//   - Layer 3 — adapters/mcpgo: wire handles to a mark3labs/mcp-go server
//     with an Observer for metrics and slog for structured logging.
//
// The Observer wired into [mcpgo.Options] receives one event per tool/resource/
// prompt call: RecordRequest("tool", name, statusCode, latency). Codec validation
// failures additionally fire RecordValidationError per field, enabling per-field
// error counters without any metrics library dependency.
//
// The server is started in stdio mode when SERVE=1 is set (see [runServer]).
// By default (SERVE unset) the example simulates calls, prints the observer
// summary, and exits (see [runDemo]). The two modes are kept in separate
// functions with SEPARATE loggers deliberately: stdio transport requires
// stdout to carry ONLY the JSON-RPC protocol stream, so [runServer] logs to
// stderr exclusively, while [runDemo] is free to log to stdout for
// readability since no MCP client reads that stream in demo mode.
//
// Run with: go run ./examples/adapters-mcp
// Run as a real stdio MCP server (e.g. from Claude Desktop's config): SERVE=1 go run ./examples/adapters-mcp
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sync"
	"time"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	"github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// It records call counts, status codes, validation error locations, and latencies.
// In production replace the counters with Prometheus or OpenTelemetry instruments
// — the interface is identical.
// CountingObserver collects per-call metrics for MCP primitives.
// Logging is handled separately via stats.NewLoggingObserver — no slog calls here.
// In production replace counters with Prometheus / OpenTelemetry instruments.
type CountingObserver struct {
	stats.NoopObserver
	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int
	latencies      []time.Duration
}

// RecordRequest implements [stats.Observer]. method is "tool", "resource", or
// "prompt"; path is the primitive name.
func (o *CountingObserver) RecordRequest(_ string, _ string, statusCode int, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byStatus == nil {
		o.byStatus = make(map[int]int)
	}
	o.total++
	o.byStatus[statusCode]++
	o.latencies = append(o.latencies, d)
}

// RecordValidationError implements [stats.ValidationObserver]. Called once per
// failing field when a codec validation error occurs in a tool/resource/prompt call.
func (o *CountingObserver) RecordValidationError(location, _, _ string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.valErrorsByLoc == nil {
		o.valErrorsByLoc = make(map[string]int)
	}
	o.valErrorsByLoc[location]++
}

// Print writes a human-readable summary to stdout.
func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total calls    : %d\n", o.total)
	for code, n := range o.byStatus {
		fmt.Printf("  status %-3d     : %d\n", code, n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs %-9s: %d\n", "("+loc+")", n)
	}
	if len(o.latencies) > 0 {
		var sum time.Duration
		for _, l := range o.latencies {
			sum += l
		}
		fmt.Printf("  avg latency    : %v\n", sum/time.Duration(len(o.latencies)))
	}
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

type CalcInput struct {
	A  float64
	B  float64
	Op string
}

type CalcOutput struct {
	Result float64
	Expr   string
}

type Item struct {
	ID    string
	Name  string
	Price float64
}

// ---------------------------------------------------------------------------
// Layer 1 — Codecs
// ---------------------------------------------------------------------------

var positiveFloat = codex.Float64().Refine(validate.MinFloat(0.0))

var calcInputCodec = codex.Struct[CalcInput](
	codex.RequiredField("a", positiveFloat,
		func(c CalcInput) float64 { return c.A },
		func(c *CalcInput, v float64) { c.A = v },
	),
	codex.RequiredField("b", positiveFloat,
		func(c CalcInput) float64 { return c.B },
		func(c *CalcInput, v float64) { c.B = v },
	),
	codex.RequiredField("op",
		codex.String().Refine(validate.OneOf("+", "-", "*", "/")),
		func(c CalcInput) string { return c.Op },
		func(c *CalcInput, v string) { c.Op = v },
	),
)

var calcOutputCodec = codex.Struct[CalcOutput](
	codex.RequiredField("result", codex.Float64(),
		func(c CalcOutput) float64 { return c.Result },
		func(c *CalcOutput, v float64) { c.Result = v },
	),
	codex.RequiredField("expr", codex.String(),
		func(c CalcOutput) string { return c.Expr },
		func(c *CalcOutput, v string) { c.Expr = v },
	),
)

var itemCodec = codex.Struct[Item](
	codex.RequiredField("id", codex.String(),
		func(i Item) string { return i.ID },
		func(i *Item, v string) { i.ID = v },
	),
	codex.RequiredField("name",
		codex.String().Refine(validate.NonEmptyString),
		func(i Item) string { return i.Name },
		func(i *Item, v string) { i.Name = v },
	),
	codex.RequiredField("price", positiveFloat,
		func(i Item) float64 { return i.Price },
		func(i *Item, v float64) { i.Price = v },
	),
)

// ---------------------------------------------------------------------------
// Layer 2 — Declare tool, resource, and prompt as package-level values
// ---------------------------------------------------------------------------

var calcTool = mcp.NewTool[CalcInput, CalcOutput]("calculate",
	calcInputCodec, calcOutputCodec,
	mcp.ToolMeta{
		Description: "Perform arithmetic on two non-negative numbers. Op must be +, -, *, or /.",
		Tags:        []string{"math"},
	},
)

// itemResource declares "{id}" via mcp.URIParam + codex.IdentityField —
// V=string serves as its own field container for this single-var template
// (the same idiom examples/go-edge-models/models/iotedge/usecase/config.go's
// Template[Name] uses), matching rest.NewRoute/events.NewChannel's exact
// "bare string + opts" call shape.
var itemResource = mcp.NewResource[string](
	"items://{id}",
	itemCodec,
	mcp.ResourceMeta{
		Name:        "Item",
		Description: "Look up an item by its ID.",
		MimeType:    "application/json",
	},
	mcp.URIParam(codex.IdentityField("id", codex.String().Refine(validate.NonEmptyString))),
)

var summaryPrompt = mcp.NewPrompt("summarize",
	mcp.PromptMeta{Description: "Generate a prompt that asks the LLM to summarize content."},
	mcp.PromptArg{Name: "content", Description: "The content to summarize.", Required: true},
	mcp.PromptArg{Name: "style", Description: "Output style: bullet-points or paragraph."},
)

// ── Logging decorator ─────────────────────────────────────────────────────────

// withLogging wraps a handler function, logging success (Info) or failure
// (Error) after the handler returns. This separates the logging concern from
// the handler body, keeping Layer 3 business logic clean while providing
// consistent observability across all tool handlers.
func withLogging[In, Out any](
	name string,
	handler mcpgo.HandlerFunc[In, Out],
	logger *slog.Logger,
) mcpgo.HandlerFunc[In, Out] {
	return func(ctx context.Context, in In) (Out, error) {
		out, err := handler(ctx, in)
		if err != nil {
			logger.ErrorContext(ctx, name+" failed", "error", err)
		} else {
			logger.InfoContext(ctx, name+" succeeded")
		}
		return out, err
	}
}

// ---------------------------------------------------------------------------
// Layer 3 — Application handlers
// ---------------------------------------------------------------------------

func handleCalculate(_ context.Context, in CalcInput) (CalcOutput, error) {
	var result float64
	switch in.Op {
	case "+":
		result = in.A + in.B
	case "-":
		result = in.A - in.B
	case "*":
		result = in.A * in.B
	case "/":
		if in.B == 0 {
			return CalcOutput{}, fmt.Errorf("division by zero")
		}
		result = in.A / in.B
	}
	return CalcOutput{
		Result: result,
		Expr:   fmt.Sprintf("%.4g %s %.4g = %.4g", in.A, in.Op, in.B, result),
	}, nil
}

var itemStore = map[string]Item{
	"1": {ID: "1", Name: "Widget", Price: 9.99},
	"2": {ID: "2", Name: "Gadget", Price: 24.99},
}

func handleItem(_ context.Context, uri string) (Item, error) {
	// Extract the id from the URI "items://{id}"
	id := uri
	if len(uri) > 8 { // "items://"
		id = uri[8:]
	}
	item, ok := itemStore[id]
	if !ok {
		return Item{}, fmt.Errorf("item %q not found", id)
	}
	return item, nil
}

func handleSummarize(_ context.Context, args map[string]string) ([]mcpgo.PromptMessage, error) {
	style := args["style"]
	if style == "" {
		style = "paragraph"
	}
	content := args["content"]
	userMsg := fmt.Sprintf("Please summarize the following content as a %s:\n\n%s", style, content)
	return []mcpgo.PromptMessage{
		{Role: "user", Content: userMsg},
	}, nil
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	// ── Layer 2: Register all primitives with the Builder ──────────────────
	b := mcp.NewBuilder(mcp.Info{Name: "go-codex Demo Server", Version: "1.0.0"})

	toolHandle, err := calcTool.Register(b)
	if err != nil {
		log.Fatal(err)
	}
	resHandle, err := itemResource.Register(b)
	if err != nil {
		log.Fatal(err)
	}
	promptHandle, err := summaryPrompt.Register(b)
	if err != nil {
		log.Fatal(err)
	}

	// The SERVE=1 branch is checked FIRST and takes over the process's
	// stdin/stdout entirely for the JSON-RPC protocol stream — runDemo's
	// stdout output (MCPSpec dump, simulated calls, observer summary) must
	// never run in that mode, or it would corrupt the very first bytes a
	// real MCP client (e.g. Claude Desktop) reads from this process.
	if os.Getenv("SERVE") == "1" {
		runServer(b, toolHandle, resHandle, promptHandle)
		return
	}
	runDemo(b, toolHandle, resHandle, promptHandle)
}

// runDemo prints the static MCPSpec, simulates a handful of tool/resource/
// prompt calls in-process, and prints an observer summary. Safe to log to
// stdout here — no MCP client is attached to this process's stdout in demo
// mode, unlike [runServer]'s stdio transport.
func runDemo(
	b *mcp.Builder,
	toolHandle *mcp.ToolHandle[CalcInput, CalcOutput],
	resHandle *mcp.ResourceHandle[string, Item],
	promptHandle *mcp.PromptHandle,
) {
	// Structured logger — text format to stdout for readability.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	// metrics collects per-call counters. In production replace with Prometheus / OTel.
	// Logging is handled separately by stats.NewLoggingObserver — no mixing of concerns.
	metrics := &CountingObserver{}
	obs := stats.NewFanout(
		metrics,
		stats.NewLoggingObserver(logger.With("component", "mcp")),
	)
	opts := mcpgo.Options{Observer: obs}

	// Print the static MCPSpec (analogous to OpenAPI/AsyncAPI spec).
	spec, err := b.MCPSpec()
	if err != nil {
		log.Fatal(err)
	}
	specJSON, _ := json.MarshalIndent(spec, "", "  ")
	fmt.Printf("=== MCPSpec ===\n%s\n\n", specJSON)

	// ── Simulated calls (no network — handler invoked directly) ────────────
	//
	// ToolHandler returns the raw mcp.Tool descriptor and a server.ToolHandlerFunc
	// so we can call the handler in-process without starting a server.
	// This demonstrates the observer and slog output.

	toolMCPTool, calcHandler := mcpgo.ToolHandler(
		toolHandle,
		withLogging("calculate", handleCalculate, logger.With("handler", "calculate")),
		opts,
	)
	_ = toolMCPTool // used when RegisterTool is called in runServer

	ctx := context.Background()
	fmt.Println("=== Simulated tool calls ===")

	// 1. Valid call: 10 + 5
	callTool(ctx, logger, calcHandler, "10 + 5", map[string]any{"a": 10.0, "b": 5.0, "op": "+"})

	// 2. Valid call: 8 / 4
	callTool(ctx, logger, calcHandler, "8 / 4", map[string]any{"a": 8.0, "b": 4.0, "op": "/"})

	// 3. Input validation error: negative number (violates MinFloat(0.0) constraint).
	//    The observer fires RecordValidationError for each failing field.
	callTool(ctx, logger, calcHandler, "−3 + 5 (invalid: a < 0)", map[string]any{"a": -3.0, "b": 5.0, "op": "+"})

	// 4. Input validation error: invalid op (violates OneOf constraint).
	callTool(ctx, logger, calcHandler, "2 % 3 (invalid: op not in enum)", map[string]any{"a": 2.0, "b": 3.0, "op": "%"})

	// 5. Handler error: division by zero.
	callTool(ctx, logger, calcHandler, "5 / 0 (handler error)", map[string]any{"a": 5.0, "b": 0.0, "op": "/"})

	// 6. BuildURI — demonstrate ResourceHandle.BuildURI with a codec failure.
	fmt.Println("\n=== ResourceHandle.BuildURI ===")
	uri, err := resHandle.BuildURI("item-1")
	if err == nil {
		fmt.Printf("  built URI: %s\n", uri)
	}
	_, err = resHandle.BuildURI("") // empty string fails NonEmptyString
	if err != nil {
		var ve codex.ValidationErrors
		if errors.As(err, &ve) {
			logger.Error("BuildURI failed", "error", ve)
		}
	}

	// 7. PromptHandle.ValidateArgs — demonstrate required arg check.
	fmt.Println("\n=== PromptHandle.ValidateArgs ===")
	if err := promptHandle.ValidateArgs(map[string]string{"content": "hello world"}); err == nil {
		fmt.Println("  valid args: OK")
	}
	if err := promptHandle.ValidateArgs(map[string]string{}); err != nil {
		var me mcp.MissingPromptArgError
		if errors.As(err, &me) {
			logger.Error("ValidateArgs failed", "arg", me.Name)
		}
	}

	fmt.Println("\n=== Observer summary ===")
	metrics.Print()
	fmt.Println("\n(set SERVE=1 to start the stdio MCP server)")
}

// runServer starts the MCP server on stdio — the transport used by local
// LLM clients such as Claude Desktop.
//
// IMPORTANT: stdio transport requires stdout to carry ONLY the JSON-RPC
// protocol stream. Every log line MUST go to stderr — never stdout — once
// this path is taken, unlike [runDemo]'s stdout logger above. A single
// stray line on stdout (a stray fmt.Println, a logger pointed at stdout,
// etc.) corrupts the message framing for a real connected client.
func runServer(
	b *mcp.Builder,
	toolHandle *mcp.ToolHandle[CalcInput, CalcOutput],
	resHandle *mcp.ResourceHandle[string, Item],
	promptHandle *mcp.PromptHandle,
) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	obs := stats.NewLoggingObserver(logger.With("component", "mcp"))
	opts := mcpgo.Options{Observer: obs}

	s := mcpgoserver.NewMCPServer(b.Info().Name, b.Info().Version)
	mcpgo.RegisterTool(s, toolHandle,
		withLogging("calculate", handleCalculate, logger.With("handler", "calculate")),
		opts,
	)
	mcpgo.RegisterResource(s, resHandle, handleItem, opts)
	mcpgo.RegisterPrompt(s, promptHandle, handleSummarize, opts)

	// ── Transport options ─────────────────────────────────────────────────
	//
	// Stdio (default here, used by local LLM clients such as Claude Desktop):
	//
	//   fmt.Fprintln(os.Stderr, "Starting MCP server on stdio…")
	//   if err := mcpgoserver.ServeStdio(s); err != nil { log.Fatal(err) }
	//
	// Streamable HTTP (MCP 2025-03-26+, recommended for remote servers):
	//
	//   httpServer := mcpgoserver.NewStreamableHTTPServer(s)
	//   fmt.Fprintln(os.Stderr, "Starting MCP server on :8080 (streamable HTTP)…")
	//   if err := httpServer.Start(":8080"); err != nil { log.Fatal(err) }
	//
	// SSE over HTTP (legacy MCP transport, supported by older clients):
	//
	//   sseServer := mcpgoserver.NewSSEServer(s,
	//       mcpgoserver.WithBaseURL("http://localhost:8080"),
	//   )
	//   fmt.Fprintln(os.Stderr, "Starting MCP server on :8080 (SSE)…")
	//   if err := sseServer.Start(":8080"); err != nil { log.Fatal(err) }

	fmt.Fprintln(os.Stderr, "Starting MCP server on stdio…")
	if err := mcpgoserver.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}

// callTool invokes a tool handler in-process and prints the result.
func callTool(ctx context.Context, logger *slog.Logger, handler mcpgoserver.ToolHandlerFunc, label string, args map[string]any) {
	fmt.Printf("\n  call: %s\n", label)
	result, err := handler(ctx, mcpmsg.CallToolRequest{
		Params: mcpmsg.CallToolParams{Arguments: args},
	})
	if err != nil {
		logger.ErrorContext(ctx, "protocol error", "error", err)
		return
	}
	if result.IsError {
		fmt.Printf("  → tool error (IsError=true): see slog output above\n")
		return
	}
	// Extract the text content from the result.
	if len(result.Content) > 0 {
		if tc, ok := result.Content[0].(mcpmsg.TextContent); ok {
			// Show only the structured output portion (truncated for readability).
			text := tc.Text
			if len(text) > 80 {
				text = text[:80] + "…"
			}
			fmt.Printf("  → success: %s\n", text)
		}
	}
}

// Ensure CountingObserver satisfies stats.Observer at compile time.
var _ stats.Observer = (*CountingObserver)(nil)
