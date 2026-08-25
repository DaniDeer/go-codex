package codex

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/internal/templatematch"
)

// This file is the SHARED foundation for every "{varName}"-style
// path/topic parameter declaration in go-codex: api/rest's PathParam,
// api/events'/api/reqreply's TopicParam, and their Merged*/New*Param
// counterparts were three independently-duplicated copies of the exact
// same shape (a validate-only escape hatch struct + a merge-capable
// wrapper + a validate loop + a build method + near-identical error
// types). rest/events/reqreply keep their own PathParam/TopicParam
// NAMES (their own domain vocabulary, may diverge further later; Go's
// method-locality rule requires a package-owned type to satisfy their
// sealed RouteOpt/ChannelOpt interfaces anyway) as thin wrappers over the
// types below; their error types become plain type ALIASES over this
// file's error types (zero blast radius to every downstream caller that
// already references e.g. rest.PathParamError by name — TRUE for
// PathParamError/MissingPathVarError's identical field sets, but NOT for
// InvalidPathParamError/InvalidTopicParamError: their field renamed from
// Path/Topic to this file's Template — see each alias's own doc comment
// for the caveat).

// Param describes a "{varName}" placeholder in a path/topic template — the
// shared, VALIDATE-ONLY escape hatch: a variable the caller wants runtime
// codec validation for, without merging its value into any request/content
// type. Use [MergedParam]/[NewParam] instead when the variable's value
// should ALSO be merged into a typed value via [DecodeVars]/[EncodeVars].
type Param struct {
	// Name is the variable name (without braces) as it appears in the template.
	Name string
	// Description is shown in the generated spec (OpenAPI/AsyncAPI) for
	// this parameter.
	Description string
	// Codec validates this parameter's value at [ValidateParams] and
	// [BuildFromParams] time. Nil means no runtime validation; the spec
	// schema will be empty.
	Codec *Codec[string]
}

// WithCodec sets the validation codec and returns the updated Param.
func (p Param) WithCodec(c Codec[string]) Param { p.Codec = &c; return p }

// MergedParam[T] additionally merges this variable's value into T via a
// [FieldCodec][T] — the merge-capable counterpart to [Param]'s
// validate-only escape hatch. Construct via [NewParam].
type MergedParam[T any] struct {
	Param
	// Field is the merge-capable field declaration backing this param —
	// exported so callers embedding MergedParam (rest.MergedPathParam,
	// events.MergedTopicParam, reqreply.MergedTopicParam) can retrieve it
	// for their own builder's merge-fields list.
	Field FieldCodec[T]
}

