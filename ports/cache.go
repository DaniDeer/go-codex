package ports

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/schema"
)

// cacheBuilder accumulates [CacheKeyParam] values declared via [CacheOpt].
type cacheBuilder struct {
	params []CacheKeyParam
	// mergeFields holds type-erased codex.FieldCodec[T] values registered
	// via [NewCacheKeyParam]. T is asserted in [NewCache], where T is
	// already known from the format.Format[T] argument.
	mergeFields []any
}

// CacheOpt is the sealed option interface for [CachePattern.Opts]. Currently
// only [CacheKeyParam] implements it (mirrors [FileOpt]/[FilePathParam]).
type CacheOpt interface{ applyCache(*cacheBuilder) }

// CacheKeyParam describes a {varName} placeholder in a [CachePattern.Key]
// template. It mirrors [FilePathParam] — no Required field because
// every template variable must always be present.
//
// CacheKeyParam implements the [CacheOpt] interface: pass it directly in
// [CachePattern.Opts].
type CacheKeyParam struct {
	// Name is the placeholder name (without braces) in the key template.
	// e.g. for template "user:{id}", Name is "id".
	Name string
	// Description enriches documentation for this key variable.
	Description string
	// Codec validates the variable value at [Cache.BuildKey] and
	// [Cache.ValidateKeyVars] time. When non-nil, the codec's schema is
	// available via [Cache.KeySchemas]. Nil means no runtime validation
	// (the pre-CacheKeyParam behavior).
	Codec *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated CacheKeyParam.
// Use this instead of setting Codec directly:
//
//	ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID))
func (p CacheKeyParam) WithCodec(c codex.Codec[string]) CacheKeyParam {
	p.Codec = &c
	return p
}

func (p CacheKeyParam) applyCache(cb *cacheBuilder) { cb.params = append(cb.params, p) }

// toParam converts p to the shared [codex.Param] shape.
func (p CacheKeyParam) toParam() codex.Param {
	return codex.Param{Name: p.Name, Description: p.Description, Codec: p.Codec}
}

// toCodexCacheParams converts params to []codex.Param for
// [codex.BuildFromParams]/[codex.ValidateParams] — mirrors
// ports/file.go's toCodexFileParams exactly.
func toCodexCacheParams(params []CacheKeyParam) []codex.Param {
	out := make([]codex.Param, len(params))
	for i, p := range params {
		out[i] = p.toParam()
	}
	return out
}

// MergedCacheKeyParam is returned by [NewCacheKeyParam]. It embeds the
// unchanged [CacheKeyParam] (spec/validation, exactly as before) plus a
// merge field produced internally via [codex.RequiredField], mirroring
// [MergedFilePathParam] exactly.
type MergedCacheKeyParam[T any] struct {
	CacheKeyParam
	field codex.FieldCodec[T]
}

// NewCacheKeyParam declares a cache key template variable that is BOTH
// validated against codec (exactly like plain [CacheKeyParam], unchanged
// spec/validation behavior) AND merged into T by [Cache.MergeFields] /
// [codex.DecodeVars] — one declaration instead of a CacheKeyParam plus a
// separate codex.Field. Mirrors [ports.NewFilePathParam] exactly; unlike
// File, Cache has no MatchKey inverse (cache keys are built FROM known
// values via [Cache.BuildKey], never reverse-matched from a discovered
// key), so this is the only merge-capable constructor Cache needs.
//
//	userCache := ports.NewCache("user:{id}", format.JSON(userCodec),
//	    ports.NewCacheKeyParam("id", codex.String().Refine(validate.UUID),
//	        func(u User) string { return u.ID },
//	        func(u *User, v string) { u.ID = v }))
//
// V need not be string — see [codex.NewParam] for merging a cache key
// segment directly into an int/UUID/etc.
func NewCacheKeyParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedCacheKeyParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedCacheKeyParam[T]{
		CacheKeyParam: CacheKeyParam{Name: name, Codec: &strCodec},
		field:         codex.RequiredField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value, mirroring CacheKeyParam.WithCodec's existing chain style.
func (p MergedCacheKeyParam[T]) WithDescription(desc string) MergedCacheKeyParam[T] {
	p.Description = desc
	return p
}

func (p MergedCacheKeyParam[T]) applyCache(cb *cacheBuilder) {
	cb.params = append(cb.params, p.CacheKeyParam) // unchanged spec/validation path
	cb.mergeFields = append(cb.mergeFields, p.field)
}

// Cache is a declarative typed cache descriptor: a key template, a default
// TTL, a value [format.Format], and optional per-key-variable codecs. It
// bundles everything a cache adapter (redis.GetAdapter, redis.SetAdapter,
// redis.DrainSetAdapter, redis.Seed) needs, the same way [File]
// bundles everything a file adapter needs.
//
// Cache mirrors [File]'s two construction paths:
//
//   - Declared on a port via [CachePattern] and retrieved with [CacheHandle]
//     — the pipeline-integrated path.
//   - Built directly with [NewCache] for standalone use — cache adapters
//     take a plain Cache[T] value, so redis.GetAdapter/SetAdapter/
//     DrainSetAdapter/Seed work identically with no port/pipeline involved,
//     with the exact same codec validation (value AND key-variable) either
//     way.
type Cache[T any] struct {
	// Key is the declared key template (e.g. "user:{id}").
	Key string
	// TTL is the declared default time-to-live. Zero = no expiry.
	TTL time.Duration
	// Format encodes/decodes cached values through the port's codec —
	// every cache read and write is codec-validated.
	Format format.Format[T]

	// params holds any [CacheKeyParam] values declared via
	// [CachePattern.Opts] or [NewCache]'s variadic options.
	params []CacheKeyParam

	// mergeFields holds the merge-capable fields registered via
	// [NewCacheKeyParam] — see [MergeFields].
	mergeFields []codex.FieldCodec[T]
}

// MergeFields returns the merge-capable fields registered via
// [NewCacheKeyParam] — feed them directly into [codex.DecodeVars]/
// [codex.EncodeVars], or use the bundling convenience built on top:
// `redis.GetMerged` (decode-merge, mirrors
// [events.ChannelHandle.DecodeMerged]) and `redis.SetHandle` (encode-side
// single-call convenience, mirrors [mqtt5.PublishHandle]) — both live in
// `adapters/redis` since, like `zeromq.Call`/`CallHandle`, the actual
// lookup/write needs a [Commands] client Cache itself doesn't have.
func (c Cache[T]) MergeFields() []codex.FieldCodec[T] {
	return c.mergeFields
}

// NewCache creates a [Cache] descriptor from a key template, a value
// [format.Format], and optional [CacheKeyParam] values — the standalone
// constructor for using a cache adapter (redis.GetAdapter, redis.SetAdapter,
// redis.DrainSetAdapter, redis.Seed) directly, with no port/pipeline
// involved. Mirrors [NewFile].
//
// NewCache is infallible — it only captures the spec. Key-variable
// validation runs at [Cache.BuildKey]/[Cache.ValidateKeyVars] time. TTL is
// zero (no expiry) by default — set the returned value's TTL field directly,
// or override per-write with SetAdapterOptions.TTL:
//
//	userCache := ports.NewCache("user:{id}", format.JSON(userCodec),
//	    ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
//	userCache.TTL = 15 * time.Minute
//
//	// Use directly with a cache adapter — no ports.NewIOPort/CachePattern needed.
//	resp, err := redis.Seed(ctx, client, userCache, redis.SeedOptions{})
//
// NewCache PANICS if a merge field registered via [NewCacheKeyParam] does
// not match T — this can only happen if a caller manually constructs a
// [MergedCacheKeyParam] with the wrong type parameter, a programming error
// (mirrors [NewFile]'s panic-on-misuse precedent exactly).
func NewCache[T any](key string, f format.Format[T], opts ...CacheOpt) Cache[T] {
	var cb cacheBuilder
	for _, opt := range opts {
		opt.applyCache(&cb)
	}
	mergeFields := make([]codex.FieldCodec[T], len(cb.mergeFields))
	for i, mf := range cb.mergeFields {
		fc, ok := mf.(codex.FieldCodec[T])
		if !ok {
			panic(fmt.Sprintf("ports: NewCache[%T]: merge field %d has the wrong type parameter (got %T)", *new(T), i, mf))
		}
		mergeFields[i] = fc
	}
	return Cache[T]{Key: key, Format: f, params: cb.params, mergeFields: mergeFields}
}

// CacheKeyTemplate bundles a key template with its declared [CacheKeyParam]
// variables — the payload-independent "shape" of a [Cache]'s key (the SAME
// state [Cache.BuildKey]/[Cache.ValidateKeyVars] already use internally,
// extracted into its own value). Mirrors [FilePathTemplate]/
// [DirPathTemplate], one boundary over.
//
// The plain-string form remains the default and primary way to declare a
// cache — pass a key template string directly to [NewCache], exactly as
// always. Reach for CacheKeyTemplate ONLY when you find yourself declaring
// the SAME key template+params shape for two or more caches (of different
// value types sharing one Redis key family, e.g. "session:{id}" caching
// both a SessionMeta value under one call site and a SessionToken value
// under another) and want that shape to have exactly one source of truth,
// or when you need to build/validate a key standalone, with no value
// format involved at all.
//
// A cache declared via [NewCacheFromKeyTemplate] is byte-for-byte
// identical to one declared via [NewCache] with the same template and
// [CacheKeyParam] values passed inline — nothing downstream can tell the
// difference.
type CacheKeyTemplate struct {
	// Template is the key template, e.g. "user:{id}".
	Template string
	// Params holds the key template's variable declarations.
	Params []CacheKeyParam
}

// NewCacheKeyTemplate declares a CacheKeyTemplate from a template and its
// CacheKeyParam variables.
func NewCacheKeyTemplate(template string, params ...CacheKeyParam) CacheKeyTemplate {
	return CacheKeyTemplate{Template: template, Params: params}
}

// BuildKey substitutes {varName} placeholders in t.Template with the
// values in vars, validating each against its registered
// [CacheKeyParam.Codec] (if any). Mirrors [Cache.BuildKey] exactly (same
// underlying engine, same error types).
func (t CacheKeyTemplate) BuildKey(vars map[string]string) (string, error) {
	result, err := codex.BuildFromParams(t.Template, toCodexCacheParams(t.Params), vars)
	if err != nil {
		return "", convertCacheParamErr(t.Template, err)
	}
	return result, nil
}

// ValidateKeyVars validates vars against t's registered [CacheKeyParam]
// codecs, without building the concrete key. Mirrors [Cache.ValidateKeyVars]
// exactly (same underlying engine, same error types).
func (t CacheKeyTemplate) ValidateKeyVars(vars map[string]string) error {
	return convertCacheParamErr(t.Template, codex.ValidateParams(toCodexCacheParams(t.Params), vars))
}

// NewCacheFromKeyTemplate creates a [Cache] descriptor using a pre-built
// [CacheKeyTemplate] instead of a raw key-template string — see
// [CacheKeyTemplate]'s doc comment for when to reach for this. Produces
// the IDENTICAL [Cache] [NewCache] would produce from t.Template plus
// t.Params passed inline, since [CacheKeyParam] already implements
// [CacheOpt]. Same panic as [NewCache] applies (wrong merge-field type
// parameter).
func NewCacheFromKeyTemplate[T any](t CacheKeyTemplate, f format.Format[T], opts ...CacheOpt) Cache[T] {
	allOpts := make([]CacheOpt, 0, len(t.Params)+len(opts))
	for _, p := range t.Params {
		allOpts = append(allOpts, p)
	}
	allOpts = append(allOpts, opts...)
	return NewCache(t.Template, f, allOpts...)
}

// convertCacheParamErr converts a [codex.MissingParamError]/
// [codex.ParamError] (returned by [codex.BuildFromParams]/
// [codex.ValidateParams]) into ports' own [CacheKeyError]/
// [CacheKeyParamError], threading key through explicitly — unlike
// ports/file.go's convertFileParamErr, Cache's error types carry a Key
// field (the declared key template) that codex.Param's shared errors
// don't have, so this helper takes key as an extra argument. Passes any
// other error (including nil) through unchanged.
func convertCacheParamErr(key string, err error) error {
	var me codex.MissingParamError
	if errors.As(err, &me) {
		return CacheKeyError{Key: key, Var: me.Name}
	}
	var pe codex.ParamError
	if errors.As(err, &pe) {
		return CacheKeyParamError{Key: key, Var: pe.Name, Value: pe.Value, Err: pe.Err}
	}
	return err
}

// BuildKey expands the key template's {var} placeholders from vars,
// validating each declared [CacheKeyParam]'s value through its Codec (if
// set) before substitution. A template without placeholders ignores vars.
// Delegates to [codex.BuildFromParams] — the same shared substitution
// engine rest/events/reqreply/ports.File/ports.Dir all use.
//
// Errors:
//   - [CacheKeyError] — a placeholder has no entry in vars
//   - [CacheKeyParamError] — a declared codec rejects the value
func (c Cache[T]) BuildKey(vars map[string]string) (string, error) {
	result, err := codex.BuildFromParams(c.Key, toCodexCacheParams(c.params), vars)
	if err != nil {
		return "", convertCacheParamErr(c.Key, err)
	}
	return result, nil
}

// ValidateKeyVars validates vars against declared [CacheKeyParam] codecs
// without building the concrete key. Mirrors [File.ValidatePathVars].
// Delegates to [codex.ValidateParams] — same shared engine as [BuildKey].
//
// Returns the first codec failure as a [CacheKeyParamError], or
// [CacheKeyError] for vars referenced by a declared CacheKeyParam but
// absent from vars.
func (c Cache[T]) ValidateKeyVars(vars map[string]string) error {
	return convertCacheParamErr(c.Key, codex.ValidateParams(toCodexCacheParams(c.params), vars))
}

// KeySchemas returns a map from key template variable name to the codec's
// [schema.Schema], for each [CacheKeyParam] with a non-nil Codec. Parameters
// without a codec are omitted. Returns an empty map when no params have
// codecs registered. Mirrors [File.PathParamSchemas].
func (c Cache[T]) KeySchemas() map[string]schema.Schema {
	out := make(map[string]schema.Schema, len(c.params))
	for _, p := range c.params {
		if p.Codec != nil {
			out[p.Name] = p.Codec.Schema
		}
	}
	return out
}

// CacheKeyError is returned by [Cache.BuildKey] when the key template names
// a {var} placeholder that is missing from the supplied vars map.
type CacheKeyError struct {
	// Key is the declared key template (e.g. "user:{id}").
	Key string
	// Var is the placeholder name that has no entry in vars.
	Var string
}

func (e CacheKeyError) Error() string {
	return fmt.Sprintf("cache key %q: missing var %q", e.Key, e.Var)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e CacheKeyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
		slog.String("var", e.Var),
	)
}

// CacheKeyParamError is returned by [Cache.BuildKey] and
// [Cache.ValidateKeyVars] when a declared [CacheKeyParam]'s Codec rejects
// the substituted value.
//
// Use [errors.As] to extract the offending variable:
//
//	var paramErr ports.CacheKeyParamError
//	if errors.As(err, &paramErr) {
//	    slog.Warn("cache key variable rejected",
//	        "key", paramErr.Key, "var", paramErr.Var, "value", paramErr.Value, "cause", paramErr.Err)
//	}
type CacheKeyParamError struct {
	// Key is the declared key template (e.g. "user:{id}").
	Key string
	// Var is the placeholder name (without braces) that failed validation.
	Var string
	// Value is the value that failed validation.
	Value string
	// Err is the underlying constraint or codec error.
	Err error
}

func (e CacheKeyParamError) Error() string {
	return fmt.Sprintf("cache key %q variable %q: invalid value %q: %s", e.Key, e.Var, e.Value, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e CacheKeyParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e CacheKeyParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
		slog.String("var", e.Var),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}
