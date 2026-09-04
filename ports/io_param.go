package ports

import (
	"context"
	"errors"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/llm"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// IOParam is a protocol-agnostic parameter declaration for a port.
//
// Enforcement depends on the bound adapter:
//   - Adapters backed by a protocol-level builder (rest.Route + PathParam/QueryParam,
//     events.ChannelHandle + TopicParam, mqtt5's UserPropertyParam) already validate
//     their own declarations at that layer; Params is descriptive only for these
//     (available for spec generation, not consulted at runtime).
//   - Adapters with no such builder — e.g. file.ReadEachAdapter / file.DrainWriteFileAdapter's
//     varsFor-extracted vars — call [ValidateParams] with the params retrieved via
//     [ParamsFromContext] to get real runtime enforcement.
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

// ErrParamMissing is wrapped in a [codex.ValidationError] by [ValidateParams] when
// a [IOParam] with Required set to true has no corresponding entry in vars.
var ErrParamMissing = errors.New("required param missing")

// ValidateParams validates vars (extracted routing values — topic vars, path vars,
// file-path template vars, …) against params. For each param:
//   - if Required and vars[param.Name] is absent or empty, records [ErrParamMissing]
//   - if Codec is non-nil and a value is present, decodes it and records any error
//
// Returns nil when every param is satisfied. Otherwise returns [codex.ValidationErrors]
// with one [codex.ValidationError] per failing param (Field = param name).
//
// Adapters that accept a vars-extraction function (e.g. file.ReadEachAdapter's
// varsFor, file.DrainWriteFileAdapter's varsFor) call this with the port's params —
// retrieved via [ParamsFromContext] — before using the extracted vars, giving
// [IOParam] real runtime enforcement for adapters that have no dedicated
// protocol-level param type (unlike rest.PathParam or mqtt5.UserPropertyParam,
// which already validate via their own builder-time mechanism).
func ValidateParams(params []IOParam, vars map[string]string) error {
	var errs codex.ValidationErrors
	for _, p := range params {
		v, ok := vars[p.Name]
		if !ok || v == "" {
			if p.Required {
				errs = append(errs, codex.ValidationError{Field: p.Name, Err: ErrParamMissing})
			}
			continue
		}
		if p.Codec != nil {
			if _, err := p.Codec.Decode(v); err != nil {
				errs = append(errs, codex.ValidationError{Field: p.Name, Err: err})
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// paramsCtxKey is the context key used by [WithParams] / [ParamsFromContext].
type paramsCtxKey struct{}

// WithParams returns a copy of ctx carrying params, retrievable via [ParamsFromContext].
// [SourcePort.Bind], [SinkPort.Bind], [IOPort.Bind], and [ToolPort.Bind] call this
// automatically with the port's declared [IOParam]s before invoking the bound
// adapter, so adapters can opt into [ValidateParams] enforcement without a direct
// dependency on the port.
func WithParams(ctx context.Context, params []IOParam) context.Context {
	return context.WithValue(ctx, paramsCtxKey{}, params)
}

// ParamsFromContext returns the [IOParam] slice stored by [WithParams], or nil
// when none was stored (e.g. the port declared no Params).
func ParamsFromContext(ctx context.Context) []IOParam {
	p, _ := ctx.Value(paramsCtxKey{}).([]IOParam)
	return p
}

// PortOptions configures a port constructor ([NewSourcePort], [NewSinkPort],
// [NewIOPort], [NewToolPort], [NewLatestPort], [NewDuplexPort]).
//
// Patterns are NOT part of PortOptions — declare the port with PortOptions
// first (structural shape + shared builder references), then plug in each
// communication Pattern via the port's PluginXxxPattern method (e.g.
// [SourcePort.PluginEventPattern], [IOPort.PluginRESTPattern]) at whatever
// point in your wiring code makes sense — often right before Bind, but
// Patterns declared as standalone package-level values can be plugged in
// anywhere. PluginXxxPattern registers the Pattern (against RESTBuilder/
// EventClient/ReqReplyBuilder/MCPBuilder below, or a private single-use
// Builder when the matching field is nil) AND returns the resulting typed
// handle directly — no separate handle-lookup step.
type PortOptions struct {
	// Params declares the protocol-agnostic IO parameters for this port. Only
	// meaningful for adapters with no [Pattern] of their own — i.e. handle-less
	// adapters (file.ReadEachAdapter, file.DrainWriteFileAdapter). Made
	// available to bound adapters via [ParamsFromContext]; those adapters call
	// [ValidateParams] to enforce them. Adapters backed by a [Pattern] (or,
	// pre-Pattern, a hand-built rest.RouteHandle/events.ChannelHandle) validate
	// through that handle instead and do not consult Params.
	Params []IOParam

	// Buffer sets the internal channel buffer size. Default 0 (unbuffered).
	// A non-zero buffer prevents a slow adapter from blocking the pipeline.
	// Only [SourcePort] and [SinkPort] honor Buffer — [IOPort] and [ToolPort]
	// have no internal channel to buffer (Connect/Bind delegate directly to
	// the adapter's Transform/Bind call) and ignore this field.
	Buffer int

	// Observer receives port lifecycle events: a "port.bind" [stats.Observer.RecordRequest]
	// call (and, when Observer also implements [stats.TraceObserver], a
	// "port.bind" span) wrapping each adapter Bind/Activate call, plus (for
	// [SinkPort] only) per-item stream draining via [stats.Observer].
	// When nil, resolved from ctx at Bind/Stream/Connect time.
	Observer stats.Observer

	// RESTBuilder registers each [RESTPattern]'s Route against b via
	// rest.Route.Register(b) — the SAME call a hand-declared route makes. Supply
	// the *rest.Server your application already uses (with rest.WithPathConstraints,
	// b.AddGlobalSecurity already configured, and rest.WithSecurityScheme declared
	// on the RESTPattern's own Route) to get full parity with hand-declared
	// routes: the resulting handle is indistinguishable from one built by
	// calling rest.NewRoute(...).Register(b) directly.
	//
	// When nil, ports registers against a private, single-use *rest.Server with
	// zero [rest.Info] — the same zero-ceremony default as a builder-free
	// construction, through the identical Register code path (there is no
	// separate, weaker construction path).
	RESTBuilder *rest.Server

	// EventClient — same idea as RESTBuilder, for [EventPattern] / events.Client.
	EventClient *events.Client

	// ReqReplyBuilder — same idea as RESTBuilder, for [ReqReplyPattern] / reqreply.Builder.
	ReqReplyBuilder *reqreply.Builder

	// MCPBuilder — same idea as RESTBuilder, for [MCPPattern] / apimcp.Builder.
	MCPBuilder *apimcp.Builder

	// LLMBuilder — same idea as RESTBuilder, for [LLMPattern] / llm.Builder.
	// Only [IOPort] consults this field — an LLM completion is an outbound
	// call the pipeline makes (the same category as a REST client call or a
	// SQL query), not a transport that receives external requests, so
	// [ToolPort]/[SourcePort]/[SinkPort]/[LatestPort] ignore it.
	LLMBuilder *llm.Builder
}
