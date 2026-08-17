package usecase

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file is the SINGLE SOURCE OF TRUTH for this package's filesystem
// layout AND for the two path-derived identifier types ([Name]/
// [DeviceID]) that layout is keyed by — every directory name,
// path-variable name, derived path template, and identifier
// codec/constructor used by usecase.go/devicefile.go/devicedir.go/
// deviceconfig.go's file/dir port constructors and domain types comes
// from here, so "where do use case/device files actually live under
// basePath, and what makes a name/ID valid" is answered by reading ONE
// file rather than grepping four. The layout constants are unexported
// (a mix of const and fmt.Sprintf-derived var — see below), not a
// runtime config surface — a caller cannot override the layout; it's a
// refactor for readability, not a new feature. [Name]/[DeviceID]/their
// codecs/[NewName]/[NewDeviceID] ARE exported — they're this package's
// public, typed vocabulary for "a use case's name" / "a device's ID".
//
// The real, on-disk layout this package assumes:
//
//	{basePath}/usecases/{usecase_name}.json
//	{basePath}/devices/{usecase_name}/{device_id}.json
const (
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

// The 5 values below are DERIVED from the leaf constants above via
// fmt.Sprintf, rather than a chain of "+" concatenations, so each
// template's literal shape (e.g. "%s/{%s}.json") reads directly next to
// the values filling it in. fmt.Sprintf(...) is a function call, not a
// constant expression, so these are package-level vars rather than
// consts — evaluated once at package init, functionally equivalent to a
// const for every one of this package's call sites (all of which just
// read them as plain strings).
var (
	// useCasePathPattern is [NewFile]'s templated path, relative to
	// basePath: "usecases/{usecase_name}.json".
	useCasePathPattern = fmt.Sprintf("%s/{%s}.json", useCasesDirName, useCaseNameVar)
	// useCaseEntryShape is [DirEntryPattern]'s filename SHAPE: each use
	// case's deployment manifest file is named "{useCase}.json".
	useCaseEntryShape = fmt.Sprintf("{%s}.json", useCaseEntryVar)

	// deviceDirPathPattern is [NewDeviceDir]'s templated path, relative
	// to basePath: "devices/{usecase_name}".
	deviceDirPathPattern = fmt.Sprintf("%s/{%s}", devicesDirName, useCaseNameVar)
	// deviceFilePathPattern is [NewDeviceFile]'s templated path,
	// relative to basePath: "devices/{usecase_name}/{device_id}.json".
	deviceFilePathPattern = fmt.Sprintf("%s/{%s}/{%s}.json", devicesDirName, useCaseNameVar, deviceIDVar)
	// deviceEntryShape is [DeviceDirEntryPattern]'s filename SHAPE: each
	// device's manifest file is named "{device_id}.json".
	deviceEntryShape = fmt.Sprintf("{%s}.json", deviceIDVar)
)

// ── Identifier types ────────────────────────────────────────────────────────
//
// nameCodec/deviceIDCodec are the RAW Codec[string] validators —
// [ports.FilePathParam]/[ports.DirPathParam]/[ports.EntryParam].Codec
// all require exactly this type ([*codex.Codec[string]] is a fixed
// field type in the ports package), so these stay Codec[string] and are
// used directly by usecase.go/devicefile.go/devicedir.go's path/entry
// param declarations. [NameCodec]/[DeviceIDCodec] below wrap these same
// constraints as the named [Name]/[DeviceID] types for this package's
// own exported Go-level API (struct fields, function params/returns) —
// two layers, one underlying constraint each.
var nameCodec = c.String().Refine(v.NonEmptyString)
var deviceIDCodec = c.String().Refine(v.NonEmptyString)

// Name is a use case's own name — the {usecase_name} path segment
// ([NewFile]) and a discovered config file's {useCase} filename segment
// ([ListNames]) both become a Name. A named type (not bare string) so
// [UseCase.Name], [Read], [Write], [ListNames], [ListDeviceIDs],
// [ReadDeviceConfig], and [WriteDeviceConfig] all express "this
// parameter/field is specifically a use case's name" rather than an
// arbitrary string.
type Name string

// NameCodec validates/wraps a Name — the SAME NonEmptyString constraint
// nameCodec applies, re-typed via c.MapCodecSafe so callers work with
// Name directly instead of a bare string.
var NameCodec = c.MapCodecSafe(nameCodec,
	func(s string) Name { return Name(s) },
	func(n Name) (string, error) { return string(n), nil },
)

// NewName is a smart constructor: validates s via NameCodec.New,
// returning the zero value and the first failing constraint's error
// (empty string) on failure.
func NewName(s string) (Name, error) { return NameCodec.New(Name(s)) }

// DeviceID is a device's own ID — the {device_id} path segment
// ([NewDeviceFile]) and a discovered device manifest's filename segment
// ([ListDeviceIDs]) both become a DeviceID. Mirrors [Name]'s role one
// level down: [DeviceConfig.DeviceID], [ReadDeviceConfig], and
// [WriteDeviceConfig] all express "this parameter/field is specifically
// a device's ID" rather than an arbitrary string.
type DeviceID string

// DeviceIDCodec validates/wraps a DeviceID — the SAME NonEmptyString
// constraint deviceIDCodec applies, re-typed via c.MapCodecSafe so
// callers work with DeviceID directly instead of a bare string.
var DeviceIDCodec = c.MapCodecSafe(deviceIDCodec,
	func(s string) DeviceID { return DeviceID(s) },
	func(d DeviceID) (string, error) { return string(d), nil },
)

// NewDeviceID is a smart constructor: validates s via DeviceIDCodec.New,
// returning the zero value and the first failing constraint's error
// (empty string) on failure.
func NewDeviceID(s string) (DeviceID, error) { return DeviceIDCodec.New(DeviceID(s)) }
