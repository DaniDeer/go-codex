# MQTT5 User Property Merge — `adapters/mqtt5`

> **Status:** Idea only — no driver yet. Independent of
> [Declarative Middleware](declarative-middleware.md) (no sequencing
> dependency either way — could ship before, after, or in parallel).
> **Also independent of `reqreply-middleware.md`'s now-SHIPPED Phase 1b**
> (`mqtt5.FromUserPropertyParam`/`FromResponseUserPropertyParam` — that
> roadmap doc has since shipped and been deleted per its own graduation
> policy; see [D-0004](../design/d-0004-reqreply-workflow-simplification.md)'s
> own Addendum for the durable record) —
> checked when Phase 1b landed: that mechanism bridges a plain
> `UserPropertyParam` into `middleware.Middleware` for `.Use()`
> attachment (spec rendering + Attach-time VALIDATION only, no merge);
> THIS doc's `MergedUserPropertyParam[T]` is a separate, direct
> `ChannelOpt`/`RouteOpt`-equivalent that ALSO auto-**merges** the value
> into the decoded struct. Same non-conflicting relationship REST's own
> `rest.FromHeaderParam` and `rest.MergedHeaderParam`/
> `NewRequiredHeaderParam` already have (embedding lets a caller compose
> both for the SAME property once this doc ships) — no naming collision,
> no functional overlap, no sequencing dependency either way.
> **This doc's own "registration surface... NOT resolved" open question
> (below) is now ANSWERED by
> [Feature](protocol-native-features.md)** (formerly
> "Protocol-Native Feature Declarations", then "Feature/Provider",
> significantly grown in scope) — that doc's §5.2 works through User
> Properties as a concrete, SEALED `mqtt5.Capability` instance (carrying
> an embedded `middleware.Declaration[In,Out]` for its merge-capable
> half, supplied at `mqtt5.Attach` time) instead of a plain `ChannelOpt`,
> resolving this doc's own open question as part of that doc's broader
> redesign. Still not IMPLEMENTED — this doc's own Scope/API sections
> below remain the most concrete existing sketch until that broader
> design is itself
> implemented.
> **A THIRD point on this SAME spectrum now exists, ALREADY SHIPPED** —
> [D-0003](../design/d-0003-codec-declared-middlewares.md)'s own Addendum
> (folded in from the now-deleted `reqreply-codec-declared-middleware.md`
> roadmap doc) covers the
> new "property" vocabulary axis (`WithRequestProperty`/
> `WithResponseProperty` for `api/reqreply`, `WithSubscribeProperty`/
> `WithPublishProperty` for `api/events`) reuses D-0003's ALREADY-SHIPPED
> `Middleware[In,Out]` mechanism directly — no new core primitive needed,
> unlike `protocol-native-features.md`'s own `Capability` redesign, which
> this doc's own resolution above still depends on. Scoped to
> `api/reqreply`/`api/events` specifically (NOT a general `mqtt`(v3)-
> agnostic `ChannelOpt`/`RouteOpt` the way THIS doc's own
> `MergedUserPropertyParam[T]` is designed to be) — so it does NOT
> replace this doc's own planned work, but IS a real, ALREADY-SHIPPED
> alternative for callers who only need the reqreply/events cases
> specifically. See
> [Feature: ReqReply Codec-Declared Middleware](../features/reqreply-middleware.md)
> for the user-facing docs, or that doc's own "The 'property' vocabulary
> axis" section for the full design record (19 review rounds).
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
| `examples/events-api` | Demonstrate a merged User Property alongside `demo_user_property_middleware.go`'s existing validate-only example |
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
