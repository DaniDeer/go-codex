package events

import (
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/stats"
)

// DeadLetterEnvelope is the FIXED payload schema [DeadLetter] publishes —
// the SAME shape regardless of which channel dead-lettered it. Unlike
// [ErrorChannel]'s codec-backed TYPED payload, DeadLetter's payload has
// no reliable business type to encode once processing has failed
// unmatched — Payload carries the ORIGINAL, UNDECODED bytes (decode may
// itself be what failed, so it is never re-encoded from a typed value).
type DeadLetterEnvelope struct {
	// SourceTopic is the original channel's topic.
	SourceTopic string
	// Payload is the original, undecoded message bytes.
	Payload []byte
	// Error is the failure's error string (Error(), not a typed value —
	// the whole point of a dead letter is that no reliable type exists
	// here).
	Error string
	// Timestamp is when the dead-letter was produced.
	Timestamp time.Time
}

var deadLetterEnvelopeCodec = codex.Struct[DeadLetterEnvelope](
	codex.RequiredField("source_topic", codex.String(),
		func(e DeadLetterEnvelope) string { return e.SourceTopic },
		func(e *DeadLetterEnvelope, v string) { e.SourceTopic = v },
	),
	codex.RequiredField("payload", codex.Bytes(),
		func(e DeadLetterEnvelope) []byte { return e.Payload },
		func(e *DeadLetterEnvelope, v []byte) { e.Payload = v },
	),
	codex.RequiredField("error", codex.String(),
		func(e DeadLetterEnvelope) string { return e.Error },
		func(e *DeadLetterEnvelope, v string) { e.Error = v },
	),
	codex.RequiredField("timestamp", codex.Time(),
		func(e DeadLetterEnvelope) time.Time { return e.Timestamp },
		func(e *DeadLetterEnvelope, v time.Time) { e.Timestamp = v },
	),
)

var deadLetterEnvelopeFormat = format.JSON(deadLetterEnvelopeCodec)

// deadLetterRule is the resolved, per-channel (or global-default)
// DeadLetter declaration. A nil *deadLetterRule (channel-level) means
// "not declared at this channel — inherit the Client-level global
// default, if any." An explicit empty Topic ("") means "opted out" —
// mirrors [route.SecurityRequirement]'s own nil-inherit/empty-override
// convention for [Client.AddGlobalSecurity].
type deadLetterRule struct {
	topic       string
	code        string
	description string
	// schemaName, when non-empty, registers [DeadLetterEnvelope]'s schema
	// under this name in the AsyncAPI document's components/schemas
	// (emitting a $ref); empty inlines the schema instead. Mirrors
	// [reqreply.ErrorPatternOpt.WithSchemaName].
	schemaName string
	// channelAddress, when non-empty, overrides the generated AsyncAPI
	// channel address (default: topic). Mirrors
	// [reqreply.ErrorPatternOpt.WithChannelAddress].
	channelAddress string
	// operationID, when non-empty, overrides the generated receive
	// operation ID. Mirrors [reqreply.ErrorPatternOpt.WithOperationID].
	operationID string
}

// DeadLetterOpt is the [ChannelOpt] value returned by [DeadLetter].
type DeadLetterOpt struct {
	rule deadLetterRule
}

// WithCode returns a copy of o with Code set — purely descriptive
// metadata (no wire effect), for callers who want to distinguish
// multiple declared dead-letter destinations by a short label in logs.
func (o DeadLetterOpt) WithCode(code string) DeadLetterOpt {
	o.rule.code = code
	return o
}

// WithDescription returns a copy of o with Description set — purely
// descriptive metadata (no wire effect).
func (o DeadLetterOpt) WithDescription(desc string) DeadLetterOpt {
	o.rule.description = desc
	return o
}

// WithSchemaName returns a copy of o with SchemaName set, registering
// [DeadLetterEnvelope]'s schema under this name in the AsyncAPI
// document's components/schemas (emitting a $ref) instead of inlining
// it. Multiple DeadLetter declarations sharing one destination topic
// (e.g. via [Client.AddGlobalDeadLetter]) should use the SAME SchemaName
// to avoid registering the identical schema under different names.
func (o DeadLetterOpt) WithSchemaName(name string) DeadLetterOpt {
	o.rule.schemaName = name
	return o
}

// WithChannelAddress returns a copy of o with ChannelAddress set,
// overriding the generated AsyncAPI channel address (default: the
// dead-letter topic itself).
func (o DeadLetterOpt) WithChannelAddress(addr string) DeadLetterOpt {
	o.rule.channelAddress = addr
	return o
}

// WithOperationID returns a copy of o with OperationID set, overriding
// the generated receive operation ID.
func (o DeadLetterOpt) WithOperationID(id string) DeadLetterOpt {
	o.rule.operationID = id
	return o
}

func (o DeadLetterOpt) applyChannel(cb *channelBuilder) {
	rule := o.rule
	cb.deadLetterRule = &rule
}

