package codex

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/internal/templatematch"
	"github.com/DaniDeer/go-codex/schema"
)

// ── TemplateStyle ────────────────────────────────────────────────────────
//
// TemplateStyle selects which [internal/templatematch] matching algorithm
// and delimiter a [Template] uses, and whether a GIVEN pattern (not the
// style itself) supports the build (Encode) direction — a wildcard/glob
// pattern has no single value to substitute for "any segment", but
// whether a CONCRETE pattern actually contains one is a per-instance
// question (a MQTTStyle/DottedStyle/GlobStyle template with NO wildcard/
// glob metacharacters in it is perfectly buildable). This mirrors
// ports/file.go's/ports/dir.go's own EXISTING convention exactly: a
// glob-enabled template's BuildPath call returns a typed error (not a
// construction-time panic) — Template.Codec().Encode does the same,
// checking hasWildcard(pattern) at ENCODE time, never at construction.

// TemplateStyle bundles a matching function with a per-pattern
// wildcard/glob detector. Construct via the four named values below
// ([PathStyle]/[MQTTStyle]/[DottedStyle]/[GlobStyle]) — there is no
// public constructor, since the four dialects go-codex actually needs
// are already enumerated.
type TemplateStyle struct {
	name        string
	match       func(template, concrete string, wrapMismatch func(t, c string) error) (map[string]string, error)
	hasWildcard func(pattern string) bool
}

var (
	// PathStyle: "/"-delimited, no wildcards, a {varName} placeholder
	// MAY share a segment with literal text (e.g. "{date}.json") — REST
	// paths, event/reqreply topics, MCP resource URIs, file/dir paths.
	// Always buildable (no wildcard syntax exists in this style at all).
	PathStyle = TemplateStyle{
		name:        "path",
		match:       templatematch.MatchNonWildcard,
		hasWildcard: func(string) bool { return false },
	}

	// MQTTStyle: "/"-delimited, "+"/"#" wildcards — MQTT topic
	// subscriptions. A {varName} placeholder must occupy an entire
	// topic level (cannot share a segment with literal text or another
	// placeholder — see [templatematch.MatchMQTTWildcard]'s own doc
	// comment). Buildable ONLY when the concrete pattern contains no
	// "+"/"#" wildcards — checked at Codec().Encode CALL time (returns
	// [TemplateWildcardBuildError], never panics — mirrors
	// ports/file.go's own glob-template-build-rejection convention).
	MQTTStyle = TemplateStyle{
		name:        "mqtt",
		match:       templatematch.MatchMQTTWildcard,
		hasWildcard: func(pattern string) bool { return hasLevelWildcard(pattern, "/") },
	}

	// DottedStyle: "."-delimited, "+"/"#" wildcards — dotted wire keys
	// ([DottedKeyCodec]/[DottedPatchMapCodec]'s own template dialect).
	// Same buildable-only-without-wildcards rule as [MQTTStyle].
	DottedStyle = TemplateStyle{
		name:        "dotted",
		match:       templatematch.MatchDottedWildcard,
		hasWildcard: func(pattern string) bool { return hasLevelWildcard(pattern, ".") },
	}

	// GlobStyle: "/"-delimited, glob metacharacters ("*"/"**"/"?"/
	// "[...]") — ports/file.go, ports/dir.go's own glob-enabled
	// templates. Buildable ONLY when the concrete pattern contains no
	// glob metacharacters (delegates to
	// [templatematch.IsGlobEnabled]) — same encode-time-error
	// convention as MQTTStyle/DottedStyle.
	GlobStyle = TemplateStyle{
		name:        "glob",
		match:       templatematch.MatchGlob,
		hasWildcard: templatematch.IsGlobEnabled,
	}
)

// hasLevelWildcard reports whether pattern contains an MQTT-style "+" or
// "#" wildcard segment, splitting on delimiter.
func hasLevelWildcard(pattern, delimiter string) bool {
	start := 0
	for i := 0; i <= len(pattern); i++ {
		if i == len(pattern) || string(pattern[i]) == delimiter {
			seg := pattern[start:i]
			if seg == "+" || seg == "#" {
				return true
			}
			start = i + 1
		}
	}
	return false
}

// ── Template ─────────────────────────────────────────────────────────────

