package ports

import (
	"fmt"
	"log/slog"
	"strings"
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
func NewCacheKeyParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedCacheKeyParam[T] {
	return MergedCacheKeyParam[T]{
		CacheKeyParam: CacheKeyParam{Name: name, Codec: &codec},
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
// [codex.EncodeVars]. No bundling convenience method exists for Cache
// (unlike [rest.RouteHandle.DecodeMerged]/[events.ChannelHandle.DecodeMerged])
// since [Cache.Get]/[Cache.Set] already take vars directly — there is no
// "body vs. vars" split to coordinate.
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

// codecFor returns the codec declared for a key var name, or nil.
func (c Cache[T]) codecFor(name string) *codex.Codec[string] {
	for i := range c.params {
		if c.params[i].Name == name {
			return c.params[i].Codec
		}
	}
	return nil
}

// BuildKey expands the key template's {var} placeholders from vars,
// validating each declared [CacheKeyParam]'s value through its Codec (if
// set) before substitution. A template without placeholders ignores vars.
//
// Errors:
//   - [CacheKeyError] — a placeholder has no entry in vars
//   - [CacheKeyParamError] — a declared codec rejects the value
func (c Cache[T]) BuildKey(vars map[string]string) (string, error) {
	key := c.Key
	for {
		start := strings.IndexByte(key, '{')
		if start < 0 {
			return key, nil
		}
		end := strings.IndexByte(key[start:], '}')
		if end < 0 {
			return key, nil // unbalanced brace: treat the rest as literal
		}
		name := key[start+1 : start+end]
		val, ok := vars[name]
		if !ok {
			return "", CacheKeyError{Key: c.Key, Var: name}
		}
		if codec := c.codecFor(name); codec != nil {
			if err := codec.Validate(val); err != nil {
				return "", CacheKeyParamError{Key: c.Key, Var: name, Value: val, Err: err}
			}
		}
		key = key[:start] + val + key[start+end+1:]
	}
}

// ValidateKeyVars validates vars against declared [CacheKeyParam] codecs
// without building the concrete key. Mirrors [File.ValidatePathVars].
//
// Returns the first codec failure as a [CacheKeyParamError], or
// [CacheKeyError] for vars referenced by a declared CacheKeyParam but
// absent from vars.
func (c Cache[T]) ValidateKeyVars(vars map[string]string) error {
	for i := range c.params {
		p := &c.params[i]
		if p.Codec == nil {
			continue
		}
		val, ok := vars[p.Name]
		if !ok {
			return CacheKeyError{Key: c.Key, Var: p.Name}
		}
		if err := p.Codec.Validate(val); err != nil {
			return CacheKeyParamError{Key: c.Key, Var: p.Name, Value: val, Err: err}
		}
	}
	return nil
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
