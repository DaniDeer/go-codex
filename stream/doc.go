// Package stream provides a declarative reactive pipeline for go-codex, bridging
// push-based transport adapters (MQTT, ZeroMQ) with governed [forge.Function]
// computations over typed Go channels.
//
// # Reactive programming paradigm
//
// Every operator in this package follows the same model:
//
//   - Source: [From], [FromCodec] (accepts any [format.Format])
//   - Transform: [Apply] (forge.Function per-item), [Filter], [Tap], [MapErr], [Retry], [FlatMapSlice]
//   - Fan-in/out: [Merge], [Tee], [CombineLatest2], [CombineLatest3], [CombineLatest4], [Zip]
//   - Time: [Buffer] (count/timeout), [Window] (fixed ticker), [SlidingWindow], [Debounce], [Throttle]
//   - Sink: [Drain] (safe — drains both channels), [Collect]
//
// Pipelines are composed by passing [Stream] values between free functions:
//
//	sensors  := stream.FromCodec(ctx, rawCh, sensorCodec, stream.SourceOptions{Observer: obs})
//	oeeData  := stream.Apply(ctx, sensors, oeeCalcFn, stream.ApplyOptions{Observer: obs})
//	oeeData   = stream.Tap(ctx, oeeData, func(oee OEE) { dashboard.Publish(oee) })
//	alerts   := stream.Filter(ctx, oeeData, func(o OEE) bool { return float64(o) < 0.65 })
//	stream.Drain(ctx, alerts, publishAlert, logError, stream.DrainOptions{Observer: obs})
//
// # Explicit error channels
//
// Every [Stream] has two channels: Values for successful items and Errors for
// per-item errors. The stream continues after each error — a single bad sensor
// reading does not terminate a continuous monitoring pipeline.
//
// Consumers MUST drain both channels concurrently to avoid goroutine leaks.
// [Drain] is the safe default sink: it handles both channels in a single select loop.
//
// Use [MapErr] to recover from errors or reclassify them before the final sink.
//
// # Forge integration
//
// [Apply] bridges [forge.Function] (governed synchronous computation) into a
// streaming pipeline. Every item is validated by the function's input codec,
// processed, and validated by the output codec — the same governance that applies
// to batch computations also applies per-item in the stream.
//
// Forge functions can be composed before being used in a stream:
//
//	composed := forge.Compose("c2k", "1.0.0", celsius2centi, centi2kelvin)
//	kelvinStream := stream.Apply(ctx, celsiusStream, composed, opts)
//
// # Two observer kinds
//
// Infrastructure metrics — how many items, latency, error counts:
//
//	opts := stream.ApplyOptions{Observer: obs} // obs implements stats.StreamObserver
//
// Domain event observation — typed business values flowing through the pipeline:
//
//	oeeStream = stream.Tap(ctx, oeeStream, func(oee OEE) {
//	    slog.Info("OEE computed", "value", float64(oee))
//	})
//
// Both are orthogonal and composable. A pipeline can use both simultaneously.
//
// # Observer interfaces
//
// [stats.StreamObserver.RecordStreamItem] fires inside [Apply] for every item.
// [stats.PipelineObserver.RecordApply] fires separately inside forge for every item.
// Both fire independently — compose them via [stats.NewFanout].
//
// # No external dependencies
//
// This package imports only codex, forge, format, stats, context, time, and sync —
// all already in the module. No RxGo or any other reactive library is needed.
package stream
