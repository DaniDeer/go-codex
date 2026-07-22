package ports

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ChainEdge records one [Chain]/[ChainStream] connection FROM a [PipePort]
// — the destination pipe's name and the REAL identity of the transform
// function, captured via reflection at the [Chain]/[ChainStream] call site
// rather than hand-typed. Used by [PipelineSpec].
type ChainEdge struct {
	// Kind is "chain" (single value-mapping function) or "chainStream"
	// (arbitrary stream transform).
	Kind string
	// To is the destination pipe's [PipePort.Name].
	To string
	// Func is the transform's Go function identity, e.g. "main.validateReading"
	// (from [Chain]) or a closure name like "main.BuildPipeline.func1" (from
	// [ChainStream] with an inline transform) — real either way, never
	// fabricated. Empty if reflection could not resolve a function value
	// (should not happen for any func passed to Chain/ChainStream).
	Func string
}

// funcName returns fn's Go function identity via reflection — the same
// name that would appear in a stack trace or pprof profile. Returns "" if
// fn is not a function value.
func funcName(fn any) string {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return ""
	}
	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return ""
	}
	return f.Name()
}

// recordEdge appends e to the pipe's outgoing edge list. Called by [Chain]
// and [ChainStream] on their from pipe.
func (p *PipePort[T]) recordEdge(e ChainEdge) {
	p.mu.Lock()
	p.edges = append(p.edges, e)
	p.mu.Unlock()
}

// recordChainEdgeWithObserver is the shared setup-time observability path
// for [Chain] and [ChainStream]: it resolves an observer (from's own, or
// from ctx when unset — Chain/ChainStream previously resolved none at all),
// brackets the edge recording in a "pipe.chain" [stats.TraceObserver] span
// when the observer supports it (edge SETUP only, not a per-item span — a
// Chain/ChainStream edge may carry unbounded traffic, so tracing every item
// would be an unbounded tracing cost; this matches [bindWithObserver]'s
// "port.bind" cost/benefit precedent), then calls [PipePort.recordEdge].
func recordChainEdgeWithObserver[In, Out any](ctx context.Context, from *PipePort[In], to *PipePort[Out], edge ChainEdge) {
	obs := from.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	spanCtx := ctx
	var tracer stats.TraceObserver
	if t, ok := obs.(stats.TraceObserver); ok {
		tracer = t
		spanCtx = t.StartSpan(ctx, "pipe.chain", from.Name()+"->"+to.Name())
	}
	from.recordEdge(edge)
	if tracer != nil {
		tracer.EndSpan(spanCtx, nil)
	}
}

// Buffer returns the pipe's configured channel buffer size.
func (p *PipePort[T]) Buffer() int { return p.buffer }

// InputAdapters returns, for every named [InputPort] registered so far, the
// real [SourceAdapter.AdapterName] of each adapter bound to it — read from
// the underlying [SourcePort.BoundAdapters], not hand-typed. Used by
// [PipelineSpec].
func (p *PipePort[T]) InputAdapters() map[string][]string {
	p.mu.Lock()
	in := make(map[string]*SourcePort[T], len(p.in))
	for name, sp := range p.in {
		in[name] = sp
	}
	p.mu.Unlock()

	out := make(map[string][]string, len(in))
	for name, sp := range in {
		out[name] = sp.BoundAdapters()
	}
	return out
}

// OutputAdapters returns, for every named [OutputPort] registered so far,
// the real [SinkAdapter.AdapterName] of each adapter bound to it — read
// from the underlying [SinkPort.BoundAdapters], not hand-typed. Used by
// [PipelineSpec].
func (p *PipePort[T]) OutputAdapters() map[string][]string {
	p.mu.Lock()
	out2 := make(map[string]*SinkPort[T], len(p.out))
	for name, sp := range p.out {
		out2[name] = sp
	}
	p.mu.Unlock()

	out := make(map[string][]string, len(out2))
	for name, sp := range out2 {
		out[name] = sp.BoundAdapters()
	}
	return out
}

