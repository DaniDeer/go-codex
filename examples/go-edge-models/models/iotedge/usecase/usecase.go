package usecase

import (
	"context"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	f "github.com/DaniDeer/go-codex/format"
)

// ── Use case model ────────────────────────────────────────────────────────────
//
// This file holds EVERYTHING that describes "what is a use case": its
// path pattern, its directory-entry filename shape, its name-validation
// codec, its file/dir declarations ([baselineFile]/[fileResource]/
// [dirResource]), and the domain-level composition ([UseCase]/
// [Store.Read]/[Store.Write]) that pairs a use case with the devices
// nested under it. Mirrors devicefile.go/devicedir.go/deviceconfig.go's
// device-level model one level down, consolidated into a single file
// since a use case is the package's primary aggregate root.
//
// Stage D2 (receiver-style migration to api/file.Client, see
// store.go's own doc comment): every former free function taking
// basePath as its own, per-call parameter (NewFile/NewBaselineFile/
// NewDir/Read/Write/ListNames) is now a [*Store] METHOD, taking the
// SAME basePath parameter — the only thing that changed is WHERE the
// method lives (on Store, dispatching through Store's own attached
// [file.Client]) and HOW the Router composes (Store.routerFor builds a
// FRESH [file.Router] from basePath on every call — see store.go). The
// underlying [file.Resource]/[file.Dir] VALUES below are now
// PACKAGE-LEVEL vars, declared ONCE with a RELATIVE path (no basePath
// baked in) — mirrors
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry]'s
// own GetTagsRoute/GetManifestRoute package-level-var convention
// (confirmed during Stage D2 planning as the precedent to mirror).

// See config.go for the filesystem-layout constants this file's
// declarations are built from (useCasesDirName, useCaseNameVar,
// useCasePathPattern, useCaseEntryShape, useCaseEntryVar) and for
// nameCodec (the raw Codec[string] validator used below) plus the
// exported [Name] type/[NameCodec]/[NewName] this file's Go-level API
// (UseCase.Name/Store.Read/Store.Write/Store.ListNames) is typed with.
// config.go is UNCHANGED by Stage D2 — every symbol there is pure
// codec/template logic with no ports.File/ports.Dir/api/file
// involvement at all.

// FileFormat is the declared, reusable format/codec pairing for an
// iotedge deployment manifest file — no I/O by itself; it exists so
// fileResource below and any caller building their own file.Resource
// don't have to repeat format.JSON(iothub.LayeredDeploymentCodec).
var FileFormat = f.JSON(iothub.LayeredDeploymentCodec)

// BaselineFileFormat mirrors FileFormat exactly, for the GLOBAL
// baseline.json file (see [baselineFile]).
var BaselineFileFormat = f.JSON(iothub.BaseDeploymentCodec)

// baselineFile is the package-level Resource declaration for the
// SINGLE GLOBAL baseline deployment file — "baseline/baseline.json",
// relative, no basePath baked in (Store.routerFor supplies basePath as
// a Router prefix per call). Unlike fileResource/the device-level
// resource, this path has NO template variables (no per-use-case or
// per-device variation — see the models/azure/iothub package's own doc
// comment for why): Store.ReadBaseline/WriteBaseline pass a nil vars
// map.
var baselineFile = file.NewResource[iothub.BaseDeployment](baselinePathPattern.String(), BaselineFileFormat)

// DirEntryPattern declares the filename SHAPE for iotedge config files
// inside a config directory: each file is one "use case", and the
// filename (minus ".json") IS that use case's name — e.g.
// "usecase1.json" is the "usecase1" use case's deployment manifest. A
// file that doesn't match this shape at all (a stray ".gitkeep"/README
// alongside the config files) is silently excluded by
// [Store.ListUseCaseEntries] — see [file.EntryPattern]'s own doc.
var DirEntryPattern = file.EntryPattern{
	Template: useCaseEntryShape.String(),
	Params:   []file.EntryParam{file.EntryParam{Name: useCaseEntryVar}.WithCodec(nameCodec)},
}

