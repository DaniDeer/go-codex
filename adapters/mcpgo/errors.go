package mcpgo

import (
	"fmt"
	"log/slog"
)

// InvalidPromptRoleError is returned by [PromptHandler]'s handler when an
// application-supplied [PromptMessage].Role is neither "user" nor
// "assistant" — the only two values [PromptMessage]'s own doc comment
// documents as valid. Previously, any OTHER value silently fell back to
// [mcp.RoleUser] with no error at all (a confirmed, real bug — see
// deep-dive-bug-hunt's api/mcp Prompt round): a typo'd or unsupported role
// (e.g. "system", or "Assistant" miscapitalized) would silently be sent to
// the LLM client as if the application had explicitly said "user".
//
// Use [errors.As] to extract the invalid value:
//
//	var ipr mcpgo.InvalidPromptRoleError
//	if errors.As(err, &ipr) {
//	    log.Printf("prompt message role %q is not \"user\" or \"assistant\"", ipr.Role)
//	}
type InvalidPromptRoleError struct {
	// Role is the invalid value supplied.
	Role string
}

func (e InvalidPromptRoleError) Error() string {
	return fmt.Sprintf("mcpgo: prompt message role %q must be \"user\" or \"assistant\"", e.Role)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e InvalidPromptRoleError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("role", e.Role))
}
