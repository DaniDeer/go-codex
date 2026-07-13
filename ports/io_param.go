package ports

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// IOParam is a protocol-agnostic parameter declaration for a port.
//
// At adapter binding time each adapter maps IOParam to its protocol-specific
// parameter type using [IOParam.Name] as the key:
//   - REST/HTTP: PathParam, QueryParam, HeaderParam, CookieParam
//   - Events (MQTT, ZeroMQ): TopicParam
//   - MQTT5: TopicParam + UserPropertyParam
//   - File: FilePathParam
//   - SQL: query bind variable name
//
// IOParam mirrors the .WithCodec(c) pattern of rest.PathParam and
// events.TopicParam — declare inline without a temporary variable:
//
//	ports.IOParam{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)
type IOParam struct {
	// Name is the parameter key. Must match the {name} placeholder used
	// in the adapter's topic/path template (e.g. {sensorID}).
	Name string
	// Description documents the parameter for spec generation.
	Description string
	// Codec, when non-nil, validates the parameter value at runtime.
	// Use codex.String(validate.UUID()) for UUID params, etc.
	Codec *codex.Codec[string]
	// Required causes the adapter to reject messages where this parameter
	// is absent or fails codec validation.
	Required bool
}

// WithCodec returns a copy of IOParam with Codec set to c.
// Avoids a temporary variable when declaring params inline:
//
//	ports.IOParam{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)
func (p IOParam) WithCodec(c codex.Codec[string]) IOParam {
	p.Codec = &c
	return p
}

// PortOptions configures a port constructor ([NewSourcePort], [NewSinkPort], [NewIOPort]).
type PortOptions struct {
	// Params declares the protocol-agnostic IO parameters for this port.
	// Each param carries a name, description, optional codec, and required flag.
	// Adapters map param names to protocol-specific param types at binding time.
	Params []IOParam

	// Buffer sets the internal channel buffer size. Default 0 (unbuffered).
	// A non-zero buffer prevents a slow adapter from blocking the pipeline.
	Buffer int

	// Observer receives port lifecycle events.
	// When nil, resolved from ctx at Bind/Stream/Connect time.
	Observer stats.Observer
}