// DeadLetter declares a fallback destination topic for a message that
// could not be processed for ANY reason NOT already covered by a more
// specific declared [ErrorChannel] — a decode failure, or a business
// error whose type matches no declared ErrorChannel. Fires strictly
// AFTER every declared ErrorChannel has been tried and found no match
// (or was not declared at all) — never instead of a more specific
// match. See [ChannelHandle.DeadLetterFor].
//
// Declared alongside ErrorChannel on the SAME NewChannel call:
//
//	events.NewChannel[Reading]("sensors/{id}/data", readingCodec,
//	    events.ErrorChannel[domain.ValidationError, ErrorPayload](...), // specific, tried first
//	    events.DeadLetter("sensors/dead-letter"),                        // catch-all fallback
//	)
//
// A channel with no explicit DeadLetter opt inherits the Client-level
// default declared via [Client.AddGlobalDeadLetter]. To opt a specific
// channel OUT of an inherited global default, declare
// DeadLetter("") — an empty topic is treated as an explicit disable,
// mirroring [Client.AddGlobalSecurity]'s own empty-slice opt-out
// convention.
func DeadLetter(topic string, opts ...DeadLetterOpt) DeadLetterOpt {
	o := DeadLetterOpt{rule: deadLetterRule{topic: topic}}
	for _, opt := range opts {
		o.rule.code = opt.rule.code
		o.rule.description = opt.rule.description
		o.rule.schemaName = opt.rule.schemaName
		o.rule.channelAddress = opt.rule.channelAddress
		o.rule.operationID = opt.rule.operationID
	}
	return o
}

// registerDeadLetterChannel registers rule's destination topic as its own
// AsyncAPI channel entry on client — a receive-only channel carrying the
// FIXED [DeadLetterEnvelope] schema, the direct analogue of how a
// regular channel gets its own entry via [Subscriber.Handle]/
// [Publisher.Handle]. Deduped by topic via
// [Client.deadLetterTopicsRegistered] — several channels commonly share
// ONE dead-letter destination (e.g. via [Client.AddGlobalDeadLetter]),
// so only the first caller to resolve a given topic registers it.
//
// MUST be called with client.mu already held for writing (mirrors every
// other client.entries mutation in this file).
func registerDeadLetterChannel(client *Client, rule *deadLetterRule) {
	if rule == nil || rule.topic == "" {
		return
	}
	if client.deadLetterTopicsRegistered == nil {
		client.deadLetterTopicsRegistered = make(map[string]bool)
	}
	if client.deadLetterTopicsRegistered[rule.topic] {
		return
	}
	client.deadLetterTopicsRegistered[rule.topic] = true

	address := rule.topic
	if rule.channelAddress != "" {
		address = rule.channelAddress
	}
	msg := asyncapi.Message{
		Name:        "DeadLetter",
		Schema:      deadLetterEnvelopeCodec.Schema,
		SchemaName:  rule.schemaName,
		ContentType: "application/json",
	}
	item := asyncapi.ChannelItem{
		Address:     address,
		Description: rule.description,
		Subscribe: &asyncapi.Operation{
			OperationID: rule.operationID,
			Description: rule.description,
			Message:     msg,
		},
	}
	client.entries = append(client.entries, &rawChannelEntry{topicStr: rule.topic, item: item})
}

// DeadLetterFor is the RECOMMENDED single call site for dead-lettering a
// message that could not be processed — see docs/roadmap/
// d-0005-error-handling.md's Topic 4. Builds the envelope
// for a failure at sourceTopic, reports it to obs (stats.ReportErrors(obs,
// "dead_letter", err)), and returns the destination topic + already-
// ENCODED envelope bytes ready to publish — or ok=false when no
// DeadLetter is declared (globally or on this channel), in which case the
// caller does nothing further.
//
// rawPayload is the original, undecoded message bytes — passed
// explicitly by the caller (a core-layer function cannot retrieve it
// from ctx: each adapter stores its raw message under its OWN private
// context key type).
//
// Does NOT itself consult ErrorResponseFor/ErrorChannelFor — callers are
// responsible for calling this ONLY after a more specific declared
// ErrorChannel has already been tried and missed, or immediately for
// decode-class failures where no such pattern could ever apply.
func (h *ChannelHandle[T]) DeadLetterFor(obs stats.Observer, sourceTopic string, rawPayload []byte, err error) (topic string, body []byte, ok bool) {
	dl := h.deadLetterRule
	if dl == nil || dl.topic == "" {
		return "", nil, false
	}
	envelope := DeadLetterEnvelope{
		SourceTopic: sourceTopic,
		Payload:     rawPayload,
		Error:       err.Error(),
		Timestamp:   time.Now().UTC(),
	}
	encoded, encErr := deadLetterEnvelopeFormat.Marshal(envelope)
	if encErr != nil {
		stats.ReportErrors(obs, "dead_letter", encErr)
		return "", nil, false
	}
	stats.ReportErrors(obs, "dead_letter", err)
	return dl.topic, encoded, true
}
