package mcprest

import (
	"fmt"
	"log/slog"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ToolRequestMapError wraps a failing toReq mapper function passed to
// [MappedToolHandler] — distinct from the underlying REST call's own
// typed errors, which continue to forward unchanged.
type ToolRequestMapError struct {
	// Method is handle.Descriptor.Method, for context.
	Method string
	// Path is handle.Descriptor.Path, for context.
	Path string
	// Err is the error returned by the toReq mapper function.
	Err error
}

func (e ToolRequestMapError) Error() string {
	return fmt.Sprintf("mcprest: map tool input to %s %s: %s", e.Method, e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ToolRequestMapError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ToolRequestMapError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", e.Method),
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// ToolResponseMapError wraps a failing fromResp mapper function passed to
// [MappedToolHandler] — same shape as [ToolRequestMapError].
type ToolResponseMapError struct {
	// Method is handle.Descriptor.Method, for context.
	Method string
	// Path is handle.Descriptor.Path, for context.
	Path string
	// Err is the error returned by the fromResp mapper function.
	Err error
}

func (e ToolResponseMapError) Error() string {
	return fmt.Sprintf("mcprest: map REST response from %s %s: %s", e.Method, e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ToolResponseMapError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ToolResponseMapError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", e.Method),
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// RESTClientErrorPayload is the structured MCP tool-error payload used by
// [DefaultErrorPatterns] for REST-client-side failures (never business
// logic errors — those remain the caller's own [apimcp.ErrorPattern]
// declarations, matched first if declared before DefaultErrorPatterns'
// rules in NewTool's opts).
type RESTClientErrorPayload struct {
	// Kind identifies which client error type matched: "unexpected_status",
	// "request", "request_build", "response_body", "security_credential",
	// "path_param", "missing_path_var", "query_param", "cookie_param",
	// "header_param", "request_map", or "response_map".
	Kind string
	// StatusCode is populated only when Kind == "unexpected_status".
	StatusCode int
	// Body is the raw response body, populated only when
	// Kind == "unexpected_status".
	Body string
	// Message is err.Error() — always populated.
	Message string
}

// restClientErrorPayloadCodec is the codec backing every [DefaultErrorPatterns]
// rule — RESTClientErrorPayload is an OUTPUT-only payload (always encoded,
// never decoded from external input), so plain RequiredFields are enough.
var restClientErrorPayloadCodec = codex.Struct[RESTClientErrorPayload](
	codex.RequiredField("kind", codex.String(), func(p RESTClientErrorPayload) string { return p.Kind }, func(p *RESTClientErrorPayload, v string) { p.Kind = v }),
	codex.RequiredField("status_code", codex.Int(), func(p RESTClientErrorPayload) int { return p.StatusCode }, func(p *RESTClientErrorPayload, v int) { p.StatusCode = v }),
	codex.RequiredField("body", codex.String(), func(p RESTClientErrorPayload) string { return p.Body }, func(p *RESTClientErrorPayload, v string) { p.Body = v }),
	codex.RequiredField("message", codex.String(), func(p RESTClientErrorPayload) string { return p.Message }, func(p *RESTClientErrorPayload, v string) { p.Message = v }),
)

// DefaultErrorPatterns returns [apimcp.ToolOpt] values mapping every
// exported adapters/nethttp and api/rest CLIENT error type — including the
// pre-flight validation errors [rest.PathParamError]/[rest.MissingPathVarError]/
// [rest.QueryParamError]/[rest.CookieParamError]/[rest.HeaderParamError] that
// [nethttp.CallWithHandle] returns BEFORE any HTTP request is sent — PLUS
// [ToolRequestMapError]/[ToolResponseMapError], into [RESTClientErrorPayload]
// — pass alongside [apimcp.NewTool]'s other opts. Purely additive: declare
// your OWN [apimcp.ErrorPattern] BEFORE these in the opts list to override
// one of these mappings for a specific tool (first-declared-rule-wins, per
// [apimcp.ErrorPattern]'s existing precedence rule).
//
// [rest.InvalidPathParamError] is deliberately NOT covered — it is a
// Register-time/spec-declaration error (a [rest.PathParam] naming a
// variable absent from the path template), never returned by a runtime
// [nethttp.Call]/[nethttp.CallWithHandle] invocation. [nethttp.ErrorPatternResponse]
// (the underlying REST route's OWN declared [rest.ErrorPattern] match) is
// also NOT covered — it already carries an application-decoded typed
// Value; wrapping it generically here would discard that type information
// rather than add to it.
func DefaultErrorPatterns() []apimcp.ToolOpt {
	return []apimcp.ToolOpt{
		apimcp.ErrorPattern[nethttp.UnexpectedStatusError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e nethttp.UnexpectedStatusError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{
					Kind:       "unexpected_status",
					StatusCode: e.StatusCode,
					Body:       string(e.Body),
					Message:    e.Error(),
				}, nil
			},
		),
		apimcp.ErrorPattern[nethttp.RequestError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e nethttp.RequestError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "request", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[nethttp.RequestBuildError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e nethttp.RequestBuildError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "request_build", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[nethttp.ResponseBodyError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e nethttp.ResponseBodyError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "response_body", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[rest.SecurityCredentialError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e rest.SecurityCredentialError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "security_credential", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[rest.PathParamError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e rest.PathParamError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "path_param", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[rest.MissingPathVarError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e rest.MissingPathVarError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "missing_path_var", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[rest.QueryParamError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e rest.QueryParamError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "query_param", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[rest.CookieParamError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e rest.CookieParamError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "cookie_param", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[rest.HeaderParamError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e rest.HeaderParamError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "header_param", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[ToolRequestMapError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e ToolRequestMapError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "request_map", Message: e.Error()}, nil
			},
		),
		apimcp.ErrorPattern[ToolResponseMapError, RESTClientErrorPayload](
			restClientErrorPayloadCodec,
			func(e ToolResponseMapError) (RESTClientErrorPayload, error) {
				return RESTClientErrorPayload{Kind: "response_map", Message: e.Error()}, nil
			},
		),
	}
}
