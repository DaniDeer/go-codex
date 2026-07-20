# Declarative Var Extraction & Merge — `codex.DecodeVars`/`EncodeVars`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> **Revision note (2026-07):** refined after the user explicitly permitted
> breaking changes if they benefit simplicity/consistency. See "Considered
> and rejected: generic Param types" below for why the design stays fully
> additive anyway — breaking the 7 existing Param types bought no extra
> capability over an additive alternative, only a verbosity regression for
> the common (validate-only) case. What DID change: the previously-deferred
> "declare twice" limitation (a separate `FilePathParam`/`PathParam`/etc. for
> spec/validation AND a separate `codex.Field` for merge) is now resolved in
> Phase 1 via new additive per-boundary constructors — see "Unifying the
> declaration: additive per-boundary constructors" below.

## Motivation

Every out-of-band string-keyed source in go-codex — file path templates
(`ports.FilePathParam`), MQTT/event topic templates (`events.TopicParam`,
`reqreply.TopicParam`), REST path/query/header/cookie parameters
(`rest.PathParam`/`QueryParam`/`HeaderParam`/`CookieParam`), and cache key
templates (`ports.CacheKeyParam`) — already lets a user declare a named
`{varName}` placeholder with an optional `Codec[string]` for **validation**.
None of them let the user say "and put the validated value into this struct
field" — that merge step is always hand-written today:

```go
// examples/adapters-nethttp/main.go, today:
r, _ := nethttp.RequestFromContext(ctx)
id := r.PathValue("id") // no codec, no typed field, no validation
```

```go
// adapters/file, adapters/redis, adapters/mqtt, adapters/zeromq — every
// SinkAdapter/IOAdapter constructor requires a hand-written varsFor closure:
varsFor := func(r SensorReading) map[string]string {
    return map[string]string{"sensorID": r.SensorID}
}
```

