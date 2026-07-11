package stream

import (
	"context"

	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

// SourceOptions configures [FromCodec].
type SourceOptions struct {
	// Name identifies this source in [StreamDecodeError] for structured logging.
	// Defaults to "stream" when empty.
	Name string

	// Observer receives [stats.Observer.RecordValidationError] for per-field decode
	// failures. Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// Buffer is the Values and Errors channel buffer size. Default 0 (unbuffered).
	Buffer int
}

// Single wraps a single value as a [Stream] that emits v once, then terminates.
// The Stream.Errors channel is never written.
//
// Use Single to start a per-request pipeline inside a [PipelineHandlerFunc] or
// [AsPipelineFunc], or any time you need a bounded one-shot stream source:
//
//	s := stream.Single(ctx, req)
//	out := stream.Apply(ctx, s, computeFn, stream.ApplyOptions{Observer: obs})
//	out = stream.Tap(ctx, out, func(v Out) { slog.Info("computed", "v", v) })
func Single[T any](ctx context.Context, v T) Stream[T] {
	ch := make(chan T, 1)
	ch <- v
	close(ch)
	return From(ctx, ch)
}

// From wraps a typed channel as a [Stream].
// Each value received from src becomes a value item. When src is closed or ctx is
// cancelled, both Stream channels are closed.
//
// The returned Stream.Errors channel is never written — it closes when the stream
// terminates. This is intentional: From is a type-safe source with no error path.
// Use [FromCodec] when decode failures must be captured.
//
//	intCh := make(chan int, 3)
//	intCh <- 1; intCh <- 2; intCh <- 3; close(intCh)
//	s := stream.From(ctx, intCh)
//	stream.Drain(ctx, s, func(_ context.Context, v int) error {
//	    fmt.Println(v)
//	    return nil
//	}, nil, stream.DrainOptions{})
func From[T any](ctx context.Context, src <-chan T) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-src:
				if !ok {
					return
				}
				select {
				case values <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[T]{Values: values, Errors: errs}
}

// FromCodec decodes raw []byte payloads from src using the given format.
// Successful decodes go to Stream.Values. Decode or validation failures go to
// Stream.Errors as [StreamDecodeError].
//
// Pass any [format.Format] value — JSON, YAML, TOML, or a custom format:
//
//	sensors := stream.FromCodec(ctx, rawCh, format.JSON(sensorCodec),
//	    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})
//
//	sensors := stream.FromCodec(ctx, rawCh, format.YAML(sensorCodec),
//	    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})
//
// Use with MQTT or ZeroMQ SubscribeHandlers that write raw payloads to a channel:
//
//	rawCh := make(chan []byte, 64)
//	mqttClient.Subscribe("sensors/+/data", 1,
//	    adaptermqtt.SubscribeHandler(ctx, handle, func(_ context.Context, raw []byte) error {
//	        select { case rawCh <- raw: default: }
//	        return nil
//	    }, adaptermqtt.SubscribeOptions{}))
//
//	sensors := stream.FromCodec(ctx, rawCh, format.JSON(sensorCodec),
//	    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})
func FromCodec[T any](ctx context.Context, src <-chan []byte, fmt format.Format[T], opts SourceOptions) Stream[T] {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	name := opts.Name
	if name == "" {
		name = "stream"
	}
	values := make(chan T, opts.Buffer)
	errs := make(chan error, opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-src:
				if !ok {
					return
				}
				v, err := fmt.Unmarshal(raw)
				if err != nil {
					stats.ReportErrors(obs, "stream", err)
					sde := StreamDecodeError{Source: name, Err: err}
					select {
					case errs <- sde:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[T]{Values: values, Errors: errs}
}
