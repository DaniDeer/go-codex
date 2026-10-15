// Package file provides a transport-agnostic File API layer applying the
// SAME three-layer model (declare -> attach backend -> dispatch) every
// other go-codex boundary (api/rest, api/events, api/reqreply) already
// uses, with file-shaped verbs (Read, Create, Patch, Delete, List,
// Exists, Copy, Move, Mkdir, BatchDelete) instead of GET/POST/PATCH/
// DELETE/GET-collection.
//
// A declared [Resource][T] carries a path template, a codec, and
// optional per-variable merge fields — exactly like
// [github.com/DaniDeer/go-codex/api/rest.Path]/
// [github.com/DaniDeer/go-codex/api/events.Topic]. Each verb is attached
// via its own role-builder method (Resource.WithRead, Resource.WithCopy,
// etc.), mirroring [github.com/DaniDeer/go-codex/api/events.Channel]'s
// asymmetric WithSubscribe/WithPublish pattern rather than
// [github.com/DaniDeer/go-codex/api/rest.Route]'s uniform Req->Resp
// shape, since file verbs are fundamentally non-uniform.
//
// The ultimate goal is true backend transparency: the SAME declared
// Resource[T] works unchanged whether the concrete storage is the local
// filesystem, S3, or an SMB share, with the backend chosen entirely at
// [Client.Attach] time, never baked into the declaration. A [Backend]
// implementation carries ONLY the byte-level, backend-specific
// operations (ReadBytes/WriteBytes/DeleteBytes/ListDir/Stat/Mkdir); the
// shared dispatch mechanics (path templating, codec encode/decode,
// merge-field application, Observer calls, ErrorPattern dispatch) live
// once in this package's own dispatch core.
//
// [Client] is the attach-time wiring (Client.Attach, exclusive) and
// call-time dispatch (Client.Read/Create/Patch/Delete/List/Exists/Copy/
// Move/Mkdir/BatchDelete) surface, mirroring
// [github.com/DaniDeer/go-codex/api/events.Client]/
// [github.com/DaniDeer/go-codex/api/reqreply.Client]'s established
// Attach+reflective-any-dispatch shape: every dispatch method accepts
// EITHER the bare role builder (the common case) or an explicitly
// pre-built *XxxHandle[T] (the Router-grouped/extra-options case).
//
// See docs/roadmap/file-api.md for the full design rationale, including
// the cross-backend [Capability] permission model (AccessMode,
// NativePOSIXMode) and documented backend semantic differences.
package file