And the reverse direction — parsing information embedded in a **discovered**
file path (the user's stated use case: "encode/decode information from the
file path into the respective struct, e.g. filename") — has **no primitive
at all** for `ports.File`. `ports.File.BuildPath` only goes forward (known
vars → concrete path); there is no inverse "match a concrete path against
the template and extract vars" the way `mqtt.TopicVarsFromMessage` already
does for MQTT topics.

This roadmap closes both gaps with **one small, reusable primitive** rather
than adding a new declaration surface per boundary: it turns out
`codex.Field[T,F]` (already used for `codex.Struct[T]`'s JSON object fields)
is *exactly* the right shape for a named, codec-validated, gettable/settable
template variable too — the same `RequiredField`/`OptionalField`/`DefaultField`
declarations already in every codebase work unchanged. What's missing is a
pair of free functions that decode/encode a `map[string]string` (instead of
a JSON `map[string]any`) using those same field declarations, plus one new
reverse-match method on `ports.File` to produce that map from a discovered
path.

### A fifth beneficiary found by inspection: `config.FromEnv`/`FromEnvVar`

Two other boundaries were checked against this same pattern and yield an
asymmetric answer:

- **`adapters/sql` does NOT benefit** — sqlc already hands back a
  fully-assembled Go struct row per query; `codex.Struct[T]` (via
  `sql.Validate`) already does the complete "one declaration, full
  decode+validate" job in a single call. There is no template-string
  boundary to match/extract (`ports.SQLPattern` is deliberately
  metadata-only — `{Table, Op string}` — precisely because nothing there is
  templatable). The "declare twice" problem this roadmap solves does not
  exist for SQL.
- **`config.FromEnv`/`FromEnvVar` DOES benefit — for free, with no new
  per-boundary constructor needed.** `docs/features/config.md`'s own "Config
  file + env var overrides" recipe is hand-rolled today (`os.Getenv(...)` +
  manual `strconv.Atoi` + manual field assignment — the exact same class of
  friction as `r.PathValue("id")`). Unlike path/query/topic/cache-key
  params, env vars have no OpenAPI/AsyncAPI spec-generation role, so there
  is no separate "spec Param" type to unify a merge field with in the first
  place (`config` declares no `EnvVarParam` at all) — env vars get the win
  directly from the CORE `codex.DecodeVars` primitive already in scope,
  with zero additional code:
  `codex.DecodeVars(&cfg, map[string]string{"port": os.Getenv("APP_PORT")}, portField)`
  replaces the hand-rolled recipe outright. See "Files to create" for the
  `docs/features/config.md` update this implies.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `codex.DecodeVars[T](target *T, vars map[string]string, fields ...FieldCodec[T]) error` — decode named vars into (a subset of) an existing struct's fields, reusing the exact `Field[T,F]`/`RequiredField`/`OptionalField`/`DefaultField` declarations already used for `codex.Struct[T]` | A new declaration type (`VarField`, `PathField`, etc.) — deliberately reuses `Field[T,F]` verbatim; this is the central design decision, not an implementation shortcut |
| `codex.EncodeVars[T](v T, fields ...FieldCodec[T]) (map[string]string, error)` — the inverse: extract field values from a struct into a vars map, replacing hand-written `varsFor func(T) map[string]string` closures across `adapters/file`, `adapters/redis`, `adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq` | Changing any existing adapter's `varsFor func(T) map[string]string` parameter *signature* — `EncodeVars` is called *inside* a user-supplied closure (`func(v T) map[string]string { return codex.Must(codex.EncodeVars(v, fields...)) }`); zero adapter code changes required |
| `ports.File[T].MatchPath(path string) (map[string]string, error)` — the inverse of `BuildPath`: matches a concrete discovered file path against the template, returns extracted+validated vars (mirrors `mqtt.TopicVarsFromMessage`'s existing pattern for topics) | Directory scanning / glob / `fsnotify` watching to *produce* candidate paths — `MatchPath` operates on a path the caller already obtained by whatever means (their own `filepath.WalkDir`, `filepath.Glob`, etc.); go-codex stays transport/discovery-agnostic |
| Hoisting the segment-matching logic (`{var}` placeholder ↔ literal segment matching) that already exists, MQTT-specific and unexported, in `adapters/mqtt/topicvars.go`'s `matchTopicTemplate`, into a shared, wildcard-free `api/internal.MatchTemplate(template, concrete string) (map[string]string, error)` that both `File.MatchPath` and (unchanged) `mqtt.TopicVarsFromMessage` can build on | Adding wildcard (`+`/`#`) support to `File.MatchPath` — file paths have no MQTT wildcard semantics; the shared helper handles only literal segments + `{var}` placeholders, and MQTT's wrapper keeps its own wildcard handling on top |
| Reusing `codex.ValidationErrors`/`ValidationError` for `DecodeVars` failures (zero new error types for the decode direction — same shape `Struct.Decode` already produces) | — |
| One new error type, `VarEncodeTypeError`, for the one genuinely new failure mode `EncodeVars` introduces (a field's codec `Encode` returned a non-`string` value — only possible if a user attaches a non-string-wire codec to a var field, which is a caller programming error, not a runtime data error) | — |
| One new error type, `FilePathMismatchError`, mirroring `mqtt.TopicMismatchError` exactly, for `MatchPath` structural mismatches (wrong segment count, literal segment doesn't match) | — |
| **Exporting `codex`'s unexported `fieldCodec[T]` interface as `codex.FieldCodec[T]`** (mechanical rename — since it was unexported, no external caller could reference it before, so nothing existing breaks; it only unlocks other packages naming the type in their own signatures) | Exporting its METHODS — they stay unexported so only `codex.RequiredField`/`OptionalField`/`DefaultField` can produce values satisfying it; external packages can hold/pass `FieldCodec[T]` values but never implement the interface themselves |
| **New additive "declare once" constructors per boundary** — `rest.NewPathParam[T]`, `rest.NewRequiredQueryParam[T]`/`NewOptionalQueryParam[T]` (+ Header/Cookie equivalents), `events.NewTopicParam[T]`, `reqreply.NewTopicParam[T]`, `ports.NewFilePathParam[T]`, `ports.NewCacheKeyParam[T]` — each is sugar for "declare the existing spec Param AND a merge field from ONE call," `T` inferred from `get`/`set` arguments exactly like `codex.RequiredField` already infers `T`/`F` | Making the EXISTING `PathParam`/`QueryParam`/`HeaderParam`/`CookieParam`/`TopicParam`/`FilePathParam`/`CacheKeyParam` types themselves generic — considered and rejected, see below |
| New accessor/convenience methods: `RouteHandle.MergeFields()`, `ChannelHandle.MergeFields()`, `ports.File.MergeFields()` (feed directly into `codex.DecodeVars`/`EncodeVars`), plus `RouteHandle.DecodeMerged(body, pathVars, query, headers, cookies map[string]string) (Req, error)` — one call, fully merged and validated | Changing `RouteHandle.Decode`'s existing signature — `DecodeMerged` is a NEW, additive method; `Decode` is untouched and keeps working exactly as today for routes with no merge-capable params declared |
| Updating `adapters/nethttp`/`adapters/chi` `Handler` internals to call `DecodeMerged` automatically when the route declares merge-capable params (backward compatible: identical behavior when none are declared) | Updating every other adapter (`mqtt5`, `zeromq`, `mcpgo`) in Phase 1 — REST is the highest-value, most-requested surface (explicitly named in the original ask: "for rest routes in header fields, query parameters"); the same recipe (accessor + adapter integration) applies mechanically to events/reqreply adapters later, tracked as a Phase 1 "mechanical repeat," not blocked on a separate roadmap doc |

### Considered and rejected: making the existing Param types generic

Given the user's explicit permission to make breaking changes, the most
direct-seeming design would be to add `Get`/`Set` fields straight onto the
existing `PathParam`/`QueryParam`/`HeaderParam`/`CookieParam`/`TopicParam`/
`FilePathParam`/`CacheKeyParam` structs by making each generic over the
target struct `T` (e.g. `PathParam[Req]{Name, Codec, Get func(Req) string,
Set func(*Req, string)}`).

This was evaluated and **rejected**: Go does not infer generic type
parameters for a **struct literal** from surrounding context (unlike
function-argument inference, which `RequiredField[T,F](name, codec, get,
set)` already relies on). Making these types generic would force EVERY
existing declaration — including the common validate-only case that never
wants a merge field — to spell the type parameter explicitly:
`rest.PathParam[CreateUserReq]{Name: "id"}` instead of today's
`rest.PathParam{Name: "id"}`. That is a verbosity regression for the
majority use case, in exchange for **no additional capability** over the
additive alternative below (which achieves identical unification with zero
impact on existing declarations). `interface{}`-typed `Get`/`Set` functions
(`Get func(any) string`) were also considered and rejected outright — that
would reintroduce the `interface{}` boxing the whole library's design
philosophy explicitly avoids.

### Unifying the declaration: additive per-boundary constructors

Instead, each boundary gains a **new constructor function** — not a new
type shape to learn, just one more entry in the same `RequiredField`-style
constructor family users already know — that declares the existing spec
Param AND a merge field together, from one call:

```go
// api/rest/builder.go — additive; PathParam itself is UNCHANGED.
type MergedPathParam[T any] struct {
    PathParam                 // embeds the existing, unchanged type
    field codex.FieldCodec[T] // produced internally via codex.RequiredField
}

// NewPathParam declares a path parameter that is BOTH validated against
// codec (exactly like plain PathParam, unchanged spec/validation behavior)
// AND automatically merged into Req by [RouteHandle.DecodeMerged] — one
// declaration instead of a PathParam plus a separate codex.Field.
//
//	rest.NewRoute[GetUserReq, User]("GET", "/users/{id}", reqCodec, userCodec,
//	    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
//	        func(r GetUserReq) string { return r.ID },
//	        func(r *GetUserReq, v string) { r.ID = v },
//	    ),
//	)
func NewPathParam[T any](
    name string,
    codec codex.Codec[string],
    get func(T) string,
    set func(*T, string),
) MergedPathParam[T] {
    return MergedPathParam[T]{
        PathParam: PathParam{Name: name, Codec: &codec},
        field:     codex.RequiredField(name, codec, get, set),
    }
}

func (p MergedPathParam[T]) applyRoute(rb *routeBuilder) {
    rb.pathParams = append(rb.pathParams, p.PathParam) // unchanged spec/validation path
    rb.mergeFields = append(rb.mergeFields, p.field)    // NEW — type-erased `any`, asserted at Register time
}

// WithDescription sets the PARAMETER-level description (rendered into the
// OpenAPI "parameter" object, distinct from the CODEC's schema-level
// description — see "Setting additional options" below) and returns the
// updated value, mirroring PathParam.WithCodec's existing chain style.
func (p MergedPathParam[T]) WithDescription(desc string) MergedPathParam[T] {
    p.Description = desc // promoted field, from the embedded PathParam
    return p
}
```

`QueryParam`/`HeaderParam`/`CookieParam` get the same treatment with
`Required`/`Optional` pairs matching `codex.RequiredField`/`OptionalField`'s
existing naming convention exactly:

| Boundary | New constructors |
|---|---|
| `api/rest` (path) | `NewPathParam[T]` (always required, matches today's `PathParam` doc comment) |
| `api/rest` (query/header/cookie) | `NewRequiredQueryParam[T]`/`NewOptionalQueryParam[T]`, `NewRequiredHeaderParam[T]`/`NewOptionalHeaderParam[T]`, `NewRequiredCookieParam[T]`/`NewOptionalCookieParam[T]` |
| `api/events` | `NewTopicParam[T]` (always required, matches today's `TopicParam`) |
| `api/reqreply` | `NewTopicParam[T]` (always required) |
| `ports` (file) | `NewFilePathParam[T]` (always required) |
| `ports` (cache) | `NewCacheKeyParam[T]` (always required) |

### Setting additional options: the `WithDescription` chain, and "primary but not sole"

Checking the complete field inventory of all 7 existing Param types
(`PathParam`/`QueryParam`/`HeaderParam`/`CookieParam`/`TopicParam`×2/
`FilePathParam`/`CacheKeyParam`) confirms there is exactly ONE additional
option beyond `Name`/`Codec` that any of them carry: **`Description`**
(universal). `Required` (query/header/cookie only) is already resolved by
the `NewRequired*`/`NewOptional*` constructor split above — no separate
toggle needed. Nothing else exists at the Param level: examples, titles,
and deprecation all live on the CODEC's own schema via its existing chain
(`codex.String().Refine(...).WithDescription(...).WithExample(...)`), which
composes naturally since the codec is a plain value passed into the
constructor.

**A genuine subtlety, confirmed via `render/openapi/document.go`**: the
Param's OWN `Description` (rendered into the OpenAPI *parameter* object,
`document.go:278-279`) and the codec's `Schema.Description` (rendered into
the nested *schema* object, `document.go:389-390`) are TWO DIFFERENT fields
serving different purposes — "what this parameter means" vs. "what the
value's format/constraints are." Today NEITHER `PathParam` nor any other
Param type has a `WithDescription` method (Description is currently
struct-literal-only) — the new constructors need one to stay
literal-free, so each `Merged*` wrapper gets a new
`WithDescription(string)` chain method, matching the style
`Codec[T].WithDescription`/`PathParam.WithCodec` already establish:

```go
rest.NewPathParam("id",
    codex.String().Refine(validate.UUID).WithDescription("Must be a valid UUID v4"), // schema-level
    func(r GetUserReq) string { return r.ID },
    func(r *GetUserReq, v string) { r.ID = v },
).WithDescription("The user's unique identifier") // param-level, NEW
```

**Primary but not sole** (explicit decision, confirmed with the user):
`NewPathParam`/`NewRequiredQueryParam`/etc. become the DOCUMENTED,
RECOMMENDED, PRIMARY way to declare a param from Phase 1 onward — guides
and examples lead with them. The plain `PathParam{Name, Description,
Codec}` struct-literal style is NOT removed or deprecated: it remains the
low-level escape hatch for genuinely validate-only params with no merge
need. Forcing every declaration through `NewPathParam` would mandate a
`get`/`set` pair even for spec-only params the handler never reads
directly — a real ergonomics tax with nothing to return for that case. Both
styles register cleanly today's way (`rb.pathParams`) — a route can freely
mix a `NewPathParam` (merge-capable) with a plain `PathParam` (validate-only)
in the same `NewRoute` call.

`RouteHandle`/`ChannelHandle`/`File` gain a `MergeFields() []codex.FieldCodec[T]`
accessor (collects whatever merge fields were registered via the new
constructors) for callers who want to invoke `codex.DecodeVars`/`EncodeVars`
themselves, plus one additive convenience method that closes the loop
completely for REST:

```go
// DecodeMerged decodes body (if the route has a request body) AND merges
// every MergeFields()-registered path/query/header/cookie value into the
// SAME Req value, using codex.DecodeVars internally. Additive — [Decode]
// is unchanged and keeps working exactly as today.
func (h *RouteHandle[Req, Resp]) DecodeMerged(
    body []byte,
    pathVars, query, headers, cookies map[string]string,
) (Req, error)
```

`adapters/nethttp`/`adapters/chi`'s `Handler` internals call `DecodeMerged`
instead of `Decode` + separate `Validate*` calls whenever
`len(handle.MergeFields()) > 0` — identical behavior (byte-for-byte) when a
route declares no merge-capable params, so this is backward compatible for
every existing route declaration.

## API surface

```go
package codex // codex/varfields.go

// DecodeVars decodes each named field in fields from vars into target,
// mutating only those fields — any other fields already set on *target are
// left untouched. This is a PARTIAL merge, unlike [Struct]'s Decode, which
// builds an entirely new T from one JSON object.
//
// fields are declared with the SAME [RequiredField]/[OptionalField]/
// [DefaultField] constructors already used for [Struct] — no new
// declaration API. A field's Codec must accept a string value on Decode
// (e.g. codex.String()... or codex.MapCodecSafe(codex.String()..., ...)
// for a typed field like int or time.Time) since vars is always
// string-keyed/string-valued (path segments, topic segments, header/query/
// cookie values, and file path segments are all strings at the wire level).
//
// [RequiredField] vars that are absent from vars return
// [ValidationErrors] containing [ErrMissingField]; [OptionalField]/
// [DefaultField] vars that are absent are skipped/defaulted exactly as in
// [Struct]. Codec validation failures are collected the same way —
// DecodeVars never stops at the first error; every field is attempted, and
// every failure is reported.
//
//	var req GetUserReq
//	err := codex.DecodeVars(&req, map[string]string{"id": r.PathValue("id")},
//	    codex.RequiredField("id", codex.String().Refine(validate.UUID),
//	        func(r GetUserReq) string { return r.ID },
//	        func(r *GetUserReq, v string) { r.ID = v }))
//
// fields is typed [FieldCodec][T] — the exported name for the interface
// [Struct] itself uses internally (renamed from the previously-unexported
// fieldCodec[T] so other packages, e.g. api/rest's MergedPathParam, can
// name the type in their own signatures; see "Unifying the declaration"
// above).
func DecodeVars[T any](target *T, vars map[string]string, fields ...FieldCodec[T]) error

// EncodeVars extracts each named field in fields from v using its Get
// function and Codec, producing a map[string]string. This replaces
// hand-written varsFor func(T) map[string]string closures used by every
// adapter's SinkAdapter/IOAdapter/SourceAdapter constructor
// (adapters/file, adapters/redis, adapters/mqtt, adapters/mqtt5,
// adapters/zeromq) — call it FROM inside the closure the adapter expects:
//
//	varsFor := func(r SensorReading) map[string]string {
//	    return codex.Must(codex.EncodeVars(r, sensorIDField))
//	}
//
// Returns [VarEncodeTypeError] if any field's Codec.Encode does not
// produce a string — a caller programming error (an unsuitable codec was
// attached to a var field), not a runtime data error.
func EncodeVars[T any](v T, fields ...FieldCodec[T]) (map[string]string, error)
```

```go
package ports // ports/file.go — new method on the existing File[T] type

// MatchPath is the inverse of [File.BuildPath]: it matches a concrete,
// already-discovered file path (e.g. from the caller's own
// filepath.WalkDir/filepath.Glob) against the File's path template and
// returns the extracted variable values, validated against each
// registered [FilePathParam.Codec] — mirrors [mqtt.TopicVarsFromMessage]'s
// existing pattern for MQTT topics.
//
// Returns [FilePathMismatchError] if path does not match the template's
// structure (wrong number of segments, or a literal segment does not
// match). Returns [FilePathParamError] if an extracted variable fails its
// registered codec.
//
//	vars, err := readingFile.MatchPath("readings/sensor-42/2024-01-15.json")
//	// vars == map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"}
//	var reading ReadingMeta
//	err = codex.DecodeVars(&reading, vars, sensorIDField, dateField)
func (fh File[T]) MatchPath(path string) (map[string]string, error)
```

```go
package internal // api/internal/template.go — new shared helper

// MatchTemplate matches a concrete path/topic string against a template
// containing {varName} placeholders and literal segments, returning the
// extracted variable values. Segments are split on "/". No wildcard
// support ({+}/{#}-style) — MQTT's matchTopicTemplate (unexported, in
// adapters/mqtt/topicvars.go) keeps its own wildcard handling and may
// delegate to this for the non-wildcard segment-matching core.
//
// Returns wrapMismatch(template, concrete) when the segment count differs
// or a literal segment does not match.
func MatchTemplate(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error)
```

## Usage sketch — the motivating end-to-end scenario

```go
// Domain: file body carries only Value; sensorID and date are ENCODED IN
// THE FILENAME ("readings/{sensorID}/{date}.json") — exactly the user's
// stated scenario.
type ReadingMeta struct {
    SensorID string
    Date     string // codex.String().Refine(validate.Date) — kept as string here for simplicity
    Value    float64
}

// NewFilePathParam declares the FilePathParam (spec/validation, unchanged
// behavior) AND a merge field (for MergeFields()/DecodeVars) from ONE call
// — the elevated, closed-loop declaration; the low-level equivalent
// (separate FilePathParam{...}.WithCodec(...) + codex.RequiredField(...))
// still works too, shown further below.
var readingFile = ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(valueOnlyCodec),
    ports.NewFilePathParam("sensorID", codex.String().Refine(validate.NonEmptyString),
        func(r ReadingMeta) string { return r.SensorID },
        func(r *ReadingMeta, v string) { r.SensorID = v }),
    ports.NewFilePathParam("date", codex.String().Refine(validate.Date),
        func(r ReadingMeta) string { return r.Date },
        func(r *ReadingMeta, v string) { r.Date = v }),
)

// Caller's own directory scan (go-codex stays discovery-agnostic):
for _, path := range discoveredPaths {
    vars, err := readingFile.MatchPath(path) // NEW — parses the filename declaratively
    if err != nil { ... }

    var meta ReadingMeta
    if err := codex.DecodeVars(&meta, vars, readingFile.MergeFields()...); err != nil { // NEW — typed, validated merge, fields from the SAME declaration above
        ...
    }

    content, err := readingFile.Read(vars, ports.FileOptions{}) // EXISTING — reads the body
    if err != nil { ... }
    meta.Value = content.Value // remaining field from the body — distinct struct shapes, manual merge stays explicit
}
```

The low-level primitive (separate `FilePathParam` + `codex.RequiredField`
declarations, no `NewFilePathParam` sugar) remains fully supported for
callers who want the spec Param and the merge field to have independently
different names/codecs, or who are validate-only and never call
`DecodeVars` at all:

```go
var sensorIDField = codex.RequiredField("sensorID", codex.String().Refine(validate.NonEmptyString),
    func(r ReadingMeta) string { return r.SensorID },
    func(r *ReadingMeta, v string) { r.SensorID = v })

var readingFile = ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(valueOnlyCodec),
    ports.FilePathParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
    ports.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
)

vars, _ := readingFile.MatchPath(path)
var meta ReadingMeta
_ = codex.DecodeVars(&meta, vars, sensorIDField) // only sensorID merged; date left unused here
```

For REST, the CLOSED-LOOP story (declare once via `NewPathParam`, decode
fully-merged in one call — no separate `DecodeVars` call needed by the
user, and no adapter changes needed at the call site since
`nethttp.Handler`/`chi` invoke `DecodeMerged` internally):

```go
type GetUserReq struct{ ID string }

var getUser = rest.NewRoute[GetUserReq, User]("GET", "/users/{id}", reqCodec, userCodec,
    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
        func(r GetUserReq) string { return r.ID },
        func(r *GetUserReq, v string) { r.ID = v },
    ),
)
handle, _ := getUser.Register(builder)

// examples/adapters-nethttp/main.go's makeGetUserHandler, TODAY:
//   r, _ := nethttp.RequestFromContext(ctx)
//   id := r.PathValue("id") // no codec, no typed field, no validation
//
// AFTER this feature ships, the handler function receives an ALREADY
// fully-merged, fully-validated req — nethttp.Handler calls DecodeMerged
// internally because handle.MergeFields() is non-empty:
func(ctx context.Context, req GetUserReq) (User, error) {
    record, ok := store.Get(req.ID) // req.ID already validated as a UUID
    ...
}
```

Without `NewPathParam` (i.e. using the plain, unchanged `PathParam` for
validate-only routes that don't want automatic merge), `codex.DecodeVars`
remains directly usable exactly as shown in the File example above — the
low-level primitive is always available even where the per-boundary sugar
isn't used:

```go
vars := map[string]string{"id": r.PathValue("id")}
var req GetUserReq
err := codex.DecodeVars(&req, vars, idField)
```

## Structured errors (all implement `slog.LogValuer`)

`DecodeVars` reuses `codex.ValidationErrors`/`ValidationError` verbatim — no
new type. Two new types, for the two genuinely new failure modes:

```go
// VarEncodeTypeError is returned by EncodeVars when a field's Codec.Encode
// does not produce a string value — attaching an unsuitable codec (e.g.
// codex.Int() directly, instead of a string-wire-wrapped codec) to a var
// field is a caller programming error, not a runtime data error.
type VarEncodeTypeError struct {
    Field string
    Got   string // fmt.Sprintf("%T", val)
}

func (e VarEncodeTypeError) Error() string {
    return fmt.Sprintf("codex: var field %q: Codec.Encode must produce a string, got %s", e.Field, e.Got)
}

func (e VarEncodeTypeError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("field", e.Field), slog.String("got", e.Got))
}

// FilePathMismatchError is returned by File.MatchPath when a concrete path
// does not match the template's structure. Mirrors mqtt.TopicMismatchError
// exactly (same fields, same rationale).
type FilePathMismatchError struct {
    Template string
    Path     string
}

func (e FilePathMismatchError) Error() string {
    return fmt.Sprintf("file path %q does not match template %q", e.Path, e.Template)
}

func (e FilePathMismatchError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("template", e.Template), slog.String("path", e.Path))
}
```

No `Unwrap()` on either — both are self-contained structural mismatches, no
wrapped cause (same rationale as `codex.InvalidColorError`,
`mqtt.TopicMismatchError`).

## Observer integration

None for `codex.DecodeVars`/`EncodeVars` — pure functions with no `ctx`, no
adapter boundary, matching `codex.Struct.Encode`/`Decode`'s existing
observer-free precedent (observation happens one layer up via
`stats.ReportErrors(obs, location, err)` after the call, exactly like every
other codec-level function). `ports.File.MatchPath` also has no observer —
it does no I/O (pure string matching, like `BuildPath`/`ValidatePathVars`,
neither of which have an observer today).

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| D1 | `DecodeVars` happy path, `RequiredField` | vars decoded and set onto target |
| D2 | `DecodeVars` partial merge | fields NOT listed in `fields...` are left untouched on target |
| D3 | `DecodeVars` missing required var | `ValidationErrors` containing the field name + `ErrMissingField` |
| D4 | `DecodeVars` codec validation failure | `ValidationErrors` with the field's constraint error, not a panic |
| D5 | `DecodeVars` `OptionalField`/`DefaultField` absent | no error; default applied or field left zero, matching `Struct.Decode` semantics |
| D6 | `DecodeVars` multiple fields, multiple failures | all failures collected, not just the first (matches `Struct.Decode`) |
| E1 | `EncodeVars` happy path | map built from Get functions + codecs |
| E2 | `EncodeVars` non-string codec | `VarEncodeTypeError{Field, Got}` |
| E3 | `EncodeVars`/`DecodeVars` round-trip | encode then decode reproduces the original field values |
| E4 | `VarEncodeTypeError.LogValue` | `slog.KindGroup` + keys `field`/`got` |
| M1 | `File.MatchPath` happy path | vars extracted matching `BuildPath`'s forward direction |
| M2 | `File.MatchPath` segment count mismatch | `FilePathMismatchError` |
| M3 | `File.MatchPath` literal segment mismatch | `FilePathMismatchError` |
| M4 | `File.MatchPath` extracted var fails codec | `FilePathParamError` (existing type, reused) |
| M5 | `File.MatchPath`/`BuildPath` round-trip | `BuildPath(MatchPath(path))` reproduces `path` |
| M6 | `FilePathMismatchError.LogValue` | `slog.KindGroup` + keys `template`/`path` |
| T1 | `api/internal.MatchTemplate` happy path | literal + `{var}` segment matching, no wildcards |
| T2 | `api/internal.MatchTemplate` mismatch | `wrapMismatch` callback invoked with template/concrete |
| — | `ExampleDecodeVars` / `ExampleEncodeVars` / `ExampleFile_MatchPath` | deterministic, doc-quality examples |
| P1 | `rest.NewPathParam` registers both spec Param and merge field | `RouteHandle.Descriptor.PathParams` unchanged from plain `PathParam`; `RouteHandle.MergeFields()` contains the field |
| P2 | `RouteHandle.DecodeMerged` happy path | body decoded + path/query/header/cookie vars merged into one Req, all in one call |
| P3 | `RouteHandle.DecodeMerged` merge failure | `ValidationErrors` surfaced from the merge step, body-decoded fields still populated |
| P4 | `RouteHandle.DecodeMerged` with zero merge fields registered | behaves identically to plain `Decode` (no merge step, no behavior change) |
| P5 | `nethttp.Handler`/`chi` `Handler` route WITH merge fields | handler function receives a fully-merged, validated Req; no manual `r.PathValue` needed |
| P6 | `nethttp.Handler`/`chi` `Handler` route WITHOUT merge fields | byte-for-byte identical behavior to today (regression guard — confirms backward compatibility) |
| P7 | `events.NewTopicParam[T]` / `reqreply.NewTopicParam[T]` / `ports.NewFilePathParam[T]` / `ports.NewCacheKeyParam[T]` | same "registers both spec Param and merge field" pattern as P1, one test per boundary |
| P8 | `codex.FieldCodec[T]` export | compile-time only — confirms `codex.RequiredField(...)` remains assignable to `codex.FieldCodec[T]` and existing `Struct[T](fields ...FieldCodec[T])` callers are unaffected by the rename |
| P9 | `MergedPathParam.WithDescription` | sets the PARAM-level description; `RouteHandle.Descriptor.PathParams[i].Description` reflects it, distinct from the codec's `Schema.Description` |
| P10 | Mixing `NewPathParam` (merge-capable) and plain `PathParam` (validate-only) on the same route | both register correctly; `MergeFields()` contains only the `NewPathParam` one; `DecodeMerged` doesn't error on the validate-only one |

## Files to create

| File | Responsibility |
|---|---|
| `codex/varfields.go` | `DecodeVars`, `EncodeVars`, `VarEncodeTypeError`; rename unexported `fieldCodec[T]` → exported `FieldCodec[T]` (mechanical, in `codex/object.go`) |
| `codex/object.go` | rename `fieldCodec[T]` → `FieldCodec[T]`; update `Struct[T](fields ...FieldCodec[T])`'s signature (mechanical, zero behavior change) |
| `codex/varfields_test.go` | D1–D6, E1–E4 + Examples |
| `api/internal/template.go` | add `MatchTemplate` alongside existing `ParseTemplateVars`/`BuildFromTemplate`/`StripTemplateVars` |
| `api/internal/template_test.go` | T1–T2 (new tests added to the existing file) |
| `ports/file.go` | add `File.MatchPath`, `FilePathMismatchError`, `NewFilePathParam[T]`, `MergedFilePathParam[T]`, `File.MergeFields()` |
| `ports/file_test.go` | M1–M6 + `ExampleFile_MatchPath` + P7 (file case) |
| `ports/cache.go` | add `NewCacheKeyParam[T]`, `MergedCacheKeyParam[T]`, `Cache.MergeFields()` |
| `ports/cache_test.go` | P7 (cache case) |
| `api/rest/builder.go` | add `NewPathParam[T]`, `NewRequiredQueryParam[T]`/`NewOptionalQueryParam[T]` (+ Header/Cookie), `MergedPathParam[T]`/etc., `RouteHandle.MergeFields()`, `RouteHandle.DecodeMerged` |
| `api/rest/builder_test.go` | P1–P4, P8–P10 |
| `api/events/builder.go` | add `NewTopicParam[T]`, `MergedTopicParam[T]`, `ChannelHandle.MergeFields()` |
| `api/events/builder_test.go` | P7 (events case) |
| `api/reqreply/route.go` | add `NewTopicParam[T]`, `MergedTopicParam[T]`, `RouteHandle.MergeFields()` |
| `api/reqreply/route_test.go` | P7 (reqreply case) |
| `adapters/nethttp/adapter.go` | `Handler` calls `DecodeMerged` when `handle.MergeFields()` is non-empty |
| `adapters/chi/adapter.go` | same change, chi's path-value extraction (`chi.URLParam`) |
| `adapters/nethttp/adapter_test.go`, `adapters/chi/adapter_test.go` | P5–P6 |
| `adapters/mqtt/topicvars.go` | (optional, see Open design decision 1) refactor `matchTopicTemplate` to delegate its non-wildcard segment matching to `internal.MatchTemplate` |
| `docs/features/ports.md` | new subsection under File: "Extracting information from a discovered path" (`MatchPath`) + "Declaring path params with automatic merge" (`NewFilePathParam`) |
| `docs/features/rest-api.md` | new subsection: "Path/query/header params with automatic merge" showing `NewPathParam`/`DecodeMerged` replacing manual `r.PathValue()` extraction |
| `docs/guides/http-server.md` | update the path-param section to show `rest.NewPathParam` + automatic merge as the recommended pattern, with plain `PathParam` kept as the validate-only alternative |
| `docs/features/config.md` | update "Config file + env var overrides" to replace the hand-rolled `os.Getenv`+`strconv.Atoi`+manual-assignment recipe with `codex.DecodeVars` — the fifth beneficiary found by inspection, needs no new `config`-specific code |
| `docs/concepts/codec.md` | new subsection near "Struct codecs": "Reusing Field declarations for path/topic/header vars" — covers both the low-level `codex.DecodeVars`/`EncodeVars` primitive and the per-boundary `NewXxxParam` sugar |
| `.github/instructions/go-codex.instructions.md` | new subsection documenting `DecodeVars`/`EncodeVars`/`FieldCodec[T]` under the `codex` package entry; `NewPathParam`/etc. + `MergeFields`/`DecodeMerged` under `api/rest`/`api/events`/`api/reqreply`/`ports` entries |
| `examples/adapters-nethttp/main.go` | update `makeGetUserHandler` to use `rest.NewPathParam` + rely on automatic `DecodeMerged` instead of `r.PathValue("id")` |
| `examples/adapters-chi/main.go` | same update for the chi-specific `chi.URLParam(r, "id")` path |
| `examples/sensor-service/main.go` | update the `r.PathValue("sensorID")` comment/call site — this example's existing comment ("already codec-validated by the RESTPattern's PathParam") is the ideal place to show the FULL merge story, not just validation |
| `examples/api-rest/main.go` | update the path-parameter comment/handling to reference the new pattern |
| `examples/file-io/main.go` (or a new small example) | demonstrate filename-encoded metadata via `MatchPath` + `DecodeVars`, the original motivating scenario |

## Out of scope (Phase 2)

- **Applying `MergeFields()`/`DecodeMerged`-style automatic merge to
  `events`/`reqreply`/`ports.Cache` adapters in Phase 1** — the accessor
  pattern and constructors (`NewTopicParam[T]`, `NewFilePathParam[T]`,
  `NewCacheKeyParam[T]`) ship in Phase 1 for ALL boundaries (declaration is
  cheap and mechanical), but WIRING automatic merge into
  `adapters/mqtt5`/`adapters/zeromq`/`adapters/redis`'s handler/subscribe
  paths is deferred — REST (`nethttp`/`chi`) is the highest-value, most
  explicitly requested surface ("for rest routes in header fields, query
  parameters"). Users can still call `codex.DecodeVars` themselves with
  `ChannelHandle.MergeFields()`/`File.MergeFields()` in the meantime — the
  low-level primitive works everywhere from day one, only the
  "adapter-does-it-for-you" polish is sequenced later.
- **Directory scanning / glob / filesystem watching** — `MatchPath` consumes
  a path the caller already has; go-codex adds no `filepath.WalkDir`
  wrapper, no `fsnotify` integration. Deliberately transport/discovery-agnostic,
  matching the existing `ports.File` design (declarative descriptor, no
  owned I/O beyond Read/Write/Update).
- **`EncodeVars`/`DecodeVars` support for non-string leaf types without a
  wrapping codec** — e.g. accepting `codex.Int()` directly and having
  `DecodeVars` auto-`strconv.Atoi` the string for you. Phase 1 requires the
  user to wrap non-string field types via `codex.MapCodecSafe(codex.String()...,
  parse, format)` explicitly — auto-coercion would silently reintroduce the
  "guess the format" ambiguity `config.FromEnv`'s schema-driven coercion
  works hard to avoid elsewhere. Revisit only if the manual wrap proves to
  be a common pain point.

## Open design decisions (to resolve before/during implementation)

1. **Should `adapters/mqtt/topicvars.go`'s `matchTopicTemplate` actually be
   refactored to delegate to the new shared `api/internal.MatchTemplate`,
   or left as-is (duplicated) since it already works and has its own
   wildcard-handling that complicates a clean extraction?** Leaning: yes,
   refactor — but treat it as a small, separate, low-risk follow-up commit
   AFTER `File.MatchPath` ships and proves the shared helper's shape is
   right; don't block Phase 1 on getting the MQTT wildcard/shared-core split
   perfectly right up front.
2. **Where should `File.MatchPath`'s round-trip guarantee
   (`BuildPath(MatchPath(path)) == path`) be documented/enforced?** A
   godoc-only guarantee (Phase 1 lean), or a property-based test
   (`quick.Check`-style) verifying it holds for generated templates? Leaning:
   godoc + example-based unit test (M5) is sufficient for Phase 1; a
   property-based test is a natural pairing with the
   [Fuzz & Benchmark Testing](fuzz-benchmark-testing.md) roadmap item
   instead of being invented here.
3. **Naming**: `DecodeVars`/`EncodeVars` vs. alternatives considered
   (`MergeVars`/`ExtractVars`, `ApplyVars`/`CollectVars`). Leaning: keep
   `DecodeVars`/`EncodeVars` — mirrors `Codec[T]`'s own `Decode`/`Encode`
   vocabulary directly, signaling "this is the same operation, just against
   a `map[string]string` instead of `any`." The per-boundary constructors
   (`NewPathParam`, `NewRequiredQueryParam`/`NewOptionalQueryParam`, etc.)
   follow `RequiredField`/`OptionalField`/`DefaultField`'s existing
   Required/Optional/Default naming split — no separate decision needed
   there, it's a direct application of an established convention.
4. ~~Should `MergedPathParam[T]` (and its siblings) be the type users see
   in godoc/errors, or should it stay an internal implementation detail~~
   — **RESOLVED**: exported. Confirmed necessary (not just preferred) once
   the `WithDescription` chain method was added — `NewPathParam(...).WithDescription(...)`
   requires `NewPathParam` to return a concrete type with a discoverable
   method, not an opaque `RouteOpt` interface value. Also mirrors how
   `codex.Field[T,F]` itself is exported, not hidden behind an interface,
   and is consistent with the rest of the codebase's preference for
   concrete, inspectable types over interface-only surfaces.
5. **`DecodeMerged`'s error type when BOTH body decode and var merge fail**
   — should it return a combined `ValidationErrors` (flattening both
   sources into one slice, losing which failure came from the body vs. a
   param) or a new wrapper distinguishing the two? Leaning: return the
   FIRST failure encountered (body decode, if the route has a body, then
   var merge) rather than combining — matches `RouteHandle.Decode`'s
   existing "stop at first structural failure" behavior for body decode,
   while `DecodeVars`'s OWN internal per-field collection (all fields
   attempted) is preserved for the merge step specifically. Revisit if a
   real use case wants every possible failure across both sources reported
   at once.
6. **Should `File.MergeFields()`/`ChannelHandle.MergeFields()` also gain
   their OWN `DecodeMerged`-equivalent convenience method (analogous to
   `RouteHandle.DecodeMerged`), or is the low-level
   `codex.DecodeVars(&meta, vars, handle.MergeFields()...)` call clear
   enough for those boundaries where there's no "body vs. vars" split to
   coordinate (a file's body and a topic's payload are ALREADY separate
   decode calls today, unlike REST where `Decode` and param validation
   happen in the same `Handler` call)?** Leaning: no — `RouteHandle`
   uniquely benefits from a combined method because `nethttp.Handler`
   already does body decode AND param validation in one pass; File/Channel
   readers already call `Read`/decode-payload and merge-vars as two
   separate, already-well-understood steps, so a combined method would add
   a new method without removing any existing complexity.
