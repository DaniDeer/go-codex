// Package stats defines the Observer interface for codec and adapter lifecycle events.
//
// go-codex exposes two levels of observability:
//
// # Codec-level (ValidationObserver)
//
// Use [ValidationObserver] when you call codecs directly without an adapter —
// for example, validating config files, parsing binary protocols, or any
// non-HTTP/MQTT use case. Implement just [ValidationObserver.RecordValidationError]
// and call [ReportErrors] after each [codex.Codec.Decode]:
//
//	val, err := appConfigCodec.Decode(rawData)
//	stats.ReportErrors(obs, "config", err)
//
// # Adapter-level (Observer)
//
// Use the full [Observer] interface when wiring to an adapter. It embeds
// [ValidationObserver] and adds transport-specific hooks for HTTP and MQTT:
//
//	route.WithOptions(nethttp.Options{Observer: obs})
//	route.Register(b); nethttp.Serve(mux, b)
//	adaptermqtt.SubscribeHandler(ctx, ch, fn, adaptermqtt.SubscribeOptions{Observer: obs})
//
// # Composing metrics and logging
//
// Use [NewLoggingObserver] and [NewFanout] to separate the metrics concern
// from the logging concern — no mixing of slog and counters in one struct:
//
//	metrics := &MyMetricsObserver{}  // pure counters — swap for Prometheus
//
//	obs := stats.NewFanout(
//	    metrics,
//	    stats.NewLoggingObserver(slog.Default().With("component", "api")),
//	)
//
//	route.WithOptions(nethttp.Options{Observer: obs})
//	route.Register(b); nethttp.Serve(mux, b)
//
// [LoggingObserver] implements all five observer interfaces and logs every event
// via slog. Configure the logger's handler for your environment:
//   - [slog.NewTextHandler] for development
//   - [slog.NewJSONHandler] for log aggregation
//   - An OpenTelemetry slog bridge for distributed traces
//
// [NewFanout] fans out to all provided observers and also implements the optional
// [FileObserver], [SecurityObserver], and [PipelineObserver] interfaces —
// delegating each to the inner observers that satisfy those interfaces.
//
// [NoopObserver] satisfies all five interfaces at zero cost; it is the default
// when no observer is configured.
package stats
