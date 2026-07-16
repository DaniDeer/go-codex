package ports

import (
	"fmt"
	"log/slog"
)

// MissingPatternError is returned by [RESTHandle], [EventHandle],
// [ReqReplyHandle], and [MCPHandle] when the port declares no [Pattern] of the
// requested kind.
type MissingPatternError struct {
	// Port is the name passed to the port constructor.
	Port string
	// Kind identifies the requested pattern kind: "rest", "event", "reqreply", or "mcp".
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
// handle from a declared [Pattern] fails — e.g. a [RESTPattern.Opts] entry
// names a path variable that is not a {varName} placeholder in Path, or the
// equivalent for [EventPattern]/[ReqReplyPattern]/[MCPPattern]. Wraps the
// underlying rest/events/reqreply/mcp error.
type PatternRegisterError struct {
	// Port is the name passed to the port constructor.
	Port string
	// Kind identifies the pattern kind that failed to build: "rest", "event", "reqreply", or "mcp".
	Kind string
	// Err is the underlying error from the rest/events/reqreply/mcp package.
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

// CacheKeyError is returned by [Cache.BuildKey] when the key template names
// a {var} placeholder that is missing from the supplied vars map.
type CacheKeyError struct {
	// Key is the declared key template (e.g. "user:{id}").
	Key string
	// Var is the placeholder name that has no entry in vars.
	Var string
}

func (e CacheKeyError) Error() string {
	return fmt.Sprintf("cache key %q: missing var %q", e.Key, e.Var)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e CacheKeyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
		slog.String("var", e.Var),
	)
}
