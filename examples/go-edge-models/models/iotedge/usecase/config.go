package usecase

import (
	"fmt"
	"path/filepath"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file is the SINGLE SOURCE OF TRUTH for this package's filesystem
// layout AND for the three path-derived identifier types ([Name]/
// [DeviceID]/[BasePath]) that layout is keyed by — every directory
// name, path-variable name, derived path template, and identifier
// codec/constructor used by usecase.go/devicefile.go/devicedir.go/
// deviceconfig.go's file/dir port constructors and domain types comes
// from here, so "where do use case/device files actually live under
// basePath, and what makes a name/ID/basePath valid" is answered by
// reading ONE file rather than grepping four. The layout constants are
// unexported (a mix of const and fmt.Sprintf-derived var — see below),
// not a runtime config surface — a caller cannot override the layout;
// it's a refactor for readability, not a new feature. [Name]/
// [DeviceID]/[BasePath]/their codecs/[NewName]/[NewDeviceID]/
// [NewBasePath] ARE exported — they're this package's public, typed
// vocabulary for "a use case's name" / "a device's ID" / "the use-case
// layout's root directory".
//
// The real, on-disk layout this package assumes:
//
//	{basePath}/baseline/baseline.json
//	{basePath}/usecases/{usecase_name}.json
//	{basePath}/devices/{usecase_name}/{device_id}.json
const (
	// baselineDirName is the subdirectory under basePath holding the
	// SINGLE GLOBAL baseline file — mirrors useCasesDirName/
	// devicesDirName's "one subdirectory per concern" convention,
	// even though this one holds exactly one file, not one-per-name.
	baselineDirName = "baseline"
	// baselineFileName is the SINGLE GLOBAL file (no template variables
	// — unlike every other path here) holding the priority-0 base
	// deployment applied to EVERY device, regardless of use case —
	// see models/azure/iothub for its wire shape.
	baselineFileName = "baseline.json"
	// useCasesDirName is the subdirectory under basePath holding one
	// deployment manifest file per use case.
	useCasesDirName = "usecases"
	// devicesDirName is the subdirectory under basePath holding one
	// device-manifest subdirectory per use case (further nested by
	// usecase_name, then one file per device).
	devicesDirName = "devices"

	// useCaseNameVar is the path/template variable name for a use
	// case's own name — used both as {usecase_name} in file path
	// templates and as the captured var name for [ports.FilePathParam]/
	// [ports.DirPathParam].
	useCaseNameVar = "usecase_name"
	// deviceIDVar is the path/template variable name for a device's ID
	// — used both as {device_id} in file path templates and as the
	// captured var name for [ports.FilePathParam]/[ports.EntryParam].
	deviceIDVar = "device_id"
	// useCaseEntryVar is the captured var name [DirEntryPattern] uses
	// for a discovered use case's name (extracted from its filename,
	// not a path template variable substituted BY the caller — hence
	// its own, distinct name from useCaseNameVar).
	useCaseEntryVar = "useCase"
)

// ── Path patterns ────────────────────────────────────────────────────────
//
// The 6 values below are DERIVED from the leaf constants above via
// fmt.Sprintf, rather than a chain of "+" concatenations, so each
// template's literal shape (e.g. "%s/{%s}.json") reads directly next to
// the values filling it in. Each is wrapped as a [codex.Const[string]]
// (validated ONCE, at package init, against pathPatternCodec — a typo
// producing an empty pattern would panic immediately, not surface as a
// mysterious ports.File error at first use) rather than a bare string —
// this is exactly [codex.Const[T]]'s intended "compile-time-authored
// constant" use case (see docs/concepts/codec.md's Getter/Setter
// subsection). The pattern TEXT
// itself stays private (readable only via the embedded Const's own
// String()/Get()); ports.NewFile/NewDir's own {var} substitution still
// consumes that raw, unsubstituted text via String(), UNCHANGED from
// before this refactor.
//
// Three of the six ALSO substitute vars into their pattern — a
// validated, concrete-path-producing [codex.Template]: useCasePathPattern
// (1 var), deviceDirPathPattern (1 var), deviceFilePathPattern
// (2 vars). Each is a [codex.Template[T]] whose T is either the
// existing [Name] identifier type directly (single-var patterns need
// no dedicated wrapper struct — Name serves as its own FieldCodec
// container) or a small dedicated vars struct ([deviceFileVars], for
// the two-var pattern). [codex.Template.Build] substitutes an
// already-validated [Name]/[DeviceID] into the pattern directly (no
// ports.File/I/O involved at all), via the SAME [NameCodec]/
// [DeviceIDCodec] every other use of Name/DeviceID already goes
// through. The other three have nothing to substitute —
// baselinePathPattern names a single FIXED path (no {var} placeholders
// at all), and useCaseEntryShape/deviceEntryShape are filename SHAPES
// ports' own [ports.EntryPattern] EXTRACTS a var FROM (the reverse
// direction of Build) — so those three stay plain
// [codex.Const[string]], read via String() only.

// pathPatternCodec is the shared, validated Codec[string] every path
// pattern below is checked against — non-empty (an empty pattern would
// be silently meaningless as a path template).
var pathPatternCodec = c.String().Refine(v.NonEmptyString)

// baselinePathPattern is [NewBaselineFile]'s path, relative to
// basePath: "baseline/baseline.json". Has NO {var} placeholders —
// unlike every other pattern below — since it names the SAME single
// global file every time, never a per-use-case/per-device one.
var baselinePathPattern = c.MustConst(
	fmt.Sprintf("%s/%s", baselineDirName, baselineFileName),
	pathPatternCodec,
)

// useCasePathPattern is [NewFile]'s templated path, relative to
// basePath: "usecases/{usecase_name}.json" — a [codex.Template[Name]]
// whose single field substitutes {usecase_name} via [NameCodec]. Name
// serves as its own FieldCodec container (get/set are identity
// operations, via [codex.IdentityField]), so no dedicated vars struct
// is needed for a single-var pattern. [codex.Template.Build]
// substitutes name into the pattern, returning a concrete path
// relative to basePath — e.g. "usecases/usecase1.json" — validated the
// same way [NameCodec] validates every other use of Name.
var useCasePathPattern = c.NewTemplate(
	fmt.Sprintf("%s/{%s}.json", useCasesDirName, useCaseNameVar), c.PathStyle,
	c.IdentityField(useCaseNameVar, NameCodec),
)

// useCaseEntryShape is [DirEntryPattern]'s filename SHAPE: each use
// case's deployment manifest file is named "{useCase}.json". Unlike
// useCasePathPattern above, this has no Resolve method — a discovered
// filename's {useCase} segment is EXTRACTED by ports.EntryPattern, not
// substituted by this package.
var useCaseEntryShape = c.MustConst(
	fmt.Sprintf("{%s}.json", useCaseEntryVar),
	pathPatternCodec,
)

// deviceDirPathPattern is [NewDeviceDir]'s templated path, relative to
// basePath: "devices/{usecase_name}" — mirrors [useCasePathPattern]
// exactly, one directory level up (a device DIRECTORY, not a device
// FILE): a [codex.Template[Name]] substituting {usecase_name} via
// [NameCodec] (get/set are identity operations, via
// [codex.IdentityField]), returning a concrete directory path relative
// to basePath via [codex.Template.Build] — e.g. "devices/usecase1".
var deviceDirPathPattern = c.NewTemplate(
	fmt.Sprintf("%s/{%s}", devicesDirName, useCaseNameVar), c.PathStyle,
	c.IdentityField(useCaseNameVar, NameCodec),
)

// deviceFileVars bundles the two path variables deviceFilePathPattern
// substitutes — {usecase_name} and {device_id} — since, unlike
// [useCasePathPattern]/[deviceDirPathPattern]'s single-variable
// patterns, no single existing type holds both together.
type deviceFileVars struct {
	Name     Name
	DeviceID DeviceID
}

// deviceFilePathPattern is [NewDeviceFile]'s templated path, relative
// to basePath: "devices/{usecase_name}/{device_id}.json" — a
// [codex.Template[deviceFileVars]] substituting BOTH {usecase_name}
// and {device_id} via [NameCodec]/[DeviceIDCodec] (the SAME codecs
// every other use of Name/DeviceID already goes through).
// [codex.Template.Build] returns a concrete path relative to basePath
// — e.g. "devices/usecase1/sensor-1.json".
var deviceFilePathPattern = c.NewTemplate(
	fmt.Sprintf("%s/{%s}/{%s}.json", devicesDirName, useCaseNameVar, deviceIDVar), c.PathStyle,
	c.RequiredField(useCaseNameVar, NameCodec,
		func(v deviceFileVars) Name { return v.Name },
		func(v *deviceFileVars, n Name) { v.Name = n },
	),
	c.RequiredField(deviceIDVar, DeviceIDCodec,
		func(v deviceFileVars) DeviceID { return v.DeviceID },
		func(v *deviceFileVars, d DeviceID) { v.DeviceID = d },
	),
)

// deviceEntryShape is [DeviceDirEntryPattern]'s filename SHAPE: each
// device's manifest file is named "{device_id}.json". Like
// useCaseEntryShape above, this has no Resolve method — EXTRACTED, not
// substituted.
var deviceEntryShape = c.MustConst(
	fmt.Sprintf("{%s}.json", deviceIDVar),
	pathPatternCodec,
)

// ── Path construction helpers ────────────────────────────────────────────
//
// Each function below joins a [BasePath] with ONE path pattern above via
// filepath.Join — the SAME construction usecase.go/device.go's file/dir
// port constructors previously repeated inline at their own call sites
// (two of them via "+"+"" string concatenation instead of
// filepath.Join, an inconsistency this fixes too). Named per pattern
// (not a single generic join helper) so each call site reads as "the
// on-disk path for X" rather than a bare join. The "...Template" suffix
// marks a path that STILL contains {var} placeholders — substitution
// happens later, at Read/Write/List time, via
// ports.FilePathParam/ports.DirPathParam; baselineFilePath/
// useCasesDirPath have no vars to substitute at all.

// baselineFilePath returns the concrete, fully-resolved on-disk path
// for the SINGLE GLOBAL baseline file under basePath — no {var}
// placeholders (see [baselinePathPattern]).
func baselineFilePath(basePath BasePath) string {
	return filepath.Join(string(basePath), baselinePathPattern.String())
}

// useCasesDirPath returns the concrete, fully-resolved on-disk path for
// the "usecases" directory under basePath — used by [ListNames] to
// discover use cases (NOT [useCasePathPattern]'s own per-use-case
// template).
func useCasesDirPath(basePath BasePath) string {
	return filepath.Join(string(basePath), useCasesDirName)
}

// useCaseFilePathTemplate returns basePath joined with
// [useCasePathPattern]'s STILL-templated "{usecase_name}.json" path —
// substitution happens later, at Read/Write time, via
// ports.FilePathParam.
func useCaseFilePathTemplate(basePath BasePath) string {
	return filepath.Join(string(basePath), useCasePathPattern.String())
}

// deviceFilePathTemplate returns basePath joined with
// [deviceFilePathPattern]'s STILL-templated
// "{usecase_name}/{device_id}.json" path — substitution happens later,
// at Read/Write time, via ports.FilePathParam.
func deviceFilePathTemplate(basePath BasePath) string {
	return filepath.Join(string(basePath), deviceFilePathPattern.String())
}

// deviceDirPathTemplate returns basePath joined with
// [deviceDirPathPattern]'s STILL-templated "{usecase_name}" path —
// substitution happens later, at List time, via ports.DirPathParam.
func deviceDirPathTemplate(basePath BasePath) string {
	return filepath.Join(string(basePath), deviceDirPathPattern.String())
}

// ── Identifier types ────────────────────────────────────────────────────────
//
// Each identifier type below ([Name], [DeviceID], [BasePath]) is grouped
// as ONE self-contained block: the underlying raw Codec[string]
// validator, the named Go type, the re-typed [codex.MapCodecSafe]-built
// codec, and the smart constructor — in that order. The raw
// Codec[string] validators (nameCodec/deviceIDCodec) also stay in scope
// for [ports.FilePathParam]/[ports.DirPathParam]/[ports.EntryParam].Codec
// (a fixed [*codex.Codec[string]] field in the ports package), used
// directly by usecase.go/devicefile.go/devicedir.go's path/entry param
// declarations — two layers, one underlying constraint each.

// nameCodec is Name's raw Codec[string] validator.
var nameCodec = c.String().Refine(v.NonEmptyString).WithDescription(
	"The use case whose deployment manifest is targeted.",
)

// Name is a use case's own name — the {usecase_name} path segment
// ([NewFile]) and a discovered config file's {useCase} filename segment
// ([ListNames]) both become a Name. A named type (not bare string) so
// [UseCase.Name], [Read], [Write], [ListNames], [ListDeviceIDs],
// [ReadDeviceConfig], and [WriteDeviceConfig] all express "this
// parameter/field is specifically a use case's name" rather than an
// arbitrary string.
type Name string

// NameCodec validates/wraps a Name — the SAME NonEmptyString constraint
// (and description) nameCodec applies, re-typed via c.MapCodecSafe (which
// copies nameCodec's Schema unchanged) so callers work with Name
// directly instead of a bare string. Any field built from NameCodec
// (e.g. the sibling modulesummary package's own MCP tool request types,
// via modulesummary.UseCaseNameField) inherits nameCodec's description
// as its own MCP tool field description, with zero duplicated text.
var NameCodec = c.MapCodecSafe(nameCodec,
	func(s string) Name { return Name(s) },
	func(n Name) (string, error) { return string(n), nil },
)

// NewName is a smart constructor: validates s via NameCodec.New,
// returning the zero value and the first failing constraint's error
// (empty string) on failure.
func NewName(s string) (Name, error) { return NameCodec.New(Name(s)) }

// deviceIDCodec is DeviceID's raw Codec[string] validator.
var deviceIDCodec = c.String().Refine(v.NonEmptyString).WithDescription(
	"If set, scopes the operation to THIS DEVICE'S OWN config " +
		"(template + this device's own config, as applicable) " +
		"instead of the use case template alone.",
)

// DeviceID is a device's own ID — the {device_id} path segment
// ([NewDeviceFile]) and a discovered device manifest's filename segment
// ([ListDeviceIDs]) both become a DeviceID. Mirrors [Name]'s role one
// level down: [DeviceConfig.DeviceID], [ReadDeviceConfig], and
// [WriteDeviceConfig] all express "this parameter/field is specifically
// a device's ID" rather than an arbitrary string.
type DeviceID string

// DeviceIDCodec validates/wraps a DeviceID — the SAME NonEmptyString
// constraint (and description) deviceIDCodec applies, re-typed via
// c.MapCodecSafe so callers work with DeviceID directly instead of a
// bare string. Used directly as modulesummary.DeviceIDField's OPTIONAL
// "deviceID" field codec: codex.OptionalField's own semantics mean this
// non-empty constraint is only enforced when the wire key IS present —
// an ABSENT key still decodes to DeviceID("")'s zero value (the "no
// device" sentinel) without ever invoking this codec.
var DeviceIDCodec = c.MapCodecSafe(deviceIDCodec,
	func(s string) DeviceID { return DeviceID(s) },
	func(d DeviceID) (string, error) { return string(d), nil },
)

// NewDeviceID is a smart constructor: validates s via DeviceIDCodec.New,
// returning the zero value and the first failing constraint's error
// (empty string) on failure.
func NewDeviceID(s string) (DeviceID, error) { return DeviceIDCodec.New(DeviceID(s)) }

// basePathCodec is BasePath's raw Codec[string] validator: non-empty,
// with a description of what "basePath" means for this package's own
// layout — set HERE, the single source of truth. BasePathCodec (below)
// copies this Schema unchanged (see codex.MapCodecSafe's own doc
// comment), so any field built from BasePathCodec (e.g.
// modulesummary/updatemoduleimage's Req.BasePath, via
// modulesummary.BasePathField) surfaces this SAME text as its own MCP
// tool field description, with zero duplicated description text at the
// field-declaration call site.
var basePathCodec = c.String().Refine(v.NonEmptyString).WithDescription(
	"The root directory holding this use case layout: " +
		"{basePath}/baseline/baseline.json (the global baseline " +
		"deployment applied to every device), " +
		"{basePath}/usecases/{usecase_name}.json (a use case's shared " +
		"deployment template), and " +
		"{basePath}/devices/{usecase_name}/{device_id}.json (one " +
		"device's own config overrides).",
)

// BasePath is the root directory holding this package's ENTIRE on-disk
// layout (see this file's own top doc comment for the exact shape) — a
// named type (not bare string) so [NewFile]/[NewBaselineFile]/
// [NewDeviceFile]/[NewDeviceDir]/[Read]/[Write]/[ListNames]/
// [ListDeviceIDs]/[ReadDeviceConfig]/[WriteDeviceConfig]/[ReadEffective],
// and every caller layered on top of them (e.g. the sibling
// modulesummary/updatemoduleimage packages' own Req types, and
// app/iotedge's file-I/O wrappers), all express "this parameter/field is
// specifically the use-case layout's root," with exactly ONE
// string->BasePath conversion at the outermost boundary (an MCP tool
// request decode via [BasePathCodec], or a CLI/example entry point via
// [NewBasePath]) rather than a bare string threaded — and re-validated
// nowhere — through every layer in between.
type BasePath string

// BasePathCodec validates/wraps a BasePath — canonicalizes via
// filepath.Clean on decode (normalizes path separators and redundant
// "."/".." segments; deliberately does NOT require an absolute path —
// a caller may legitimately pass a path relative to the process's own
// working directory, e.g. in a test or a CLI invoked from the target
// directory). Encode is a trivial cast back to string: Clean's
// normalization already happened at decode time, so re-Cleaning on
// encode would be a no-op for any BasePath this package itself
// produced.
var BasePathCodec = c.MapCodecSafe(basePathCodec,
	func(s string) BasePath { return BasePath(filepath.Clean(s)) },
	func(bp BasePath) (string, error) { return string(bp), nil },
)

// NewBasePath is a smart constructor: validates+canonicalizes s via
// BasePathCodec.Decode (NOT .New — New validates and returns v
// UNCHANGED on success, which would skip filepath.Clean's
// canonicalization; Decode is what actually applies it), returning the
// zero value and the first failing constraint's error (empty string)
// on failure. THE single conversion point a caller (an MCP tool
// request decode, a CLI flag, this package's own examples) uses to turn
// a raw string into a BasePath — every function beneath that accepts
// BasePath directly, never a bare string.
func NewBasePath(s string) (BasePath, error) { return BasePathCodec.Decode(s) }
