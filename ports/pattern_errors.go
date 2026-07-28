package ports

import (
	"fmt"
	"log/slog"
)

// MissingPatternError is returned by [RegisterREST], [RegisterEvent],
// [RegisterReqReply], [RegisterMCP], and [RegisterLLM] when the port declares
// no [Pattern] of the requested kind.
type MissingPatternError struct {
	// Port is the name passed to the port constructor.
	Port string
	// Kind identifies the requested pattern kind: "rest", "event", "reqreply",
	// "mcp", "file", "cache", "socket", or "llm".
	Kind string
}

func (e MissingPatternError) Error() string {
	return fmt.Sprintf("port %q: no %s Pattern declared", e.Port, e.Kind)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingPatternError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", e.Port),
		slog.String("kind", e.Kind),
	)
}

// PatternRegisterError is returned by a port constructor when building a
// handle from a declared [Pattern] fails. Common causes: a [RESTPattern.Opts]
// entry names a path variable that is not a {varName} placeholder in Path
// (or the equivalent for [EventPattern]/[ReqReplyPattern]/[MCPPattern]); a
// declared CustomFormat on [FilePattern]/[CachePattern]/[SocketPattern] holds
// the wrong format.Format[T] type; or a pattern is declared on a port type
// that doesn't support it (e.g. [CachePattern] on a [SourcePort],
// [SocketPattern] on an [IOPort]). Wraps the underlying error — from
// rest/events/reqreply/mcp package Register calls, a format type-mismatch
// (rest/events/reqreply's FormatOptError), or a plain descriptive error for
// port-type rejections.
type PatternRegisterError struct {
	// Port is the name passed to the port constructor.
	Port string
	// Kind identifies the pattern kind that failed to build: "rest", "event",
	// "reqreply", "mcp", "file", "cache", "socket", or "llm".
	Kind string
	// Err is the underlying error.
	Err error
}

func (e PatternRegisterError) Error() string {
	return fmt.Sprintf("port %q: building %s pattern: %v", e.Port, e.Kind, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e PatternRegisterError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e PatternRegisterError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", e.Port),
		slog.String("kind", e.Kind),
		slog.Any("err", e.Err),
	)
}
