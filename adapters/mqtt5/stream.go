package mqtt5

import (
	"context"

	gstream "github.com/DaniDeer/go-codex/stream"
)

// AsPipelineFunc converts a pipeline handler function into the plain handler
// function signature accepted by [Serve].
//
// Internally: calls fn(ctx, req) to build the pipeline, then collects the result
// via [gstream.Collect]. Errors take precedence over values. If the pipeline emits
// no value, [PipelineNoResponseError] is returned.
//
// Use AsPipelineFunc when the [Serve] handler body benefits from [gstream.Tap]
// for declarative intermediate observation, [gstream.Apply] for multi-step forge
// function composition, or [gstream.MapErr] for per-step typed error recovery:
//
//	mqtt5.Serve(ctx, client, router, oeeHandle,
//	    mqtt5.AsPipelineFunc(func(ctx context.Context, req SensorReq) gstream.Stream[OEEResult] {
//	        s  := gstream.Single(ctx, req)
//	        s   = gstream.Apply(ctx, s, validateFn, gstream.ApplyOptions{Observer: obs})
//	        s   = gstream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("request", "id", v.ID) })
//	        out := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{Observer: obs})
//	        return gstream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
//	    }),
//	    mqtt5.ServeOptions{Observer: obs})
//
// For simple single-step handlers, use a plain fn directly with [Serve].
func AsPipelineFunc[Req, Resp any](
	fn func(context.Context, Req) gstream.Stream[Resp],
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		pipeline := fn(ctx, req)
		vals, errs := gstream.Collect(ctx, pipeline)
		var zero Resp
		if len(errs) > 0 {
			return zero, errs[0]
		}
		if len(vals) == 0 {
			// Topic is left empty because AsPipelineFunc wraps the fn, not the handle;
			// the actual MQTT topic is not available at this level.
			return zero, PipelineNoResponseError{Topic: ""}
		}
		return vals[0], nil
	}
}
