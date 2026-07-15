package ports

import (
	"context"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// bindWithObserver wraps a port's adapter-binding call with observer lifecycle
// events, matching the "Bind"/"Activate" events described in the ports design:
//   - obs.RecordRequest("port.bind", "<portName>/<adapterName>", 200|500, duration)
//     records once fn returns (for [SourcePort]/[SinkPort], fn wraps the adapter's
//     entire Activate lifetime — the goroutine that calls fn runs until Activate
//     returns; for [IOPort]/[ToolPort], fn wraps the synchronous Bind call).
//   - When obs implements [stats.TraceObserver], a "port.bind" span brackets fn.
//
// fn receives a context carrying the span (when tracing is active) and must use
// it when invoking the adapter so span propagation works.
func bindWithObserver(ctx context.Context, obs stats.Observer, portName, adapterName string, fn func(context.Context) error) error {
	start := time.Now()
	spanCtx := ctx
	var tracer stats.TraceObserver
	if to, ok := obs.(stats.TraceObserver); ok {
		tracer = to
		spanCtx = to.StartSpan(ctx, "port.bind", portName+"/"+adapterName)
	}

	err := fn(spanCtx)

	if tracer != nil {
		tracer.EndSpan(spanCtx, err)
	}
	status := 200
	if err != nil {
		status = 500
	}
	obs.RecordRequest("port.bind", portName+"/"+adapterName, status, time.Since(start))
	return err
}
