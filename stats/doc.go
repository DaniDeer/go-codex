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
//	type MyObserver struct{}
//	func (o *MyObserver) RecordValidationError(location, constraint, field string) {
//	    // increment Prometheus counter, emit log, etc.
//	}
//
//	val, err := appConfigCodec.Decode(rawData)
//	stats.ReportErrors(&MyObserver{}, "config", err)
//
// # Adapter-level (Observer)
//
// Use the full [Observer] interface when wiring to an adapter. It embeds
// [ValidationObserver] and adds transport-specific hooks for HTTP and MQTT:
//
//	nethttp.Register(mux, route, handler, nethttp.Options{Observer: obs})
//
//	adaptermqtt.SubscribeHandler(ctx, ch, fn, adaptermqtt.SubscribeOptions{Observer: obs})
//	adaptermqtt.Publish(ctx, client, ch, qos, retained, msg, vars,
//	    adaptermqtt.PublishOptions{Observer: obs})
//
// [NoopObserver] is a zero-cost default used when no observer is configured.
package stats
