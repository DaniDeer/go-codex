package usecase

import (
	"path/filepath"

	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
	f "github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

// ── Use case model ────────────────────────────────────────────────────────────
//
// This file holds EVERYTHING that describes "what is a use case": its
// path pattern, its directory-entry filename shape, its name-validation
// codec, its file/dir port CONSTRUCTORS ([NewFile]/[NewDir]), and the
// domain-level composition ([UseCase]/[Read]/[Write]) that pairs a use
// case with the devices nested under it. Mirrors devicefile.go/
// devicedir.go/deviceconfig.go's device-level model one level down,
// consolidated into a single file since a use case is the package's
// primary aggregate root.

// See config.go for the filesystem-layout constants this file's port
// constructors are built from (useCasesDirName, useCaseNameVar,
// useCasePathPattern, useCaseEntryShape, useCaseEntryVar), and for
// nameCodec (the raw Codec[string] validator used below) plus the
// exported [Name] type/[NameCodec]/[NewName] this file's Go-level API
// (UseCase.Name/Read/Write/ListNames) is typed with.

// FileFormat is the declared, reusable format/codec pairing for an
// iotedge deployment manifest file — no I/O by itself; it exists so
// [NewFile] and any caller building their own ports.File don't have to
// repeat format.JSON(manifesttemplate.DeploymentManifestCodec).
var FileFormat = f.JSON(manifesttemplate.DeploymentManifestCodec)

// DirEntryPattern declares the filename SHAPE for iotedge config files
// inside a config directory: each file is one "use case", and the
// filename (minus ".json") IS that use case's name — e.g. "usecase1.json"
// is the "usecase1" use case's deployment manifest. A file that doesn't
// match this shape at all (a stray ".gitkeep"/README alongside the config
// files) is silently excluded by [ports.Dir.List] — see
// [ports.EntryPattern]'s own doc.
var DirEntryPattern = ports.EntryPattern{
	Template: useCaseEntryShape,
	Params:   []ports.EntryParam{{Name: useCaseEntryVar, Codec: &nameCodec}},
}

// NewFile declares the templated file port for a USE CASE's deployment
// manifest under basePath — "{basePath}/usecases/{usecase_name}.json" —
// a thin, pure (no I/O) constructor over ports.NewFile, using
// FileFormat. usecase_name is validated against nameCodec (the SAME
// codec [DirEntryPattern] uses to validate a discovered use case's name)
// via a PLAIN (non-merge) [ports.FilePathParam] —
// [manifesttemplate.DeploymentManifest] stays pure wire/file content;
// usecase_name is never merged into it. Read/Write take usecase_name via
// their own vars map, e.g.
// NewFile(basePath).Read(map[string]string{"usecase_name": name}, opts).
//
// The returned port is reused for reading (File.Read), writing
// (File.Write), and patching (ports.PatchEncoded(file, ...)) — see
// [Read]/[Write] for the higher-level convenience that combines this
// with device discovery.
func NewFile(basePath string) ports.File[manifesttemplate.DeploymentManifest] {
	return ports.NewFile[manifesttemplate.DeploymentManifest](filepath.Join(basePath, useCasePathPattern), FileFormat,
		ports.FilePathParam{Name: useCaseNameVar, Codec: &nameCodec},
	)
}

// NewDir declares the directory-listing port for a directory of iotedge
// config files (deployment manifests), one per use case — mirrors
// [NewFile]'s shape exactly: a thin, pure (no I/O) constructor over
// [ports.NewDir], using [DirEntryPattern] so [ports.Dir.List] discovers
// which use cases exist and extracts each one's name, the same
// declarative way [NewFile]'s caller validates a manifest's own path
// variables.
//
// The returned port is read-only (listing has no write/patch operation)
// — pair its result with [NewFile] to read/patch a SPECIFIC discovered
// use case's manifest.
func NewDir(path string) ports.Dir {
	return ports.NewDir(path, ports.WithEntryPattern(DirEntryPattern))
}

// ListNames returns every discovered use case name under
// "{basePath}/usecases" — a thin convenience wrapping [NewDir] +
// [ports.Dir.List], extracting each entry's captured "useCase" var. Pair
// a returned name with [NewFile]/[Read] to read/patch that SPECIFIC use
// case's manifest.
func ListNames(basePath string, opts ports.DirOptions) ([]Name, error) {
	entries, err := NewDir(filepath.Join(basePath, useCasesDirName)).List(nil, opts)
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
// [manifesttemplate.DeploymentManifest] wire content, plus every device
// nested under it. [manifesttemplate.DeploymentManifest] itself stays
// pure/unchanged — Name and Devices live only on UseCase, assembled by
// [Read]/[Write].
type UseCase struct {
	Name               Name
	DeploymentManifest manifesttemplate.DeploymentManifest
	Devices            []DeviceConfig
}

// Read reads useCaseName's deployment manifest AND every device nested
// under it, assembling one UseCase value in one call.
func Read(basePath string, useCaseName Name, opts ports.FileOptions) (UseCase, error) {
	manifest, err := NewFile(basePath).Read(map[string]string{useCaseNameVar: string(useCaseName)}, opts)
	if err != nil {
		return UseCase{}, err
	}

	// CreateIfMissing: a use case with no devices yet has no
	// "devices/{useCaseName}" directory at all — treated as zero devices,
	// not a [ports.DirReadError], the same "nothing to discover yet"
	// semantics [ports.Dir.List] already gives an empty (but existing)
	// directory.
	deviceIDs, err := ListDeviceIDs(basePath, useCaseName, ports.DirOptions{
		Observer:        opts.Observer,
		Context:         opts.Context,
		CreateIfMissing: true,
	})
	if err != nil {
		return UseCase{}, err
	}
	devices := make([]DeviceConfig, len(deviceIDs))
	for i, id := range deviceIDs {
		cfg, err := ReadDeviceConfig(basePath, useCaseName, id, opts)
		if err != nil {
			return UseCase{}, err
		}
		devices[i] = cfg
	}

	return UseCase{Name: useCaseName, DeploymentManifest: manifest, Devices: devices}, nil
}

// Write writes uc.DeploymentManifest AND every uc.Devices entry — the
// inverse of [Read], one call.
func Write(basePath string, uc UseCase, opts ports.FileOptions) error {
	if _, err := NewFile(basePath).Write(map[string]string{useCaseNameVar: string(uc.Name)}, uc.DeploymentManifest, opts); err != nil {
		return err
	}
	for _, device := range uc.Devices {
		if _, err := WriteDeviceConfig(basePath, uc.Name, device, opts); err != nil {
			return err
		}
	}
	return nil
}