// fileResource is the package-level Resource declaration for a USE
// CASE's deployment manifest — "usecases/{usecase_name}.json",
// relative, no basePath baked in. usecase_name is validated against
// nameCodec (the SAME codec [DirEntryPattern] uses to validate a
// discovered use case's name) via a PLAIN (non-merge) [file.PathParam]
// — [iothub.LayeredDeployment] stays pure wire/file content;
// usecase_name is never merged into it.
//
// The declared roles built from this resource (WithRead/WithCreate/
// WithPatch) are reused for reading (Store.ReadUseCaseFile), writing
// (Store.WriteUseCaseFile), and patching ([PatchUseCaseFile]) — see
// those for the higher-level convenience that combines this with
// device discovery.
var fileResource = file.NewResource[iothub.LayeredDeployment](
	useCasePathPattern.String(), FileFormat,
	file.PathParam{Name: useCaseNameVar}.WithCodec(nameCodec),
)

// dirResource is the package-level Dir declaration for a directory of
// iotedge config files (deployment manifests), one per use case —
// "usecases", relative, no basePath baked in — mirrors [fileResource]'s
// shape exactly: a thin, pure (no I/O) declaration using
// [DirEntryPattern] so [Store.ListUseCaseEntries] discovers which use
// cases exist and extracts each one's name, the same declarative way
// [fileResource]'s caller validates a manifest's own path variables.
var dirResource = file.NewDir(useCasesDirName, file.WithEntryPattern(DirEntryPattern))

