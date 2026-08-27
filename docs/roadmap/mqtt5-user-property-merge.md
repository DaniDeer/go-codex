# MQTT5 User Property Merge — `adapters/mqtt5`

> **Status:** Idea only — no driver yet. Independent of
> [Declarative Middleware](declarative-middleware.md) (no sequencing
> dependency either way — could ship before, after, or in parallel).
> [← Back to Roadmap](index.md)

## Motivation

`adapters/mqtt5.UserPropertyParam` is VALIDATE-ONLY — its own doc comment
says it "mirrors `rest.HeaderParam`", but only the VALIDATE-ONLY half of
that comparison: REST also has a MERGE-CAPABLE sibling
(`rest.MergedHeaderParam[T]`/`NewRequiredHeaderParam`/
`NewOptionalHeaderParam`) that BOTH validates a header AND automatically
merges it into the decoded `Req` struct via `RouteHandle.DecodeMerged`.
MQTT5 User Properties have no such sibling — a caller who wants a User
Property's value available on the decoded message struct (not just
validated) must currently read it out of the raw `*pahomqtt5.Publish`
by hand, breaking the "declare once" promise every other var boundary
in this library already keeps (`rest.NewRequiredHeaderParam`,
`events.NewTopicParam`, `ports.NewFilePathParam` — see
`docs/concepts/api-contracts.md`'s one-struct-one-call principle).

This gap is INDEPENDENT of whether `adapters/mqtt5`'s `SecurityFunc`
becomes a `middleware.Middleware` (see
[Declarative Middleware](declarative-middleware.md)) — it exists today,
regardless of that design's outcome, and would exist even if
`SecurityFunc` were never touched at all.

## Scope decision

Mirror REST's EXACT naming/shape pattern — `UserPropertyParam` stays
unchanged (validate-only escape hatch, same as `rest.HeaderParam`
remains available unchanged); a NEW `MergedUserPropertyParam[T]` sibling
is ADDED, following the identical constructor-pair convention:

```go
package mqtt5

// MergedUserPropertyParam is returned by [NewRequiredUserPropertyParam]/
// [NewOptionalUserPropertyParam]. Mirrors [rest.MergedHeaderParam]'s
// exact shape and rationale — see there for the full pattern this
// follows.
type MergedUserPropertyParam[T any] struct {
    UserPropertyParam
    field codex.FieldCodec[T]
}

// NewRequiredUserPropertyParam declares a REQUIRED User Property that is
// BOTH validated against codec AND automatically merged into the decoded
// message value — mirrors [rest.NewRequiredHeaderParam] exactly.
//
// V need not be string — see [codex.NewParam] for merging a property
// value directly into an int/UUID/etc.
func NewRequiredUserPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedUserPropertyParam[T] {
    strCodec := codex.StringValidatorFrom(codec)
    return MergedUserPropertyParam[T]{
        UserPropertyParam: UserPropertyParam{Name: name, Codec: &strCodec, Required: true},
        field:             codex.RequiredField(name, codec, get, set),
    }
}

// NewOptionalUserPropertyParam declares an OPTIONAL User Property that is
// BOTH validated (when present) AND automatically merged (when present) —
// mirrors [rest.NewOptionalHeaderParam] exactly.
func NewOptionalUserPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedUserPropertyParam[T] {
    strCodec := codex.StringValidatorFrom(codec)
    return MergedUserPropertyParam[T]{
        UserPropertyParam: UserPropertyParam{Name: name, Codec: &strCodec, Required: false},
        field:             codex.OptionalField(name, codec, get, set),
    }
}
```

## Where it plugs into the existing merge pipeline

`adapters/mqtt5`'s `makeSubscribeMessageHandler` already has the EXACT
call site this needs: it merges topic variables into the decoded value
via `codex.DecodeVars(&value, vars, mergeFields...)`, BEFORE
`UserPropertyParams` validation runs. `MergedUserPropertyParam[T]`'s
`field` simply needs to reach the SAME `mergeFields` slice
`handle.MergeFields()` already returns — the merge pipeline itself
requires NO new mechanism, only a new SOURCE of merge-capable params
alongside `events.NewTopicParam`'s existing contribution:

```go
// Existing call site (adapters/mqtt5, unchanged shape):
if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
    vars, _ := TopicVarsFromMessage(handle, msg)
    // NEW: also collect registered User Property values into vars,
    // using the SAME name-keyed map merge semantics topic vars already
    // use — MergedUserPropertyParam's field is registered via
    // SubscribeOptions.UserPropertyParams (or an events.ChannelOpt
    // equivalent), read from msg's raw properties, added to vars
    // BEFORE the SAME codex.DecodeVars(&value, vars, mergeFields...) call.
    if mergeErr := codex.DecodeVars(&value, vars, mergeFields...); mergeErr != nil { ... }
}
```

The exact registration surface (a new `events.ChannelOpt`, or a
`SubscribeOptions`/`ServeOptions` field alongside today's
`UserPropertyParams []UserPropertyParam`) is NOT resolved here — this
doc captures the API surface and pipeline integration point; the
registration ergonomics need one more design pass before implementation,
following the SAME "declare once, register with the channel/route"
convention `events.NewTopicParam` already established.

## Files to create/modify (not yet scoped into phases)

| File | Change |
|---|---|
| `adapters/mqtt5/adapter.go` | `MergedUserPropertyParam[T]`, `NewRequiredUserPropertyParam`, `NewOptionalUserPropertyParam`; `makeSubscribeMessageHandler` collects merge-capable User Property values into the SAME `vars` map topic vars already populate |
| `adapters/mqtt5/adapter_test.go` | Construction + merge tests, mirroring `rest.NewRequiredHeaderParam`'s test shape |
| `examples/adapters-mqtt5` | Demonstrate a merged User Property alongside the existing validate-only example |
| `.github/instructions/go-codex.instructions.md` | New `adapters/mqtt5` row entries |

## See also

- [Declarative Middleware](declarative-middleware.md) — the "Cross-
  cutting concerns and one-struct-one-call" section there documents the
  SAME "declare once" principle this doc extends to User Properties;
  independent design, no sequencing dependency.
- `docs/concepts/api-contracts.md` — the one-struct-one-call principle
  this doc closes a gap in.
- `rest.MergedHeaderParam`/`NewRequiredHeaderParam` (`api/rest/builder.go`) —
  the exact shape this doc mirrors.
