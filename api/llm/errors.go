package llm

import (
	"fmt"
	"log/slog"
)

// EmptyNameError is returned by [Call.Register] when the declared Call
// name is empty.
//
// Use [errors.As] to detect this case:
//
//	var ene llm.EmptyNameError
//	if errors.As(err, &ene) {
//	    log.Print("call name must not be empty")
//	}
type EmptyNameError struct{}

func (e EmptyNameError) Error() string {
	return "llm: call name must not be empty"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e EmptyNameError) LogValue() slog.Value {
	return slog.GroupValue()
}

// DuplicateNameError is returned by [Call.Register] when Name was already
// registered against the SAME [Builder].
//
// Use [errors.As] to extract Name:
//
//	var dne llm.DuplicateNameError
//	if errors.As(err, &dne) {
//	    log.Printf("call %q already registered", dne.Name)
//	}
type DuplicateNameError struct {
	// Name is the Call's name, as passed to [NewCall].
	Name string
}

func (e DuplicateNameError) Error() string {
	return fmt.Sprintf("llm: call %q already registered", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateNameError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("name", e.Name))
}

// SchemaRenderError is returned by [Call.Register]/[Call.ClientHandle] when
// rendering a declared request/response codec's JSON Schema fails.
//
// Use [errors.As] to extract Kind, Name, and the underlying error:
//
//	var sre llm.SchemaRenderError
//	if errors.As(err, &sre) {
//	    log.Printf("call %q: %s schema render failed: %v", sre.Name, sre.Kind, sre.Err)
//	}
type SchemaRenderError struct {
	// Kind is "request" or "response".
	Kind string
	// Name is the Call's name.
	Name string
	// Err is the underlying render error.
	Err error
}

func (e SchemaRenderError) Error() string {
	return fmt.Sprintf("llm: call %q: render %s schema: %s", e.Name, e.Kind, e.Err)
}
func (e SchemaRenderError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SchemaRenderError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind),
		slog.String("name", e.Name),
		slog.Any("cause", e.Err),
	)
}

// UserMessageTypeMismatchError is returned by [Call.Register]/
// [Call.ClientHandle] when a [UserMessage] option's own type parameter
// doesn't match the [Call]'s own Req type parameter — reachable in
// practice (unlike some other packages' defensive-only type-assertion
// checks): [UserMessageOpt][Req] is a SEPARATE generic type from
// [Call][Req, Resp], so nothing at compile time stops
// `NewCall[OrderReq, OrderResp](..., UserMessage[WrongReq](fn))` from
// type-checking.
//
// Use [errors.As] to extract Name, Want, and Got:
//
//	var umte llm.UserMessageTypeMismatchError
//	if errors.As(err, &umte) {
//	    log.Printf("call %q: UserMessage want %s, got %s", umte.Name, umte.Want, umte.Got)
//	}
type UserMessageTypeMismatchError struct {
	// Name is the Call's name.
	Name string
	// Want describes the expected func(Req) (string, error) type.
	Want string
	// Got describes the actual, mismatched type.
	Got string
}

func (e UserMessageTypeMismatchError) Error() string {
	return fmt.Sprintf("llm: call %q: UserMessage type mismatch: want %s, got %s", e.Name, e.Want, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UserMessageTypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("want", e.Want),
		slog.String("got", e.Got),
	)
}

// ConflictingSystemPromptError is returned by [Call.Register]/
// [Call.ClientHandle] when BOTH [SystemPrompt] and [SystemPromptFile] are
// declared on the same [Call] — previously silently resolved by letting
// [SystemPromptFile] win (applied after [SystemPrompt] in [Call]'s
// internal resolution order) with the [SystemPrompt] text discarded, no
// error or warning at all — a confirmed, real usability footgun (e.g. a
// caller who declares both during a refactor, forgetting to remove the
// old one, silently loses their FIRST intent with no signal).
//
// Use [errors.As] to detect this case:
//
//	var cspe llm.ConflictingSystemPromptError
//	if errors.As(err, &cspe) {
//	    log.Printf("call %q: both SystemPrompt and SystemPromptFile declared", cspe.Name)
//	}
type ConflictingSystemPromptError struct {
	// Name is the Call's name, as passed to [NewCall].
	Name string
}

func (e ConflictingSystemPromptError) Error() string {
	return fmt.Sprintf("llm: call %q: SystemPrompt and SystemPromptFile are mutually exclusive — declare only one", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingSystemPromptError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("name", e.Name))
}

// SystemPromptFileError is returned by [Call.Register]/[Call.ClientHandle]
// when [SystemPromptFile]'s path cannot be read.
type SystemPromptFileError struct {
	// Path is the file path passed to [SystemPromptFile].
	Path string
	// Err is the underlying os.ReadFile error.
	Err error
}

func (e SystemPromptFileError) Error() string {
	return fmt.Sprintf("llm: read system prompt file %q: %v", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e SystemPromptFileError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SystemPromptFileError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// ResponseDecodeError is returned by [CallHandle.DecodeResponse] when the raw
// LLM completion content fails to decode/validate through the response codec
// — a JSON syntax error, a type mismatch, or a Refine constraint failure.
//
// This is the error [adapters/openai]'s MaxRetries loop inspects to build the
// re-prompt message: the failure means the provider's own structured-outputs
// enforcement did not (or could not) guarantee a codec-valid response —
// belt-and-suspenders local validation caught what the JSON Schema alone
// could not (e.g. cross-field Refine constraints).
type ResponseDecodeError struct {
	// Name is the Call's name, as passed to [NewCall].
	Name string
	// Raw is the raw completion content that failed to decode.
	Raw []byte
	// Err is the underlying codec Decode error.
	Err error
}

func (e ResponseDecodeError) Error() string {
	return fmt.Sprintf("llm: call %q: decode response: %v", e.Name, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e ResponseDecodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ResponseDecodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("raw", string(e.Raw)),
		slog.Any("err", e.Err),
	)
}
