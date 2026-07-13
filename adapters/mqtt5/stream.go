package mqtt5

import (
	"context"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeStream ───────────────────────────────────────────────────────────

// Deprecated: Use [SubscribeAdapter] with [ports.SourcePort] instead.
//
// SubscribeStream creates a bridge from an MQTT 5 subscription to a typed stream.
// It subscribes to the broker and registers the handler with the router internally —
// no handler registration is needed by the caller:
//
//	s := mqtt5.SubscribeStream(ctx, client, router, sensorHandle, 1,
//	    format.JSON(sensorCodec),
//	    gstream.SourceOptions{Name: "mqtt5/sensors/+"},
//	    mqtt5.SubscribeOptions{TopicFilter: "sensors/+/data"})
//	oeeStream := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{})
//
// The subscription filter for [pahomqtt5.Subscribe] and [MQTTRouter.RegisterHandler]
// is [SubscribeOptions.TopicFilter] when set, otherwise [events.ChannelHandle.Topic].
//
// The full mqtt5 validation pipeline runs: ContentType negotiation, UserPropertyParams
// validation, security enforcement, observer calls, and TraceObserver spans. All
// validation failures are sent to [gstream.Stream.Errors] as [SubscribeError].
//
// The stream terminates when ctx is cancelled.
func SubscribeStream[T any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *events.ChannelHandle[T],
	qos byte,
	fmt format.Format[T],
	srcOpts gstream.SourceOptions,
	subOpts SubscribeOptions,
) gstream.Stream[T] {
	typedCh := make(chan T, srcOpts.Buffer)
	errCh := make(chan error, srcOpts.Buffer)

	obs := subOpts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	// fmt is always used as the sole format for content-type matching and decoding.
	// handle.SubscribeFormats and handle.Formats are not consulted — unlike
	// Subscribe's variadic formats, SubscribeStream requires a single explicit
	// format and does not fall back to handle-level format lists.
	effectiveFmts := []format.Format[T]{fmt}

	// Override OnError: route adapter errors (decode, security, user properties)
	// to Stream.Errors so callers can handle them with stream operators.
	innerOpts := subOpts
	innerOpts.OnError = func(e SubscribeError) {
		select {
		case errCh <- e:
		case <-ctx.Done():
		default: // drop on full buffer
		}
	}

	// makeSubscribeMessageHandler applies ContentType negotiation,
	// UserPropertyParams validation, security, observer calls — identical to Subscribe.
	handler := makeSubscribeMessageHandler(ctx, handle, effectiveFmts,
		func(_ context.Context, v T) error {
			select {
			case typedCh <- v:
			case <-ctx.Done():
			default: // drop on full buffer
			}
			return nil
		}, obs, innerOpts)

	// Register handler and subscribe internally.
	filter := subOpts.TopicFilter
	if filter == "" {
		filter = handle.Topic
	}
	router.RegisterHandler(filter, handler)
	if _, err := client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: filter, QoS: qos}},
	}); err != nil {
		router.UnregisterHandler(filter)
		be := BrokerError{Op: "subscribe", Err: err}
		go func() {
			select {
			case errCh <- be:
			case <-ctx.Done():
			}
			close(typedCh)
			close(errCh)
		}()
		return gstream.Stream[T]{Values: typedCh, Errors: errCh}
	}

	go func() {
		<-ctx.Done()
		close(typedCh)
		close(errCh)
	}()

	return gstream.Stream[T]{Values: typedCh, Errors: errCh}
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

// MQTT5DrainPublishOptions configures [DrainPublish].
type MQTT5DrainPublishOptions struct {
	// QoS is the MQTT quality of service level (0, 1, or 2). Default 0.
	QoS byte
	// Retained, when true, publishes each item as a retained message.
	Retained bool
	// Vars, when non-nil, substitutes {varName} placeholders in the topic template.
	// The same map is used for every item in the stream (static topic vars only).
	// For per-item topic var substitution, use [gstream.Drain] with [Publish] directly.
	Vars map[string]string
	// OnError, when non-nil, is called for encode failures or upstream stream errors.
	OnError func(error)
	// Observer receives per-publish lifecycle events.
	Observer stats.Observer
}

// Deprecated: Use [PublishAdapter] with [ports.SinkPort] instead.
//
// DrainPublish publishes each value item from src to the MQTT 5 broker using handle.
// Encode failures are delivered to opts.OnError as [PublishEncodeError].
// Upstream stream errors are forwarded to opts.OnError unchanged.
// Blocks until src terminates or ctx is cancelled.
func DrainPublish[T any](
	ctx context.Context,
	client MQTTClient,
	handle *events.ChannelHandle[T],
	src gstream.Stream[T],
	fmt format.Format[T],
	opts MQTT5DrainPublishOptions,
) {
	onErr := opts.OnError
	pubOpts := PublishOptions{Observer: opts.Observer}

	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			if err := Publish(ctx, client, handle, opts.QoS, opts.Retained, v, opts.Vars, pubOpts, fmt); err != nil {
				if onErr != nil {
					onErr(err)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{Observer: opts.Observer},
	)
}

// ── AsPipelineFunc ────────────────────────────────────────────────────────────

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

// ── CallStream ────────────────────────────────────────────────────────────────

// Deprecated: Use [CallAdapter] with [ports.IOPort] instead.
//
// CallStream sends each request item from src to handle using [Call], emitting
// each decoded response to the returned [gstream.Stream]. Protocol errors or
// decode failures are sent to [gstream.Stream.Errors] as [CallError].
// Requests are issued sequentially. The stream terminates when src closes or
// ctx is cancelled.
func CallStream[Req, Resp any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	src gstream.Stream[Req],
	opts CallOptions,
) gstream.Stream[Resp] {
	values := make(chan Resp)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case req, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				resp, err := Call(ctx, client, router, handle, req, opts)
				if err != nil {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[Resp]{Values: values, Errors: errs}
}