// Template bundles a validated "{varName}"/wildcard-style pattern string
// with its own per-var [FieldCodec][T] declarations — the single, shared
// engine behind every path/topic/dotted-key pattern in go-codex,
// replacing several independently duplicated build engines
// (the former api/internal.BuildFromTemplate, ports/file.go's/
// ports/dir.go's inlined copies, codex.DottedKeyCodec's own loop) with
// one canonical implementation built on [internal/templatematch].
//
// Implements [Getter][string] (via an embedded [Const][string] — the
// raw, unsubstituted pattern text, e.g. "/users/{id}") and [HasCodec][T]
// (Codec() Codec[T] — Encode builds a concrete string FROM a T value via
// [EncodeVars]; Decode matches a concrete string INTO a T value via
// [DecodeVars]). Deliberately NOT a [Setter][T]: a Template is
// stateless/pure — producing a concrete value is an Encode call, not a
// runtime "assign" operation the way [Immutable]'s Set is.
//
// A pattern with NO {varName} placeholders at all (a static route/topic/
// path) is simply Template[struct{}] with an empty fields list — no
// separate "static" type or code path. Codec().Encode(struct{}{})
// returns the pattern text unchanged (nothing to substitute);
// Codec().Decode still confirms the concrete string equals the pattern
// exactly (correct: a static route's decode should verify the incoming
// value, not merely assume it).
type Template[T any] struct {
	Const[string]
	style  TemplateStyle
	fields []FieldCodec[T]
}

// NewTemplate validates pattern (non-empty) and that every "{varName}"
// placeholder in pattern has a matching field among fields — PANICS on
// either failure, mirroring [MustConst]/[DottedKeyCodec]'s existing
// panic-on-malformed-declaration convention: a Template's pattern and
// field declarations are always compile-time-authored, so a mismatch
// between them is a programming error, not a runtime condition.
//
// Does NOT reject a wildcard/glob-metacharacter pattern here — a
// MQTTStyle/DottedStyle/GlobStyle template with a "+"/"#"/glob
// metacharacter is perfectly constructible (e.g. for MATCHING/listing
// only); attempting to actually BUILD one returns a typed
// [TemplateWildcardBuildError] from [Template.Codec]'s Encode at CALL
// time instead — mirrors ports/file.go's own existing "glob template
// construction always succeeds; BuildPath on one fails" convention.
func NewTemplate[T any](pattern string, style TemplateStyle, fields ...FieldCodec[T]) Template[T] {
	patternCodec := String().Refine(nonEmptyPatternConstraint)

	declared := make(map[string]bool, len(fields))
	for _, f := range fields {
		name, _, _ := f.schema()
		declared[name] = true
	}
	for name := range templatematch.ParseVars(pattern) {
		if !declared[name] {
			panic(fmt.Sprintf("codex: NewTemplate: pattern %q references {%s}, which has no matching field declared", pattern, name))
		}
	}

	return Template[T]{
		Const:  MustConst(pattern, patternCodec),
		style:  style,
		fields: fields,
	}
}

// nonEmptyPatternConstraint mirrors validate.NonEmptyString without
// importing the validate package from codex (codex must not depend on
// validate — see .github/instructions/go-codex.instructions.md's import
// rules); a Template's own pattern-shape constraint is deliberately
// minimal (non-empty) since richer shape rules are style-specific and
// already enforced by NewTemplate's own wildcard/field checks above.
var nonEmptyPatternConstraint = Constraint[string]{
	Name:  "nonEmptyTemplate",
	Check: func(s string) bool { return s != "" },
	Message: func(s string) string {
		return "template pattern must not be empty"
	},
}

