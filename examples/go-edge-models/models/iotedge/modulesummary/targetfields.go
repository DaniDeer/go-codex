package modulesummary

import (
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	usecase "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
)

// ── Shared "target selector" field declarations ────────────────────────────
//
// modulesummary.ReadReq and the sibling updatemoduleimage.Req both
// identify ONE module in ONE use case (optionally scoped to one device)
// via the IDENTICAL four fields: basePath/useCaseName/moduleName/
// deviceID — same wire key, same codec, same description, on two
// different Go request types. codex.Field[T,F] is generic over BOTH the
// container type T and the field type F, so a generic BUILDER function
// (not a shared var, since T differs per caller) is the single source of
// truth for the key name + codec + description text; each caller still
// supplies its own type-specific get/set closures.

// BasePathField declares a REQUIRED "basePath" field for any request
// type T, using [usecase.BasePathCodec] directly — the description of
// what "basePath" means (the use-case layout it roots) lives ENTIRELY
// on that codec (models/iotedge/usecase/config.go), not duplicated
// here; this field also inherits usecase.BasePathCodec's own
// canonicalization (filepath.Clean) and validated Go type
// ([usecase.BasePath], not a bare string) automatically.
func BasePathField[T any](get func(T) usecase.BasePath, set func(*T, usecase.BasePath)) c.FieldCodec[T] {
	return c.RequiredField("basePath", usecase.BasePathCodec, get, set)
}

// UseCaseNameField declares a REQUIRED "useCaseName" field for any
// request type T, using [usecase.NameCodec] directly — the description
// of what "useCaseName" means lives ENTIRELY on that codec
// (models/iotedge/usecase/config.go), not duplicated here.
func UseCaseNameField[T any](get func(T) usecase.Name, set func(*T, usecase.Name)) c.FieldCodec[T] {
	return c.RequiredField("useCaseName", usecase.NameCodec, get, set)
}

// ModuleNameField declares a REQUIRED "moduleName" field for any request
// type T, using [ModuleOrSystemModuleNameCodec] — the module (or
// "edgeAgent"/"edgeHub" system module) targeted.
func ModuleNameField[T any](get func(T) iothub.ModuleName, set func(*T, iothub.ModuleName)) c.FieldCodec[T] {
	return c.RequiredField("moduleName",
		ModuleOrSystemModuleNameCodec.WithDescription(
			"The name of the module to target, e.g. \"factory-dashboard\" — "+
				"or \"edgeAgent\"/\"edgeHub\" for a system module.",
		),
		get, set,
	)
}

// deviceIDFieldCodec is [DeviceIDField]'s own field-level codec — NOT
// [usecase.DeviceIDCodec] reused as-is. codex.Struct.Encode
// unconditionally writes EVERY declared field regardless of Required/
// Optional (see codex.Struct's own doc comment), including this one at
// its ZERO VALUE (DeviceID("")) whenever a caller's Go value has no
// device set — so a round trip (Encode then Decode, exactly what
// Codec.Validate does) ALWAYS re-encounters an EXPLICIT `"deviceID": ""`
// on the wire, never a genuinely absent key. usecase.DeviceIDCodec's
// non-empty Refine constraint applies on BOTH directions (Refine always
// does), so reusing it here would make even this package's OWN
// round-trip of a valid "no device" value fail. Empty string is
// therefore accepted on BOTH directions as the "no device" sentinel —
// still typed as [usecase.DeviceID] throughout, just without
// usecase.DeviceIDCodec's stricter constraint (which remains correct
// and unchanged for every OTHER, always-non-empty use of DeviceID,
// e.g. usecase.ReadDeviceConfig's own path parameter).
var deviceIDFieldCodec = c.MapCodecSafe(c.String(),
	func(s string) usecase.DeviceID { return usecase.DeviceID(s) },
	func(d usecase.DeviceID) (string, error) { return string(d), nil },
).WithDescription(
	"If set, scopes the operation to THIS DEVICE'S OWN config " +
		"(template + this device's own config, as applicable) " +
		"instead of the use case template alone.",
)

// DeviceIDField declares an OPTIONAL "deviceID" field for any request
// type T, using [deviceIDFieldCodec] — empty (the zero value,
// DeviceID("")) means "the use case template itself, with no device
// overrides applied," and round-trips cleanly through Encode/Decode
// (see deviceIDFieldCodec's own doc comment for why it does NOT reuse
// usecase.DeviceIDCodec's non-empty constraint).
func DeviceIDField[T any](get func(T) usecase.DeviceID, set func(*T, usecase.DeviceID)) c.FieldCodec[T] {
	return c.OptionalField("deviceID", deviceIDFieldCodec, get, set)
}
