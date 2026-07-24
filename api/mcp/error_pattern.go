package mcp

import (
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// ErrorPatternResponse is the adapter-ready payload produced by
// [ToolHandle.ErrorResponseFor] when a declared [ErrorPattern] matches.
type ErrorPatternResponse struct {
	// Body is the JSON-encoded typed error payload.
	Body []byte
	// Value is the typed payload before encoding.
	Value any
}

// errorPatternRule is the type-erased runtime form of a declared
// [ErrorPattern], stored on [toolBuilder]/[ToolHandle].
type errorPatternRule struct {
	match func(error) (ErrorPatternResponse, bool, error)
}

// ErrorPatternOpt is the [ToolOpt] value returned by [ErrorPattern].
type ErrorPatternOpt[E error, B any] struct {
	codec  codex.Codec[B]
	mapper func(E) (B, error)
}

// ErrorPattern declares a codec-backed typed error result for a matched
// handler error type — the MCP tool analogue of [rest.ErrorPattern] and
// [events.ErrorChannel]. MCP tool results have no HTTP status/reply topic;
// the declaration simply says "when the handler returns an error matching
// E, structure the tool's error result as this typed JSON payload instead
// of a bare error string."
//
// Two modes, mirroring [rest.ErrorPattern]:
//   - Direct: no mapFn provided, E must be assignable to B.
//   - Mapped: mapFn(E) produces B.
//
// Matching is type-only via [errors.As]; the first declared ErrorPattern (in
// [NewTool] option order) whose type matches wins — the same deterministic
// precedence used by REST/events/reqreply.
//
// ErrorPattern only applies to errors returned by the application handler
// function (business logic) — input-decode failures and output-encode
// failures are different concerns and are not affected by ErrorPattern.
//
//	mcp.NewTool[SearchIn, SearchOut]("search", inCodec, outCodec,
//	    mcp.ErrorPattern[domain.NotFoundError, ErrorPayload](errorPayloadCodec,
//	        func(e domain.NotFoundError) (ErrorPayload, error) {
//	            return ErrorPayload{Code: "not_found", Message: e.Error()}, nil
//	        },
//	    ),
//	)
func ErrorPattern[E error, B any](
	codec codex.Codec[B],
	mapFn ...func(E) (B, error),
) ErrorPatternOpt[E, B] {
	var mapper func(E) (B, error)
	if len(mapFn) > 0 {
		mapper = mapFn[0]
	}
	return ErrorPatternOpt[E, B]{codec: codec, mapper: mapper}
}

func (o ErrorPatternOpt[E, B]) applyTool(tb *toolBuilder) {
	jsonCodec := format.JSON(o.codec)
	mapper := o.mapper
	rule := errorPatternRule{
		match: func(err error) (ErrorPatternResponse, bool, error) {
			var target E
			if !errors.As(err, &target) {
				return ErrorPatternResponse{}, false, nil
			}

			var (
				payload B
				ok      bool
			)
			if mapper != nil {
				mapped, mapErr := mapper(target)
				if mapErr != nil {
					return ErrorPatternResponse{}, true, mapErr
				}
				payload = mapped
			} else {
				payload, ok = any(target).(B)
				if !ok {
					return ErrorPatternResponse{}, true,
						fmt.Errorf("api/mcp: ErrorPattern direct mode: %T not assignable to payload", target)
				}
			}

			body, encErr := jsonCodec.Marshal(payload)
			if encErr != nil {
				return ErrorPatternResponse{}, true, encErr
			}
			return ErrorPatternResponse{Body: body, Value: payload}, true, nil
		},
	}
	tb.errorPatternRules = append(tb.errorPatternRules, rule)
}

// ErrorResponseFor returns the first declared [ErrorPattern] match for err
// (matching via [errors.As], in declaration order), or
// (ErrorPatternResponse{}, false, nil) when none match.
//
// A non-nil third return value indicates the matched pattern's mapping or
// encoding failed — callers should treat this as a terminal error for that
// pattern (do not fall through to other patterns).
func (h *ToolHandle[In, Out]) ErrorResponseFor(err error) (ErrorPatternResponse, bool, error) {
	if err == nil {
		return ErrorPatternResponse{}, false, nil
	}
	for _, rule := range h.errorPatternRules {
		if rule.match == nil {
			continue
		}
		resp, matched, matchErr := rule.match(err)
		if !matched {
			continue
		}
		return resp, true, matchErr
	}
	return ErrorPatternResponse{}, false, nil
}
