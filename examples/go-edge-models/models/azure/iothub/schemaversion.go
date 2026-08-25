package iothub

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// SchemaVersion is the Azure IoT Edge deployment manifest schema
// version, e.g. "1.1" — appears on BOTH $edgeAgent's and $edgeHub's own
// "properties.desired" documents. Wraps a plain validated STRING (like
// manifesttemplate's Status/RestartPolicy lifecycle types) restricted to
// the schema versions this package is proven against — v.OneOf (NOT
// c.Eq, unlike Type's fixed "docker" value) since more
// than one version is valid and future manifest schema versions may be
// added here as they're adopted.
type SchemaVersion string

var SchemaVersionCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("1.0", "1.1")),
	func(s string) SchemaVersion { return SchemaVersion(s) },
	func(sv SchemaVersion) (string, error) { return string(sv), nil },
)

// SchemaVersionField declares a REQUIRED "schemaVersion" field for any
// container type T, using [SchemaVersionCodec] — single source of truth
// for EdgeAgentProperties' and EdgeHubProperties' identical
// "schemaVersion" field.
func SchemaVersionField[T any](get func(T) SchemaVersion, set func(*T, SchemaVersion)) c.FieldCodec[T] {
	return c.RequiredField("schemaVersion", SchemaVersionCodec, get, set)
}
