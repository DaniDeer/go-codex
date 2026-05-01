// Package events provides a transport-agnostic event channel builder for go-codex.
//
// Define channels with codec-backed payload types; the builder returns a
// [ChannelHandle] with typed Decode and Encode helpers. Pass those helpers to
// any message broker (MQTT, AMQP, Kafka, NATS) — this package does not import
// any messaging library.
//
// Spec generation is also available: [Builder.AsyncAPISpec] derives a complete
// AsyncAPI 2.6 document from the registered channels.
//
// Typical usage:
//
//	b := events.NewBuilder(events.Info{Title: "User Events", Version: "1.0.0"})
//	b.AddServer("production", events.Server{
//	    URL:      "mqtt://broker.example.com",
//	    Protocol: "mqtt",
//	})
//
//	userCreated := events.AddChannel[UserCreated](b, "user/created", userCreatedCodec,
//	    events.ChannelConfig{
//	        Subscribe: &events.OperationConfig{
//	            Summary:    "A user was created",
//	            SchemaName: "UserCreatedEvent",
//	        },
//	    })
//
//	// In your broker callback (any library):
//	event, err := userCreated.Decode(msg.Payload())   // JSON → UserCreated, validates
//	payload, err := userCreated.Encode(event)          // UserCreated → JSON
//
//	// AsyncAPI 2.6 spec:
//	doc, err := b.AsyncAPISpec()
//	yaml, _  := doc.MarshalYAML()
//
// Encoding is JSON only. AddChannel uses [format.JSON] internally; for other
// formats construct a [format.Format] directly and call its Unmarshal/Marshal.
package events

import (
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/asyncapi"
)

// Info is an alias for [asyncapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = asyncapi.Info

// Server is an alias for [asyncapi.Server].
type Server = asyncapi.Server

// OperationConfig holds metadata for one direction (subscribe or publish) on a
// channel. It controls the operation entry in the AsyncAPI spec.
type OperationConfig struct {
	Summary     string
	Description string
	Tags        []string

	// SchemaName, when non-empty, emits a $ref for the payload schema in the
	// spec and registers the schema under that name in components/schemas.
	SchemaName string
}

// ChannelConfig holds metadata for a channel registration.
//
// At least one of Subscribe or Publish must be non-nil. When both are set, the
// same payload codec is used for both directions.
type ChannelConfig struct {
	Description string

	// Subscribe describes the operation where the application receives messages.
	// Set to nil to omit the subscribe operation from the spec.
	Subscribe *OperationConfig

	// Publish describes the operation where the application sends messages.
	// Set to nil to omit the publish operation from the spec.
	Publish *OperationConfig
}

// ChannelHandle is returned by [AddChannel]. It holds the frozen spec
// descriptor and codec-backed Decode/Encode helpers.
type ChannelHandle[T any] struct {
	// Topic is the channel name (e.g. "user/created", "orders.placed").
	Topic string

	// Descriptor is the frozen asyncapi.ChannelItem built at registration time.
	Descriptor asyncapi.ChannelItem

	// Decode deserialises and validates a JSON payload into T.
	// All Refine constraints on the payload codec run automatically.
	Decode func(payload []byte) (T, error)

	// Encode serialises T to JSON bytes.
	Encode func(msg T) ([]byte, error)
}

// channelEntry is the type-erased interface stored inside Builder.
type channelEntry interface {
	topic() string
	descriptor() asyncapi.ChannelItem
}

// typedChannelEntry stores the frozen topic and descriptor for one channel.
type typedChannelEntry[T any] struct {
	topicStr string
	frozen   asyncapi.ChannelItem
}

func (e *typedChannelEntry[T]) topic() string                    { return e.topicStr }
func (e *typedChannelEntry[T]) descriptor() asyncapi.ChannelItem { return e.frozen }

// Builder accumulates channel registrations and produces AsyncAPI specs.
// Create one with [NewBuilder].
type Builder struct {
	info    Info
	servers map[string]Server
	entries []channelEntry
}

// NewBuilder returns a Builder initialised with the given API metadata.
func NewBuilder(info Info) *Builder {
	return &Builder{
		info:    info,
		servers: make(map[string]Server),
	}
}

// AddServer registers a named server entry in the spec.
func (b *Builder) AddServer(name string, s Server) *Builder {
	b.servers[name] = s
	return b
}

// AddChannel registers a channel with the builder and returns a [ChannelHandle].
//
// codec is used to decode and validate incoming payloads and to encode outgoing
// messages. The same codec applies to both subscribe and publish directions.
//
// AddChannel is a free function (not a method) because Go requires type
// parameters to appear on free functions, not on method receivers.
//
// The descriptor is built and frozen at call time; later mutations to config
// do not affect the registered channel or the returned handle.
func AddChannel[T any](
	b *Builder,
	topic string,
	codec codex.Codec[T],
	config ChannelConfig,
) *ChannelHandle[T] {
	frozen := buildChannelItem(codec, config)

	entry := &typedChannelEntry[T]{topicStr: topic, frozen: frozen}
	b.entries = append(b.entries, entry)

	jsonFmt := format.JSON(codec)

	return &ChannelHandle[T]{
		Topic:      topic,
		Descriptor: frozen,
		Decode:     func(payload []byte) (T, error) { return jsonFmt.Unmarshal(payload) },
		Encode:     func(msg T) ([]byte, error) { return jsonFmt.Marshal(msg) },
	}
}

// AsyncAPISpec builds a complete AsyncAPI 2.6 document from all registered channels.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
	ab := asyncapi.NewDocumentBuilder(b.info)
	for name, s := range b.servers {
		ab.AddServer(name, s)
	}
	for _, e := range b.entries {
		ab.AddChannel(e.topic(), e.descriptor())
	}
	return ab.Build()
}

// buildChannelItem constructs a frozen asyncapi.ChannelItem from the codec schema
// and config. Deep-copies all slices to prevent later mutation from affecting
// the registered channel.
func buildChannelItem[T any](codec codex.Codec[T], config ChannelConfig) asyncapi.ChannelItem {
	item := asyncapi.ChannelItem{
		Description: config.Description,
	}

	if config.Subscribe != nil {
		op := config.Subscribe
		item.Subscribe = &asyncapi.Operation{
			Summary:     op.Summary,
			Description: op.Description,
			Tags:        slices.Clone(op.Tags),
			Message: asyncapi.Message{
				Schema:     codec.Schema,
				SchemaName: op.SchemaName,
			},
		}
	}

	if config.Publish != nil {
		op := config.Publish
		item.Publish = &asyncapi.Operation{
			Summary:     op.Summary,
			Description: op.Description,
			Tags:        slices.Clone(op.Tags),
			Message: asyncapi.Message{
				Schema:     codec.Schema,
				SchemaName: op.SchemaName,
			},
		}
	}

	return item
}