// OutEdges returns the pipe's recorded [ChainEdge] list — every [Chain] or
// [ChainStream] call made with this pipe as from, in call order.
func (p *PipePort[T]) OutEdges() []ChainEdge {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ChainEdge, len(p.edges))
	copy(out, p.edges)
	return out
}

// PipeSpecSource is implemented by [PipePort] for ANY payload type — it
// exposes a non-generic view of a pipe's actual structure for
// documentation/spec tooling. A real pipeline chains different payload
// types together (e.g. PipePort[Raw] -> PipePort[Validated] ->
// PipePort[Calibrated]), so a common non-generic interface is what lets
// heterogeneous PipePorts be described together in one [PipelineSpec] call.
type PipeSpecSource interface {
	// Name returns the pipe's declared name.
	Name() string
	// Buffer returns the pipe's configured channel buffer size.
	Buffer() int
	// InputAdapters returns input-port-name -> bound SourceAdapter names.
	InputAdapters() map[string][]string
	// OutputAdapters returns output-port-name -> bound SinkAdapter names.
	OutputAdapters() map[string][]string
	// OutEdges returns the Chain/ChainStream connections made FROM this pipe.
	OutEdges() []ChainEdge
}

// PipelineSpec builds a [gstream.TopologySpec] FROM the actual wiring of
// pipes — pipe names, buffer sizes, bound adapter identities, and
// Chain/ChainStream edges are all read from the pipes themselves via
// [PipeSpecSource], not hand-typed. Call after all [InputPort]/[OutputPort]
// Bind calls and [Chain]/[ChainStream] calls for every pipe passed in.
//
// title and version are pipeline-level metadata with no corresponding pipe
// field to read them from — they, and pipes' ordering, are the only
// caller-supplied inputs; everything else is derived:
//
//	spec := ports.PipelineSpec("Sensor Pipeline", "1.0.0", Raw, Valid, Calibrated)
//	yamlBytes, err := streamrender.Render(spec)
//
// Emits one [gstream.StepKindPort] step per pipe (Description built from
// Buffer/InputAdapters/OutputAdapters) followed by one
// [gstream.StepKindApply] step per outgoing edge (Name = the edge's real
// function identity, Description = "<kind>: <from> -> <to>") — in pipes'
// given order. Pipes with no recorded structure still produce a step (an
// empty Buffer/adapter set is real information, not an error).
func PipelineSpec(title, version string, pipes ...PipeSpecSource) gstream.TopologySpec {
	steps := make([]gstream.TopologyStep, 0, len(pipes)*2)
	for _, p := range pipes {
		steps = append(steps, gstream.TopologyStep{
			Kind:        gstream.StepKindPort,
			Name:        p.Name(),
			Description: describePipe(p),
		})
		for _, e := range p.OutEdges() {
			steps = append(steps, gstream.TopologyStep{
				Kind:        gstream.StepKindApply,
				Name:        e.Func,
				Description: fmt.Sprintf("%s: %s → %s", e.Kind, p.Name(), e.To),
			})
		}
	}
	return gstream.TopologySpec{
		Info:  gstream.TopologyInfo{Title: title, Version: version},
		Steps: steps,
	}
}

// describePipe builds a human-readable, fully-derived description of p's
// current structure — no hand-typed content.
func describePipe(p PipeSpecSource) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Buffer=%d", p.Buffer())
	if ins := p.InputAdapters(); len(ins) > 0 {
		sb.WriteString("; inputs: ")
		sb.WriteString(formatAdapterMap(ins))
	}
	if outs := p.OutputAdapters(); len(outs) > 0 {
		sb.WriteString("; outputs: ")
		sb.WriteString(formatAdapterMap(outs))
	}
	return sb.String()
}

// formatAdapterMap renders a port-name -> adapter-names map deterministically
// (sorted by port name) for stable, diffable spec output.
func formatAdapterMap(m map[string][]string) string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for i, name := range names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(name)
		sb.WriteString("=")
		sb.WriteString(strings.Join(m[name], "+"))
	}
	return sb.String()
}
