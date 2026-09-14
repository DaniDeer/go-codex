package reqreply

import "github.com/DaniDeer/go-codex/codex"

// PropertyParam describes a named piece of protocol-native metadata
// (MQTT5 User Property, future AMQP message header, ...) — the
// validate-only escape hatch, mirrors [TopicParam] exactly but with NO
// "must appear in the topic template" check (there is no template to
// check against), PLUS a Required field mirroring [rest.HeaderParam.
// Required] (properties, unlike topic vars, are conceptually optional
// metadata). Wraps [codex.Param] directly — the same shared primitive
// TopicParam/HeaderParam/CookieParam/QueryParam already use.
//
// PropertyParam is the reqreply-specific realization of the "property"
// vocabulary axis (see docs/design/d-0003-codec-declared-middlewares.md's Addendum)
// — protocol-neutral named metadata carried separately from the payload
// (MQTT5 User Properties today; a future AMQP adapter's native message
// headers would realize the SAME concept).
type PropertyParam struct {
	codex.Param
	// Required, when true, causes a missing property to fail decode with
	// [MiddlewareInputError] — mirrors [middleware.HeaderParamSpec.Required].
	// Set by which constructor built the owning [MergedPropertyParam]
	// ([NewPropertyParam]: true; [NewOptionalPropertyParam]: false).
	Required bool
}

// WithCodec attaches a codec to p and returns the updated value — mirrors
// [TopicParam.WithCodec] exactly; the only way to add runtime validation
// to a PropertyParam built via a bare struct literal.
func (p PropertyParam) WithCodec(c codex.Codec[string]) PropertyParam { p.Codec = &c; return p }

// applyRoute wires p into rb's property-param list — mirrors
// [TopicParam.applyRoute]'s routeBuilder-option pattern.
func (p PropertyParam) applyRoute(rb *routeBuilder) {
	rb.propertyParams = append(rb.propertyParams, p)
}

// MergedPropertyParam[T] additionally merges this property's value into
// T — mirrors [MergedTopicParam][T] exactly, plus the same Required
// field [PropertyParam] has.
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

// applyRoute wires p into rb's property-param list — mirrors
// [MergedTopicParam.applyRoute]; carries Required through.
func (p MergedPropertyParam[T]) applyRoute(rb *routeBuilder) {
	rb.propertyParams = append(rb.propertyParams, PropertyParam{Param: p.Param, Required: p.Required})
}

// NewPropertyParam declares a property that is BOTH validated AND
// merge-capable into T, and REQUIRED (errors if absent from the
// adapter-supplied property map) — mirrors [NewTopicParam][T,V] exactly,
// wraps [codex.NewParam][T,V] directly (hardcodes RequiredField, same as
// NewTopicParam/NewParam already do).
//
// V need not be string — see [codex.NewParam] for merging a property
// value directly into an int/UUID/etc.
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
// value). Hand-built directly against [codex.OptionalField] — no codex
// package changes needed, since [codex.MergedParam][T]'s Param/Field are
// already exported for exactly this kind of package-local construction.
//
// This is the property axis's one genuine divergence from [TopicParam]
// (topic vars have no optional variant — a template var either appears
// in the topic string, unconditionally required, or doesn't exist at
// all).
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
