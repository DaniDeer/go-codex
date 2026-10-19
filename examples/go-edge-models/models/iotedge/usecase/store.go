package usecase

import (
	"errors"

	"github.com/DaniDeer/go-codex/api/file"
)

// Store is the receiver-style, Stage D2 replacement for this package's
// former free functions (NewFile/NewBaselineFile/NewDir/NewDeviceFile/
// NewDeviceDir and the Read/Write/List* convenience built on top of
// them) — holds the ONE [file.Client] every method below dispatches
// through. Mirrors
// [github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/internal.RegistryClient]'s
// own construction shape exactly: a caller supplies the raw ingredient
// (there, an *http.Client+host; here, a [file.Backend]) and NewStore
// builds+attaches the real Client internally, returning an error
// instead of requiring the caller to do that 2-step wiring themselves.
//
// Unlike RegistryClient (which fixes its own "host" at construction,
// since one registry client always talks to the same registry), Store
// does NOT fix a [BasePath] at construction — every method below takes
// basePath as its OWN parameter, since basePath varies per
// request/call in this package's real callers (ultimately an MCP tool
// request field — see app/iotedge/usecase.go's own basePath-threading
// for the confirmed evidence). A fresh [file.Router] is built per call
// instead (via routerFor), mirroring
// [github.com/DaniDeer/go-codex/ports.Dir]'s own established Stage C2
// precedent (Dir.List/Dir.Delete build a fresh Router from d.baseDir on
// every call, never caching one).
type Store struct {
	client *file.Client
}

// NewStore constructs a Store backed by backend (e.g.
// [github.com/DaniDeer/go-codex/adapters/file.NewBackend]'s real
// filesystem backend, or a fake for tests) — builds a [file.Client]
// internally and attaches backend to it in one call, returning an
// error instead of panicking (mirrors
// [github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/internal.NewRegistryClient]'s
// own "returns an error instead of panicking" convention — EACH
// caller of NewStore decides whether to panic with its own
// distinctly-prefixed message, not this constructor).
func NewStore(backend file.Backend) (*Store, error) {
	s := &Store{client: file.NewClient()}
	if err := s.client.Attach(backend); err != nil {
		return nil, err
	}
	return s, nil
}

// routerFor builds a FRESH [file.Router] anchored at basePath — called
// by every method below, once per call, never cached (basePath varies
// per call — see Store's own doc comment).
func routerFor(basePath BasePath) file.Router {
	return file.NewRouter(string(basePath))
}

// IsNotFound reports whether err is a "file does not exist" condition
// from any Store method's Read-shaped call — the Stage D2 replacement
// for the pre-migration errors.Is(err, os.ErrNotExist) check this
// package's (and app/iotedge's) callers used against ports.File.Read's
// OWN deliberate not-found-to-os.ErrNotExist translation (see
// Store.ReadEffective's own doc comment for the full history).
// [file.Client.Read] reports not-found as a bare [file.FileNotFoundError]
// with no wrapped cause — IsNotFound keeps that detail encapsulated
// behind this package's own API, so a caller never needs to import
// api/file directly just to ask "was this a not-found?".
func IsNotFound(err error) bool {
	var notFound file.FileNotFoundError
	return errors.As(err, &notFound)
}
