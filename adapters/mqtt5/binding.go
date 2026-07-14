package mqtt5

import (
	"context"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

// SubscribeAdapterOptions configures [SubscribeAdapter].
type SubscribeAdapterOptions struct {
	// TopicFilter is the MQTT 5 broker subscription filter (e.g. "sensors/+/data").
	// When empty, [events.ChannelHandle.Topic] is used.
	TopicFilter string
	// UserPropertyParams validates MQTT 5 User Properties on each message.
	UserPropertyParams []UserPropertyParam
	// SecurityFunc enforces security requirements on each incoming message.
	SecurityFunc func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) error
	// Observer receives per-message lifecycle events. Resolved from ctx when nil.
	Observer stats.Observer
}

// SubscribeAdapter returns a [ports.SourceAdapter] backed by the MQTT 5 subscription
// machinery. Use with [ports.SourcePort.Bind] to connect an MQTT 5 subscription to
// a protocol-agnostic pipeline:
//
//	domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(
//	    client, router, sensorHandle, 0,
//	    format.JSON(ReadingCodec),
//	    mqtt5.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"},
//	))
//
// The full MQTT 5 validation pipeline runs on every message: ContentType
// negotiation, UserPropertyParams, security enforcement, observer calls.
// Errors are routed to [ports.SourcePort.Stream]'s Errors channel.
func SubscribeAdapter[T any](
	client MQTTClient,
	router MQTTRouter,
	handle *events.ChannelHandle[T],
	qos byte,
	fmt format.Format[T],
	opts SubscribeAdapterOptions,
) ports.SourceAdapter[T] {
	return &mqtt5SubscribeAdapter[T]{
		client: client,
		router: router,
		handle: handle,
		qos:    qos,
		fmt:    fmt,
		opts:   opts,
	}
}

type mqtt5SubscribeAdapter[T any] struct {
	client MQTTClient
	router MQTTRouter
	handle *events.ChannelHandle[T]
	qos    byte
	fmt    format.Format[T]
	opts   SubscribeAdapterOptions
}

func (a *mqtt5SubscribeAdapter[T]) AdapterName() string { return "mqtt5.SubscribeAdapter" }

func (a *mqtt5SubscribeAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	innerOpts := SubscribeOptions{
		Observer:           obs,
		SecurityFunc:       a.opts.SecurityFunc,
		UserPropertyParams: a.opts.UserPropertyParams,
		TopicFilter:        a.opts.TopicFilter,
		OnError: func(e SubscribeError) {
			select {
			case errs <- e:
			case <-ctx.Done():
			default:
			}
		},
	}
	handler := makeSubscribeMessageHandler(ctx, a.handle, []format.Format[T]{a.fmt},
		func(_ context.Context, v T) error {
			select {
			case dst <- v:
			case <-ctx.Done():
			default:
			}
			return nil
		}, obs, innerOpts)

	filter := a.opts.TopicFilter
	if filter == "" {
		filter = a.handle.Topic
	}
	a.router.RegisterHandler(filter, handler)

	if _, err := a.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: filter, QoS: a.qos}},
	}); err != nil {
		a.router.UnregisterHandler(filter)
		select {
		case errs <- BrokerError{Op: "subscribe", Err: err}:
		case <-ctx.Done():
		}
		return
	}
	<-ctx.Done()
}

// ── PublishAdapter ────────────────────────────────────────────────────────────

// MQTT5DrainPublishOptions configures [PublishAdapter] publish behaviour.
type MQTT5DrainPublishOptions struct {
	// QoS is the MQTT quality of service level (0, 1, or 2). Default 0.
	QoS byte
	// Retained, when true, publishes each item as a retained message.
	Retained bool
	// Vars, when non-nil, substitutes {varName} placeholders in the topic template.
	// The same map is used for every item (static topic vars only).
	// For per-item substitution, call [Publish] directly inside [gstream.Drain].
	Vars map[string]string
	// OnError, when non-nil, is called for encode failures or upstream stream errors.
	OnError func(error)
	// Observer receives per-publish lifecycle events.
	Observer stats.Observer
}

// PublishAdapter returns a [ports.SinkAdapter] that publishes each item via MQTT 5.
// Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, format.JSON(OEECodec),
//	    mqtt5.PublishAdapterOptions{VarsFor: func(oee OEE) map[string]string { return map[string]string{"machineID": oee.MachineID} }}))
func PublishAdapter[T any](
	client MQTTClient,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts MQTT5DrainPublishOptions,
) ports.SinkAdapter[T] {
	return &mqtt5PublishAdapter[T]{client: client, handle: handle, fmt: fmt, opts: opts}
}

type mqtt5PublishAdapter[T any] struct {
	client MQTTClient
	handle *events.ChannelHandle[T]
	fmt    format.Format[T]
	opts   MQTT5DrainPublishOptions
}

func (a *mqtt5PublishAdapter[T]) AdapterName() string { return "mqtt5.PublishAdapter" }

func (a *mqtt5PublishAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	onErr := a.opts.OnError
	pubOpts := PublishOptions{Observer: a.opts.Observer}
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			if err := Publish(ctx, a.client, a.handle, a.opts.QoS, a.opts.Retained, v, a.opts.Vars, pubOpts, a.fmt); err != nil {
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
		gstream.DrainOptions{Observer: a.opts.Observer},
	)
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

// CallAdapter returns a [ports.IOAdapter] that performs MQTT 5 request-reply
// for each upstream item. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, mqtt5.CallAdapter(client, router, calibHandle, callOpts))
func CallAdapter[Req, Resp any](
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	opts CallOptions,
) ports.IOAdapter[Req, Resp] {
	return &mqtt5CallAdapter[Req, Resp]{client: client, router: router, handle: handle, opts: opts}
}

type mqtt5CallAdapter[Req, Resp any] struct {
	client MQTTClient
	router MQTTRouter
	handle *reqreply.RouteHandle[Req, Resp]
	opts   CallOptions
}

func (a *mqtt5CallAdapter[Req, Resp]) AdapterName() string { return "mqtt5.CallAdapter" }

func (a *mqtt5CallAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
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
				resp, err := Call(ctx, a.client, a.router, a.handle, req, a.opts)
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

// ── ServeAdapter ──────────────────────────────────────────────────────────────

// ServeAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an MQTT 5 request/reply server via [Serve]. When
// [ports.ToolPort.Bind] is called, the pipeline function is wrapped as an
// [AsPipelineFunc] handler and [Serve] is started in a background goroutine.
// Use with [ports.ToolPort.Bind]:
//
//	domain.OEEToolPort.Bind(ctx, mqtt5.ServeAdapter(client, router, handle, opts))
func ServeAdapter[Req, Resp any](
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	opts ServeOptions,
) ports.ToolAdapter[Req, Resp] {
	return &mqtt5ServeAdapter[Req, Resp]{client: client, router: router, handle: handle, opts: opts}
}

type mqtt5ServeAdapter[Req, Resp any] struct {
	client MQTTClient
	router MQTTRouter
	handle *reqreply.RouteHandle[Req, Resp]
	opts   ServeOptions
}

func (a *mqtt5ServeAdapter[Req, Resp]) AdapterName() string { return "mqtt5.ServeAdapter" }

func (a *mqtt5ServeAdapter[Req, Resp]) Bind(
	ctx context.Context,
	fn func(context.Context, Req) gstream.Stream[Resp],
) error {
	go Serve(ctx, a.client, a.router, a.handle, AsPipelineFunc(fn), a.opts) //nolint:errcheck
	return nil
}
