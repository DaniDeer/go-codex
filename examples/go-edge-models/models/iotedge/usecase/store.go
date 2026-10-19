package usecase

import (
	"errors"

	"github.com/DaniDeer/go-codex/api/file"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
)

// Store is the long-lived CONNECTION — holds the ONE [file.Client]
// every [Scope] built from it dispatches through. Mirrors
// [github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/internal.RegistryClient]'s
// own construction shape exactly: a caller supplies the raw ingredient
// (there, an *http.Client+host; here, a [file.Backend]) and NewStore
// builds+attaches the real Client internally, returning an error
// instead of requiring the caller to do that 2-step wiring themselves.
//
// Store itself does NOT fix a [BasePath] — basePath varies per
// request/call in this package's real callers (ultimately an MCP tool
// request field — see app/iotedge/usecase.go's own basePath-threading
// for the confirmed evidence), so a single Store, built ONCE per
// process (or once per long-lived MCP handler registration), is bound
// to a SPECIFIC basePath on demand via [Store.For] — see [Scope]'s own
// doc comment for the full Store/Scope split rationale.
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

// Scope is a [Store] BOUND TO ONE [BasePath] — built via [Store.For].
// Every method this package exposes (Read/Write/List*/Patch* and
// friends) is a Scope method (or a free function taking *Scope, for
// the 2 generic Patch* functions — see their own doc comments), NOT a
// Store method: basePath genuinely varies per request (a real MCP
// caller supplies a DIFFERENT basePath on every call — see Store's own
// doc comment), so a long-lived *Store is built ONCE (e.g. at
// process/handler-registration time) and bound to a specific basePath
// via Store.For ONCE PER REQUEST — cheap, pure in-memory wiring, no
// I/O of its own.
//
// Store.For EAGERLY builds and caches every non-generic [file.Handle]
// this package dispatches through (one [file.Router], built once from
// basePath, is shared by all of them) — confirmed safe because NONE of
// this package's declared [file.Resource]/[file.Dir] values require
// any [file.Capability], so a Handle build can never actually fail in
// practice; every method below is therefore just one
// `s.client.Verb(ctx, s.xHandle, vars, opts...)` call, with ZERO
// per-call Handle-building (replacing the earlier Stage D2 shape,
// which rebuilt a Router + a Handle on every single method call).
type Scope struct {
	client *file.Client

	// router is needed directly ONLY by the 2 generic free functions
	// ([PatchUseCaseFile]/[PatchDeviceFile]) — a Patch Handle can't be
	// cached here ahead of time, since its own type parameter P varies
	// per call (see those functions' own doc comments for why they
	// can't be *Scope methods at all).
	router file.Router

	readBaseline   *file.ReadHandle[iothub.BaseDeployment]
	createBaseline *file.CreateHandle[iothub.BaseDeployment]

	readUseCase   *file.ReadHandle[iothub.LayeredDeployment]
	createUseCase *file.CreateHandle[iothub.LayeredDeployment]
	listUseCases  *file.ListDirHandle

	readDevice   *file.ReadHandle[deviceconfig.Patch]
	createDevice *file.CreateHandle[deviceconfig.Patch]
	listDevices  *file.ListDirHandle
}

// For binds s to basePath, returning a [Scope] with every Handle this
// package needs eagerly built and cached — see Scope's own doc comment
// for why this is safe to do eagerly, and why it happens once per
// REQUEST rather than once per process (basePath varies per request).
func (s *Store) For(basePath BasePath) (*Scope, error) {
	rt := file.NewRouter(string(basePath))
	sc := &Scope{client: s.client, router: rt}

	var err error
	if sc.readBaseline, err = baselineFile.WithRead(file.Read{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.createBaseline, err = baselineFile.WithCreate(file.Create{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.readUseCase, err = fileResource.WithRead(file.Read{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.createUseCase, err = fileResource.WithCreate(file.Create{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.listUseCases, err = dirResource.WithList(file.ListDir{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.readDevice, err = deviceFileResource.WithRead(file.Read{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.createDevice, err = deviceFileResource.WithCreate(file.Create{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	if sc.listDevices, err = deviceDirResource.WithList(file.ListDir{}).Handle(s.client, file.WithRouter(rt)); err != nil {
		return nil, err
	}
	return sc, nil
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
