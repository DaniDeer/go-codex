package events

import "github.com/DaniDeer/go-codex/codex"

// PropertyParam describes a named piece of protocol-native metadata (MQTT5
// User Property, future AMQP message header, ...) — the validate-only
// escape hatch, mirrors [TopicParam] exactly but with NO "must appear in
// the topic template" check (there is no template to check against), PLUS
// a Required field (properties, unlike topic vars, are conceptually
// optional metadata). Wraps [codex.Param] directly — the SAME shared
// primitive [TopicParam]/rest's HeaderParam/CookieParam/QueryParam already
// use. Mirrors reqreply.PropertyParam field-for-field — see
// docs/roadmap/reqreply-codec-declared-middleware.md's "Phase 0" section.
type PropertyParam struct {
	codex.Param
	// Required, when true, means the property MUST be present in the
	// adapter-supplied property-value map — its absence fails with
	// [MiddlewareInputError] the same way a missing topic var would.
	// When false, absence is NOT an error — see [NewOptionalPropertyParam].
	Required bool
}

// WithCodec sets the validation codec and returns the updated PropertyParam
// — mirrors [TopicParam.WithCodec] exactly.
func (p PropertyParam) WithCodec(c codex.Codec[string]) PropertyParam { p.Codec = &c; return p }

// applyChannel wires p into cb's property-param list — mirrors
// [TopicParam.applyChannel]'s channelBuilder-option pattern.
func (p PropertyParam) applyChannel(cb *channelBuilder) {
	cb.propertyParams = append(cb.propertyParams, p)
}

// MergedPropertyParam[T] additionally merges this property's value into T
// — mirrors [MergedTopicParam][T] exactly PLUS the same Required field
// [PropertyParam] has (set by which constructor built it — see
// [NewPropertyParam]/[NewOptionalPropertyParam] below). A SINGLE embed of
// [codex.MergedParam][T], matching [MergedTopicParam][T]'s real shape.
type MergedPropertyParam[T any] struct {
	codex.MergedParam[T]
	Required bool
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value — mirrors [MergedTopicParam.WithDescription] exactly; the
// only way to add one, since [NewPropertyParam] takes no description
// parameter.
func (p MergedPropertyParam[T]) WithDescription(desc string) MergedPropertyParam[T] {
	p.MergedParam = p.MergedParam.WithDescription(desc)
	return p
}

// applyChannel wires p into cb's property-param list — mirrors
// [MergedTopicParam.applyChannel]; carries Required through.
func (p MergedPropertyParam[T]) applyChannel(cb *channelBuilder) {
	cb.propertyParams = append(cb.propertyParams, PropertyParam{Param: p.Param, Required: p.Required})
}

// NewPropertyParam declares a property that is BOTH validated AND
// merge-capable into T, and REQUIRED (errors if absent from the
// adapter-supplied property map) — mirrors [NewTopicParam][T,V] exactly,
// wraps [codex.NewParam][T,V] directly (hardcodes RequiredField, same as
// NewTopicParam/NewParam already do).
func NewPropertyParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedPropertyParam[T] {
	return MergedPropertyParam[T]{MergedParam: codex.NewParam(name, codec, get, set), Required: true}
}

// NewOptionalPropertyParam declares a property that is validated AND
// merge-capable into T IF PRESENT — absent from the adapter-supplied
// property map is NOT an error (T's field is simply left at its zero
// value). Hand-built directly against [codex.OptionalField] — NO codex
// package changes needed, since [codex.MergedParam][T]'s Param/Field are
// already exported for exactly this kind of package-local construction.
// This is the property axis's ONE genuine divergence from [TopicParam]
// (topic vars have no optional variant).
func NewOptionalPropertyParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedPropertyParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedPropertyParam[T]{
		MergedParam: codex.MergedParam[T]{
			Param: codex.Param{Name: name, Codec: &strCodec},
			Field: codex.OptionalField(name, codec, get, set),
		},
		Required: false,
	}
}