// Codec returns a Codec[T] — Encode builds a concrete string from v (via
// [EncodeVars] over t's declared fields, substituted into the pattern
// via [internal/templatematch.Build]); Decode matches a concrete string
// against the pattern (via t.style's own matcher) and decodes the
// extracted vars into a T (via [DecodeVars]). Template[T] satisfies
// [HasCodec][T].
func (t Template[T]) Codec() Codec[T] {
	pattern := t.Get()
	return Codec[T]{
		Encode: func(v T) (any, error) {
			if t.style.hasWildcard(pattern) {
				return nil, TemplateWildcardBuildError{Template: pattern}
			}
			vars, err := EncodeVars(v, t.fields...)
			if err != nil {
				return nil, err
			}
			return templatematch.Build(pattern, vars, func(name string) error {
				// Unreachable in practice: NewTemplate already panics at
				// construction if pattern references an undeclared var.
				// Kept as a defensive typed error, not a bare panic, in
				// case a caller somehow bypasses NewTemplate's own checks.
				return TemplateVarError{Template: pattern, Name: name}
			})
		},
		Decode: func(raw any) (T, error) {
			var zero T
			s, ok := raw.(string)
			if !ok {
				return zero, TypeMismatchError{Expected: "string", Got: fmt.Sprintf("%T", raw)}
			}
			vars, err := t.style.match(pattern, s, func(template, concrete string) error {
				return TemplateMismatchError{Template: template, Concrete: concrete}
			})
			if err != nil {
				return zero, err
			}
			var out T
			if err := DecodeVars(&out, vars, t.fields...); err != nil {
				return zero, err
			}
			return out, nil
		},
		Schema: schema.Schema{Type: "string"},
	}
}

// Vars returns the set of "{varName}" placeholder names declared in t's
// pattern (e.g. {"id": true} for "/users/{id}").
func (t Template[T]) Vars() map[string]bool {
	return templatematch.ParseVars(t.Get())
}

// Fields returns t's own declared [FieldCodec][T] values, in the order
// passed to [NewTemplate] — used by callers that need to DECOMPOSE an
// already-built Template back into individual field declarations (e.g.
// api/mcp.NewResourceFromTemplate re-declaring them as ResourceOpts to
// delegate to the bare-string primary constructor), mirroring how
// [rest.Path]/[events.Topic]/[ports.FilePathTemplate] already expose
// their own declared Params publicly for the identical purpose.
func (t Template[T]) Fields() []FieldCodec[T] {
	return t.fields
}

// Build substitutes vars into t's pattern and returns the resulting
// concrete string directly. [Template.Codec]'s own Encode returns
// (any, error) since it satisfies [Codec][T] generically, but a
// Template's wire value is ALWAYS a string (Schema.Type is always
// "string") — every caller substituting vars into a path/topic/
// dotted-key template would otherwise repeat the same `raw.(string)`
// type assertion. Build spares them that.
func (t Template[T]) Build(vars T) (string, error) {
	raw, err := t.Codec().Encode(vars)
	if err != nil {
		return "", err
	}
	s, _ := raw.(string)
	return s, nil
}

// ── Structured errors ────────────────────────────────────────────────────

// TemplateWildcardBuildError is returned by [Template.Codec]'s Encode
// direction when the pattern contains a wildcard ("+"/"#", for
// [MQTTStyle]/[DottedStyle]) or glob metacharacter (for [GlobStyle]) —
// such a pattern has no single concrete value to build. Mirrors
// ports/file.go's/ports/dir.go's own FileWildcardBuildError/
// DirWildcardBuildError convention exactly (construction always
// succeeds; only an attempt to BUILD fails).
type TemplateWildcardBuildError struct {
	Template string
}

func (e TemplateWildcardBuildError) Error() string {
	return fmt.Sprintf("codex: template %q contains a wildcard/glob metacharacter and has no single concrete value to build", e.Template)
}

// LogValue implements slog.LogValuer for structured logging.
func (e TemplateWildcardBuildError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("template", e.Template))
}

// TemplateMismatchError reports that a concrete string does not match a
// Template's pattern structurally (wrong segment count, or literal text
// does not match) — returned by [Template.Codec]'s Decode direction.
type TemplateMismatchError struct {
	Template string
	Concrete string
}

func (e TemplateMismatchError) Error() string {
	return fmt.Sprintf("codex: %q does not match template %q", e.Concrete, e.Template)
}

// LogValue implements slog.LogValuer for structured logging.
func (e TemplateMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("concrete", e.Concrete),
	)
}

// TemplateVarError reports that a Template's pattern references a
// "{varName}" placeholder with no corresponding declared field — a
// defensive error for [Template.Codec]'s Encode direction; in practice
// unreachable, since [NewTemplate] already panics on this condition at
// construction time.
type TemplateVarError struct {
	Template string
	Name     string
}

func (e TemplateVarError) Error() string {
	return fmt.Sprintf("codex: template %q references undeclared var %q", e.Template, e.Name)
}

// LogValue implements slog.LogValuer for structured logging.
func (e TemplateVarError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("name", e.Name),
	)
}
