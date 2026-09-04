package stats

import "context"

type observerKey struct{}

// WithObserver returns a copy of ctx carrying obs as the default observer.
// Adapters, stream bridges, and [ports.File] operations that receive this
// context will use obs automatically when no explicit Observer is set in their
// options struct — without passing Observer: obs on every call site.
//
// Use WithObserver at application startup to wire one observer everywhere:
//
//	obs := stats.NewFanout(metricsObserver, stats.NewLoggingObserver(slog.Default()))
//	ctx := stats.WithObserver(context.Background(), obs)
//
//	// All adapters use obs when Options.Observer is nil:
//	route.WithOptions(nethttp.Options{})
//	transport := mqtt.NewSubscribeTransport[Msg](client, 1, mqtt.SubscribeOptions{})
//	events.SubscribeHandle(ctx, sub, transport, fn)
//	stream.Apply(ctx, s, fn, stream.ApplyOptions{})
//
// The context-provided observer has **lower** priority than an explicitly set
// opts.Observer — explicit always wins:
//
//	route.WithOptions(nethttp.Options{Observer: auditObs}) // auditObs used, ctx obs ignored
//
// The observer is scoped to the context: a child context from
// [context.WithValue] inherits the parent's observer unless overridden. Use
// WithObserver again on a child context to swap the observer for a sub-tree.
func WithObserver(ctx context.Context, obs Observer) context.Context {
	return context.WithValue(ctx, observerKey{}, obs)
}

// ObserverFromContext retrieves the observer stored by [WithObserver].
// Returns [NoopObserver]{} when no observer has been stored in ctx — the same
// zero-cost default used when opts.Observer is nil and no context observer exists.
func ObserverFromContext(ctx context.Context) Observer {
	if obs, ok := ctx.Value(observerKey{}).(Observer); ok {
		return obs
	}
	return NoopObserver{}
}