// NewParam declares a variable that is BOTH validated against codec
// (exactly like plain [Param]) AND merge-capable into T via
// [DecodeVars]/[EncodeVars] — one declaration instead of a Param plus a
// separate [RequiredField].
func NewParam[T any](
	name string,
	codec Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedParam[T] {
	return MergedParam[T]{
		Param: Param{Name: name, Codec: &codec},
		Field: RequiredField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (p MergedParam[T]) WithDescription(desc string) MergedParam[T] {
	p.Description = desc
	return p
}

// ValidateParams validates vars against every param's registered [Param.Codec]
// (if any) — the shared body of rest.RouteHandle.ValidatePathParams/
// events.ChannelHandle.ValidateTopicVars/reqreply.RouteHandle.ValidateTopicVars.
//
// Returns [MissingParamError] if a param with a registered codec has no
// entry in vars. Returns [ParamError] for the first param whose value fails
// its codec. Params with no registered codec are skipped entirely (both for
// presence and validation) — mirrors every caller's existing documented
// behavior.
func ValidateParams(params []Param, vars map[string]string) error {
	for i := range params {
		p := &params[i]
		if p.Codec == nil {
			continue
		}
		val, ok := vars[p.Name]
		if !ok {
			return MissingParamError{Name: p.Name}
		}
		if err := p.Codec.Validate(val); err != nil {
			return ParamError{Name: p.Name, Value: val, Err: err}
		}
	}
	return nil
}

// ValidateDeclaredParams checks that every param's Name appears as a
// "{varName}" placeholder in template — the shared Register-time
// declaration check ("did the caller typo a param name that isn't actually
// in the template"). Returns [InvalidParamError] for the first param whose
// name is not found.
func ValidateDeclaredParams(template string, params []Param) error {
	declared := templatematch.ParseVars(template)
	for i := range params {
		if !declared[params[i].Name] {
			return InvalidParamError{Name: params[i].Name, Template: template}
		}
	}
	return nil
}

// BuildFromParams substitutes "{varName}" placeholders in template with the
// values in vars, validating each against its corresponding param's
// registered [Param.Codec] (if any) — the shared body of
// rest.RouteHandle.BuildPath/events.ChannelHandle.BuildTopic/
// reqreply.RouteHandle.BuildTopic.
//
// All template variables must be present in vars; a missing one returns
// [MissingParamError]. Values are validated (where a codec is registered)
// BEFORE substitution, in template left-to-right order — the first failing
// placeholder (missing or invalid) wins. Keys in vars that do not appear in
// the template are silently ignored. Once every placeholder passes
// validation, the actual substitution delegates to
// [templatematch.Build] — the SAME canonical build-direction function
// [Template]'s own [Template.Codec] (PathStyle/MQTTStyle/etc.) already uses.
func BuildFromParams(template string, params []Param, vars map[string]string) (string, error) {
	codecMap := make(map[string]*Codec[string], len(params))
	for i := range params {
		if params[i].Codec != nil {
			codecMap[params[i].Name] = params[i].Codec
		}
	}

	var firstErr error
	for _, m := range templatematch.ParseVarsInOrder(template) {
		if firstErr != nil {
			break
		}
		value, ok := vars[m]
		if !ok {
			firstErr = MissingParamError{Name: m}
			continue
		}
		if c := codecMap[m]; c != nil {
			if err := c.Validate(value); err != nil {
				firstErr = ParamError{Name: m, Value: value, Err: err}
			}
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return templatematch.Build(template, vars, func(name string) error {
		// Unreachable in practice: the loop above already confirmed every
		// placeholder is present in vars. Kept as a defensive typed error.
		return MissingParamError{Name: name}
	})
}

// ── Structured errors ────────────────────────────────────────────────────

// ParamError is returned by [BuildFromParams]/[ValidateParams] when a
// parameter's value fails its registered codec's constraint.
//
// Use [errors.As] to extract the failing variable name and value:
//
//	var pe codex.ParamError
//	if errors.As(err, &pe) {
//	    log.Printf("bad value %q for {%s}: %v", pe.Value, pe.Name, pe.Err)
//	}
type ParamError struct {
	Name  string // variable name without braces
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e ParamError) Error() string {
	return fmt.Sprintf("codex: param {%s}: invalid value %q: %s", e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to reach the underlying error.
func (e ParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("value", e.Value),
		slog.Any("err", e.Err),
	)
}

// MissingParamError is returned by [BuildFromParams]/[ValidateParams] when a
// "{varName}" placeholder has no corresponding entry in vars.
//
// Use [errors.As] to extract the missing variable name:
//
//	var me codex.MissingParamError
//	if errors.As(err, &me) {
//	    log.Printf("caller forgot to supply variable {%s}", me.Name)
//	}
type MissingParamError struct {
	Name string // the variable name (without braces) that had no value
}

func (e MissingParamError) Error() string {
	return fmt.Sprintf("codex: missing value for param {%s}", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingParamError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("name", e.Name))
}

// InvalidParamError is returned by [ValidateDeclaredParams] when a declared
// [Param]'s Name does not appear as a "{varName}" placeholder in template.
//
// Use [errors.As] to extract the offending name and the template:
//
//	var ipe codex.InvalidParamError
//	if errors.As(err, &ipe) {
//	    log.Printf("param %q not found in template %q", ipe.Name, ipe.Template)
//	}
type InvalidParamError struct {
	Name     string // the variable name (without braces) that is not in the template
	Template string // the template that was checked
}

func (e InvalidParamError) Error() string {
	return fmt.Sprintf("codex: param %q not found in template %q", e.Name, e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e InvalidParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("template", e.Template),
	)
}
