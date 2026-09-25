package reqreply

import (
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/stats"
)

// DeadLetterEnvelope is the FIXED payload schema [DeadLetter] publishes —
// the SAME shape regardless of which route dead-lettered it. Unlike
// [ErrorPattern]'s codec-backed TYPED payload, DeadLetter's payload has
// no reliable business type to encode once processing has failed
// unmatched — Payload carries the ORIGINAL, UNDECODED bytes (decode may
// itself be what failed, so it is never re-encoded from a typed value).
//
// Complementary to (not a replacement for) the synchronous error reply
// (Topic 2's same-reply-topic ratification) — the caller STILL gets an
// error reply on the same reply topic regardless; DeadLetter ADDITIONALLY
// preserves a durable record for ops/replay. Both fire together on an
// unmatched failure, not as alternatives.
type DeadLetterEnvelope struct {
	// SourceTopic is the original route's request topic.
	SourceTopic string
	// Payload is the original, undecoded request bytes.
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

// deadLetterRule is the resolved, per-route (or global-default)
// DeadLetter declaration. A nil *deadLetterRule (route-level) means "not
// declared at this route, inherit the Server-level global default, if
// any." An explicit empty Topic ("") means "opted out" — mirrors
// [route.SecurityRequirement]'s own nil-inherit/empty-override
// convention for [Builder.AddGlobalSecurity].
type deadLetterRule struct {
	topic       string
	code        string
	description string
	// schemaName, when non-empty, registers [DeadLetterEnvelope]'s schema
	// under this name in the AsyncAPI document's components/schemas
	// (emitting a $ref); empty inlines the schema instead. Mirrors
	// [ErrorPatternOpt.WithSchemaName].
	schemaName string
	// channelAddress, when non-empty, overrides the generated AsyncAPI
	// channel address (default: topic). Mirrors
	// [ErrorPatternOpt.WithChannelAddress].
	channelAddress string
	// operationID, when non-empty, overrides the generated receive
	// operation ID. Mirrors [ErrorPatternOpt.WithOperationID].
	operationID string
}

// DeadLetterOpt is the [RouteOpt] value returned by [DeadLetter].
type DeadLetterOpt struct {
	rule deadLetterRule
}

// WithCode returns a copy of o with Code set — purely descriptive
// metadata (no wire effect).
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
// (e.g. via [Server.AddGlobalDeadLetter]) should use the SAME SchemaName
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

func (o DeadLetterOpt) applyRoute(rb *routeBuilder) {
	rule := o.rule
	rb.deadLetterRule = &rule
}

// DeadLetter declares a fallback destination topic for a request that
// could not be processed for ANY reason NOT already covered by a more
// specific declared [ErrorPattern] — a decode failure, or a business
// error whose type matches no declared ErrorPattern. Fires strictly
// AFTER every declared ErrorPattern has been tried and found no match
// (or was not declared at all) — never instead of a more specific
// match. See [RouteHandle.DeadLetterFor].
//
// A route with no explicit DeadLetter opt inherits the Server-level
// default declared via [Builder.AddGlobalDeadLetter]. To opt a specific
// route OUT of an inherited global default, declare DeadLetter("") — an
// empty topic is treated as an explicit disable, mirroring
// [Builder.AddGlobalSecurity]'s own empty-slice opt-out convention.
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

// registerDeadLetterChannel registers rule's destination topic as its
// own AsyncAPI channel entry on b — a receive-only channel carrying the
// FIXED [DeadLetterEnvelope] schema. Deduped by topic via
// [Server.deadLetterTopicsRegistered] — several routes commonly share
// ONE dead-letter destination (e.g. via [Server.AddGlobalDeadLetter]),
// so only the first route to resolve a given topic registers it.
func registerDeadLetterChannel(b *Server, rule *deadLetterRule) {
	if rule == nil || rule.topic == "" {
		return
	}
	if b.deadLetterTopicsRegistered == nil {
		b.deadLetterTopicsRegistered = make(map[string]bool)
	}
	if b.deadLetterTopicsRegistered[rule.topic] {
		return
	}
	b.deadLetterTopicsRegistered[rule.topic] = true

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
	b.docBuilder.AddChannel(rule.topic, asyncapi.ChannelItem{
		Address:     address,
		Description: rule.description,
		Subscribe: &asyncapi.Operation{
			OperationID: rule.operationID,
			Description: rule.description,
			Message:     msg,
		},
	})
}

// DeadLetterFor is the RECOMMENDED single call site for dead-lettering a
// request that could not be processed — see
// docs/design/d-0005-error-handling.md's Topic 4. Builds the envelope
// for a failure at sourceTopic, reports it to obs (stats.ReportErrors(obs,
// "dead_letter", err)), and returns the destination topic + already-
// ENCODED envelope bytes ready to publish — or ok=false when no
// DeadLetter is declared (globally or on this route), in which case the
// caller does nothing further.
//
// rawPayload is the original, undecoded request bytes — passed
// explicitly by the caller (a core-layer function cannot retrieve it
// from ctx: each adapter stores its raw message under its OWN private
// context key type).
//
// Does NOT itself consult ErrorResponseFor — callers are responsible for
// calling this ONLY after a more specific declared ErrorPattern has
// already been tried and missed, or immediately for decode-class
// failures where no such pattern could ever apply.
func (h *RouteHandle[Req, Resp]) DeadLetterFor(obs stats.Observer, sourceTopic string, rawPayload []byte, err error) (topic string, body []byte, ok bool) {
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
