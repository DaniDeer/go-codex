# Drop `github.com/google/uuid` for the new stdlib `uuid` package — `codex`

> **Status:** Idea / investigation — Explore mode. No code changes yet.
> [← Back to Roadmap](index.md)
>
> Spun out of a coverage review of
> [Go 1.27 Generic Methods](go127-generic-methods.md), which also
> surveyed this toolchain's actual `api/go1.27.txt` for other relevant
> stdlib changes beyond generic methods.

## The idea

Go 1.27 adds a new stdlib `uuid` package (`uuid.New()`, `uuid.NewV4()`,
`uuid.NewV7()`, `uuid.Parse`, `uuid.MustParse`, `(UUID) String`/
`MarshalText`/`UnmarshalText`/`Compare`, etc. — confirmed directly
against this toolchain's `$(go env GOROOT)/api/go1.27.txt`). This
codebase already depends on the external `github.com/google/uuid`
module (`go.mod` — confirmed), used in:

- `codex/stringcodec.go` (library code, not just an example)
- `examples/events-nested-binary/main.go`
- `examples/typed-params/main.go`
- `examples/rest-api/routes/models.go`
- `examples/rest-api/routes/routes.go`
- `examples/rest-api/demo_users_get.go`
- `adapters/mqtt5/reqreply_transport.go`
- `adapters/mqtt5/reqreply.go`

Swapping to the stdlib package would remove one external dependency
from `go.mod` entirely.

## Open questions (not yet resolved)

1. **API shape parity** — does the stdlib `uuid.UUID` type's method set
   (`String`, `MarshalText`/`UnmarshalText`, `Compare`) cover everything
   `codex/stringcodec.go`'s current usage of `github.com/google/uuid`
   needs, or does the external package also use something not yet
   present in the stdlib version (e.g. `uuid.NewMD5`/`NewSHA1`,
   `uuid.FromBytes`, a `Variant`/`Version` accessor)? Needs a side-by-side
   API diff before committing to the swap.
2. **Wire-format compatibility** — `String()`/`MarshalText()` output
   must be byte-for-byte identical to `github.com/google/uuid`'s, since
   `codex/stringcodec.go` is encoding/decoding on an existing wire
   contract; any existing stored/transmitted UUID values must still
   round-trip.
3. **Version constraint** — this is a stdlib-only win once the module's
   `go.mod` floor can assume `go1.27` is available everywhere it's
   built/vendored (already true here — `go.mod` already reads
   `go 1.27.2`), but any downstream consumer pinned to an older Go
   toolchain would need to keep using the external module. Confirm this
   is an acceptable breaking change for this repo's "single
   maintainer/user, breaking changes accepted" policy (same policy
   `go127-generic-methods.md` cites).
4. Should `github.com/google/uuid` be removed from `go.mod` entirely, or
   kept available for cases the stdlib type can't yet cover?

## Next step

A short audit comparing `github.com/google/uuid`'s actual call sites
above against the stdlib `uuid` package's method set, then — if parity
holds — a mechanical swap (package import + call-site rename) and a
`go.mod`/`go.sum` tidy to drop the external dependency.
