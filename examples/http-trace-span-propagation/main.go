// Package http-trace-span-propagation demonstrates trace span propagation across
// HTTP, forge, and file I/O layers using the observer pattern.
//
// Scenario:
//  1. HTTP POST with JSON body {"name":"Alice"} to a greeting service.
//  2. Server decodes JSON, calls a forge computation function.
//  3. Forge writes the result to a temp file for persistence.
//  4. Server returns the JSON greeting.
//
// A single stats.NewFanout value wires a demo tracer, metrics, and structured
// logging. The tracer records parent→child span names to prove context propagation
// works through every layer.
//
// Run with: go run ./examples/http-trace-span-propagation
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain model ──────────────────────────────────────────────────────────────

type GreetIn struct {
	Name string `json:"name"`
}

type GreetOut struct {
	Greeting string `json:"greeting"`
}

var greetInCodec = codex.Struct[GreetIn](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(g GreetIn) string { return g.Name },
		func(g *GreetIn, v string) { g.Name = v },
	),
)

var greetOutCodec = codex.Struct[GreetOut](
	codex.RequiredField("greeting", codex.String(),
		func(g GreetOut) string { return g.Greeting },
		func(g *GreetOut, v string) { g.Greeting = v },
	),
)

// ── Metrics observer ─────────────────────────────────────────────────────────

type MetricsObs struct {
	stats.NoopObserver
	mu         sync.Mutex
	requests   int
	applies    int
	fileWrites int
}

func (m *MetricsObs) RecordRequest(_, _ string, _ int, _ time.Duration) {
	m.mu.Lock()
	m.requests++
	m.mu.Unlock()
}

func (m *MetricsObs) RecordApply(_, _ string, _ bool, _ time.Duration) {
	m.mu.Lock()
	m.applies++
	m.mu.Unlock()
}

func (m *MetricsObs) RecordFileWrite(_ string, _ bool, _ time.Duration) {
	m.mu.Lock()
	m.fileWrites++
	m.mu.Unlock()
}

func (m *MetricsObs) Print() {
	fmt.Printf("  requests=%d  applies=%d  fileWrites=%d\n", m.requests, m.applies, m.fileWrites)
}

// ── Demo tracer ──────────────────────────────────────────────────────────────

type spanNode struct {
	op   string
	name string
	kids []*spanNode
}

type tracerKey struct{}

type DemoTracer struct {
	stats.NoopObserver
	mu      sync.Mutex
	roots   []*spanNode
	parents map[context.Context]*spanNode
}

func (d *DemoTracer) StartSpan(ctx context.Context, op, name string) context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := &spanNode{op: op, name: name}
	if p := d.parents[ctx]; p != nil {
		p.kids = append(p.kids, n)
	} else {
		d.roots = append(d.roots, n)
	}
	child := context.WithValue(ctx, tracerKey{}, n)
	d.parents[child] = n
	return child
}

func (d *DemoTracer) EndSpan(_ context.Context, _ error) {}

func (d *DemoTracer) Print() {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Println("=== Trace hierarchy ===")
	var walk func(n *spanNode, indent int)
	walk = func(n *spanNode, indent int) {
		for i := 0; i < indent; i++ {
			fmt.Print("  ")
		}
		if n.op == n.name {
			fmt.Println(n.op)
		} else {
			fmt.Printf("%s (%s)\n", n.op, n.name)
		}
		for _, k := range n.kids {
			walk(k, indent+1)
		}
	}
	for _, r := range d.roots {
		walk(r, 0)
	}
}

var greetPath = "/greet"

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	metrics := &MetricsObs{}
	tracer := &DemoTracer{parents: make(map[context.Context]*spanNode)}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger), tracer)

	// ── Forge function ───────────────────────────────────────────────────

	greetFn := forge.NewFunction("greet", "1.0.0",
		codex.String(), codex.String(),
		func(name string) (string, error) {
			return "Hello, " + name + "!", nil
		},
	)

	// Register with registry to wire the observer for RecordApply + TraceObserver.
	reg := forge.NewRegistry("GreetPipeline", "1.0.0").WithObserver(obs)
	greetFn.Register(reg)

	// ── HTTP route ───────────────────────────────────────────────────────

	route := rest.NewRoute[GreetIn, GreetOut](
		"POST", greetPath, greetInCodec, greetOutCodec,
		rest.RouteMeta{OperationID: "greet", Summary: "Returns a greeting"},
	)

	// Server handle (registered with builder — validates path codecs).
	b := rest.NewBuilder(rest.Info{Title: "Trace Demo", Version: "1.0.0"})
	regHandle, err := route.Register(b)
	if err != nil {
		panic(err)
	}

	// Client handle (no builder — client-only use).
	clientHandle := route.ClientHandle()

	// ── Temp dir for file output ──────────────────────────────────────────

	outputDir, err := os.MkdirTemp("", "trace-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(outputDir)

	// ── Start server ──────────────────────────────────────────────────────

	mux := http.NewServeMux()
	nethttp.Register(mux, regHandle, func(ctx context.Context, in GreetIn) (GreetOut, error) {
		// 1. Forge — child of HTTP span.
		result, err := greetFn.ApplyContext(ctx, in.Name)
		if err != nil {
			return GreetOut{}, err
		}

		// 2. File — child of HTTP span.
		filePath := filepath.Join(outputDir, in.Name+".txt")
		f := format.NewFile(filePath, format.JSON(codex.String()))
		if err := f.Write(nil, result, format.FileOptions{Observer: obs, Context: ctx}); err != nil {
			return GreetOut{}, err
		}

		return GreetOut{Greeting: result}, nil
	}, nethttp.Options{Observer: obs})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── Client call — creates root span ──────────────────────────────────

	clientCtx := tracer.StartSpan(context.Background(), "client:http.request", greetPath)
	defer tracer.EndSpan(clientCtx, nil)

	resp, err := nethttp.Call(clientCtx, srv.Client(), srv.URL,
		clientHandle, GreetIn{Name: "Alice"}, nil,
		nethttp.CallOptions{Observer: obs},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Call failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Response: %+v\n\n", resp)
	tracer.Print()
	fmt.Println()
	fmt.Println("=== Metrics ===")
	metrics.Print()
}
