package events

import (
	"context"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
)

// SpecTopicRequiredError is returned by [Client.ServeSpec] when topic is
// empty — ServeSpec has no default topic, mirroring every other
// Register/Handle call's "explicit, required" convention.
type SpecTopicRequiredError struct{}

func (e SpecTopicRequiredError) Error() string {
	return "api/events: ServeSpec requires a non-empty topic"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e SpecTopicRequiredError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("op", "ServeSpec"))
}

// SpecFormat selects the serialization [Client.ServeSpec] publishes.
type SpecFormat int

const (
	// SpecFormatYAML publishes the spec document as YAML (the default).
	SpecFormatYAML SpecFormat = iota
	// SpecFormatJSON publishes the spec document as JSON.
	SpecFormatJSON
)

// SpecOpt configures [Client.ServeSpec].
type SpecOpt interface{ applySpecOpt(*specOptions) }

type specOptions struct {
	format      SpecFormat
	middlewares []specMiddleware
}

type specMiddleware struct {
	mw middleware.RouteMiddleware
	fn any
}

type specFormatOpt SpecFormat

func (o specFormatOpt) applySpecOpt(so *specOptions) { so.format = SpecFormat(o) }

// WithSpecFormat selects which single format THIS [Client.ServeSpec] call
// publishes — pub/sub has no per-call negotiation the way an HTTP Accept
// header does, so publishing BOTH formats means calling ServeSpec TWICE,
// with two different topics (e.g. "spec/yaml" and "spec/json"). Default,
// when this opt is not passed, is [SpecFormatYAML].
func WithSpecFormat(f SpecFormat) SpecOpt { return specFormatOpt(f) }

type specMiddlewareOpt specMiddleware

func (o specMiddlewareOpt) applySpecOpt(so *specOptions) {
	so.middlewares = append(so.middlewares, specMiddleware(o))
}

// WithSpecMiddleware attaches a general-purpose (unpaired) middleware
// implementation to the internal publish [Client.ServeSpec] performs —
// identical nilable-mw semantics as [Publisher.PublishMW].
func WithSpecMiddleware(mw middleware.RouteMiddleware, fn any) SpecOpt {
	return specMiddlewareOpt{mw: mw, fn: fn}
}

// ServeSpec publishes c's CURRENT [Client.AsyncAPISpec] ONCE, immediately,
// to topic — unlike [rest.Server.ServeSpec]/[reqreply.Server.ServeSpec],
// there is no lazy caching here: pub/sub has no later "next request"
// trigger the way REST's next request or reqreply's next Call provides
// one, so ServeSpec computes and publishes the CURRENT document the
// moment it's called.
//
// Since pub/sub has no format-negotiation mechanism at publish time
// either, ServeSpec publishes exactly ONE format per call — YAML by
// default, or JSON via [WithSpecFormat]. A caller wanting BOTH formats
// published calls ServeSpec TWICE, with two different topics:
//
//	c.ServeSpec(ctx, "spec/yaml")
//	c.ServeSpec(ctx, "spec/json", events.WithSpecFormat(events.SpecFormatJSON))
//
// topic is REQUIRED — [SpecTopicRequiredError] is returned if empty.
// ServeSpec must be called AFTER [Client.Attach] (a [Transport] must
// already be present to publish against). Calling ServeSpec twice with
// the SAME topic and the SAME format is a no-op on the second call for
// spec purposes (the channel is first-registered-wins, like any other
// [Publisher.Handle] call against the same topic+type) — it still
// re-publishes the current document each time.
//
// The published payload is RAW, pre-marshaled YAML/JSON text (via
// [format.Binary]/[codex.Bytes]) — a caller SUBSCRIBING to topic must
// declare the SAME format on its own [Subscriber], or decoding will fail
// (the default JSON encoding of a []byte base64-wraps it, which is
// wrong for reading the raw document):
//
//	sub := events.NewChannel[[]byte]("spec/yaml", codex.Bytes()).
//	    WithSubscribe(events.Subscribe{}).
//	    Handle(subscriberClient) // *ChannelHandle, then:
//	sub.WithFormats(format.Binary(codex.Bytes()))
//	subscriberClient.Subscribe(ctx, sub, func(ctx context.Context, body []byte) error {
//	    // body is the raw spec document text
//	    return nil
//	})
func (c *Client) ServeSpec(ctx context.Context, topic string, opts ...SpecOpt) error {
	if topic == "" {
		return SpecTopicRequiredError{}
	}
	var so specOptions
	for _, o := range opts {
		o.applySpecOpt(&so)
	}

	doc, err := c.AsyncAPISpec()
	if err != nil {
		return err
	}

	var body []byte
	var contentType string
	switch so.format {
	case SpecFormatJSON:
		body, err = doc.MarshalJSON()
		contentType = "application/json"
	default:
		body, err = doc.MarshalYAML()
		contentType = "application/yaml"
	}
	if err != nil {
		return err
	}

	ch := NewChannel[[]byte](topic, codex.Bytes(),
		ChannelMeta{Description: "This API's own AsyncAPI specification"},
	)
	pub := ch.WithPublish(Publish{
		OperationID: "publishSpec",
		Summary:     "This API's own AsyncAPI specification",
		Description: "Publishes this API's own AsyncAPI 3.0 document (" + contentType + ").",
		Tags:        []string{"spec"},
		// Security is an explicit, non-nil EMPTY slice — the spec
		// document is public by default, even when the Client declares
		// a [Client.AddGlobalSecurity] requirement for every other
		// channel (nil Security would otherwise INHERIT that global
		// requirement, like any other channel).
		Security: []route.SecurityRequirement{},
	})
	for _, m := range so.middlewares {
		pub = pub.PublishMW(m.mw, m.fn)
	}

	handle, err := pub.Handle(c)
	if err != nil {
		return err
	}
	// WithFormats declares format.Binary(codex.Bytes()) for BOTH
	// publish and subscribe directions — body is already pre-marshaled
	// raw text (YAML or JSON), not a value that should go through the
	// default JSON-encode-of-[]byte path (which base64-wraps it — wrong
	// for a caller subscribing to read the raw document). A caller
	// subscribing to this topic must declare the SAME format.Binary
	// format on its own Subscriber for the payload to decode correctly;
	// see [Client.ServeSpec]'s doc comment's worked example.
	handle.WithFormats(format.Binary(codex.Bytes()))
	return c.Publish(ctx, handle, body)
}