// ReadBaseline reads the SINGLE GLOBAL baseline deployment file under
// basePath — "{basePath}/baseline/baseline.json".
func (s *Store) ReadBaseline(ctx context.Context, basePath BasePath) (iothub.BaseDeployment, error) {
	handle, err := baselineFile.WithRead(file.Read{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	v, err := s.client.Read(ctx, handle, nil)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	return v.(iothub.BaseDeployment), nil
}

// WriteBaseline writes base as the SINGLE GLOBAL baseline deployment
// file under basePath.
func (s *Store) WriteBaseline(ctx context.Context, basePath BasePath, base iothub.BaseDeployment, opts file.WriteOptions) ([]string, error) {
	handle, err := baselineFile.WithCreate(file.Create{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return nil, err
	}
	return s.client.Create(ctx, handle, base, nil, opts)
}

// ReadUseCaseFile reads useCaseName's deployment manifest under
// basePath — just the manifest, not its nested devices (see
// Store.Read for that).
func (s *Store) ReadUseCaseFile(ctx context.Context, basePath BasePath, useCaseName Name) (iothub.LayeredDeployment, error) {
	handle, err := fileResource.WithRead(file.Read{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return iothub.LayeredDeployment{}, err
	}
	v, err := s.client.Read(ctx, handle, map[string]string{useCaseNameVar: string(useCaseName)})
	if err != nil {
		return iothub.LayeredDeployment{}, err
	}
	return v.(iothub.LayeredDeployment), nil
}

// WriteUseCaseFile writes manifest as useCaseName's deployment
// manifest under basePath.
func (s *Store) WriteUseCaseFile(ctx context.Context, basePath BasePath, useCaseName Name, manifest iothub.LayeredDeployment, opts file.WriteOptions) ([]string, error) {
	handle, err := fileResource.WithCreate(file.Create{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return nil, err
	}
	return s.client.Create(ctx, handle, manifest, map[string]string{useCaseNameVar: string(useCaseName)}, opts)
}

// PatchUseCaseFile applies a typed partial update (via patchCodec) to
// useCaseName's deployment manifest under basePath — the Stage D2
// equivalent of the former ports.PatchEncoded(usecase.NewFile(basePath),
// ...) call external callers (app/iotedge.PatchUseCaseModule) used
// directly. A FREE FUNCTION, not a *Store method: Go methods cannot
// introduce a new type parameter (P) beyond the receiver's own — the
// same reason [file.WithPatch] itself is a free function, not a
// Resource[T] method (see that function's own doc comment).
func PatchUseCaseFile[P any](ctx context.Context, s *Store, basePath BasePath, useCaseName Name, patchCodec codex.Codec[P], patch P, opts file.WriteOptions) error {
	handle, err := file.WithPatch(fileResource, patchCodec, file.Patch{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return err
	}
	return s.client.Patch(ctx, handle, patch, map[string]string{useCaseNameVar: string(useCaseName)}, opts)
}

// ListUseCaseEntries returns every discovered directory entry under
// basePath's "usecases" directory — the low-level, raw [file.DirEntry]
// access Store.ListNames derives from; exposed directly for a caller
// needing the raw Name/RelPath/Kind detail (e.g. a demo showing
// api/file's own Dir-listing mechanism, not just the extracted name).
func (s *Store) ListUseCaseEntries(ctx context.Context, basePath BasePath, opts file.DirOptions) ([]file.DirEntry, error) {
	handle, err := dirResource.WithList(file.ListDir{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return nil, err
	}
	return s.client.ListDir(ctx, handle, nil, opts)
}

// ListNames returns every discovered use case name under
// "{basePath}/usecases" — a thin convenience wrapping
// [Store.ListUseCaseEntries], extracting each entry's captured
// "useCase" var. Pair a returned name with Store.ReadUseCaseFile/
// Store.Read to read/patch that SPECIFIC discovered use case's
// manifest.
func (s *Store) ListNames(ctx context.Context, basePath BasePath, opts file.DirOptions) ([]Name, error) {
	entries, err := s.ListUseCaseEntries(ctx, basePath, opts)
	if err != nil {
		return nil, err
	}
	names := make([]Name, len(entries))
	for i, e := range entries {
		names[i] = Name(e.Vars[useCaseEntryVar])
	}
	return names, nil
}

// ── UseCase ───────────────────────────────────────────────────────────────────

// UseCase is the domain-level composition struct for ONE use case: its
// name (from the file's own path, not its body) paired with its PURE
// [iothub.LayeredDeployment] wire content, plus every device
// nested under it. [iothub.LayeredDeployment] itself stays
// pure/unchanged — Name and Devices live only on UseCase, assembled by
// [Store.Read]/[Store.Write].
type UseCase struct {
	Name               Name
	DeploymentManifest iothub.LayeredDeployment
	Devices            []DeviceConfig
}

// Read reads useCaseName's deployment manifest AND every device nested
// under it, assembling one UseCase value in one call.
func (s *Store) Read(ctx context.Context, basePath BasePath, useCaseName Name) (UseCase, error) {
	manifest, err := s.ReadUseCaseFile(ctx, basePath, useCaseName)
	if err != nil {
		return UseCase{}, err
	}

	// CreateIfMissing: a use case with no devices yet has no
	// "devices/{useCaseName}" directory at all — treated as zero devices,
	// not a DirReadError, the same "nothing to discover yet" semantics
	// Store.ListDeviceIDs already gives an empty (but existing) directory.
	deviceIDs, err := s.ListDeviceIDs(ctx, basePath, useCaseName, file.DirOptions{CreateIfMissing: true})
	if err != nil {
		return UseCase{}, err
	}
	devices := make([]DeviceConfig, len(deviceIDs))
	for i, id := range deviceIDs {
		cfg, err := s.ReadDeviceConfig(ctx, basePath, useCaseName, id)
		if err != nil {
			return UseCase{}, err
		}
		devices[i] = cfg
	}

	return UseCase{Name: useCaseName, DeploymentManifest: manifest, Devices: devices}, nil
}

// Write writes uc.DeploymentManifest AND every uc.Devices entry — the
// inverse of [Store.Read], one call.
func (s *Store) Write(ctx context.Context, basePath BasePath, uc UseCase, opts file.WriteOptions) error {
	if _, err := s.WriteUseCaseFile(ctx, basePath, uc.Name, uc.DeploymentManifest, opts); err != nil {
		return err
	}
	for _, device := range uc.Devices {
		if _, err := s.WriteDeviceConfig(ctx, basePath, uc.Name, device, opts); err != nil {
			return err
		}
	}
	return nil
}
