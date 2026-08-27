package stats

import "context"

// diagnosticsKey is the unexported type for the Diagnostic sink stored in
// context by [WithDiagnostics].
type diagnosticsKey struct{}

// Diagnostic is one decode/validate-time observability event, ferried out of
// a codec decode/validate pipeline via ctx instead of a direct [Observer]
// call — for boundaries whose decode pipeline runs in multiple sequential
// stages (e.g. query → cookie → header → path → body), where a
// wrapping-only middleware structurally cannot observe an intermediate
// stage's failure the way it can observe the terminal (T, error) outcome.
type Diagnostic struct {
	// Location identifies the data source (e.g. "query", "cookie", "header",
	// "path", "body").
	Location string
	// ConstraintName is the constraint identifier (e.g. "minLen(3)",
	// "non-negative-int", "email", "type-mismatch", "required").
	ConstraintName string
	// Field is the field or parameter name from the structured error.
	Field string
}

// WithDiagnostics pre-allocates a ctx-carried Diagnostic sink and returns the
// resulting context — call once, at the top of a request/call handling
// pipeline, mirroring [nethttp.WithResponseHeaders]'s pre-allocation pattern
// exactly: a mutable slice is stored behind a pointer in ctx, so every
// [RecordDiagnostic] call from ANY nested Fn/middleware/decode stage appends
// to the SAME underlying slice, visible to whoever holds the returned ctx
// (or any context derived from it) once decoding completes.
func WithDiagnostics(ctx context.Context) context.Context {
	sink := new([]Diagnostic)
	return context.WithValue(ctx, diagnosticsKey{}, sink)
}

// RecordDiagnostic appends d to the sink allocated by [WithDiagnostics]. A
// no-op if ctx was never so decorated (mirrors [WithResponseHeaders]'s
// same no-op-when-absent safety).
func RecordDiagnostic(ctx context.Context, d Diagnostic) {
	if sink, ok := ctx.Value(diagnosticsKey{}).(*[]Diagnostic); ok {
		*sink = append(*sink, d)
	}
}

// DiagnosticsFromContext returns every Diagnostic recorded so far via
// [RecordDiagnostic]. Returns nil if ctx was never decorated by
// [WithDiagnostics].
func DiagnosticsFromContext(ctx context.Context) []Diagnostic {
	if sink, ok := ctx.Value(diagnosticsKey{}).(*[]Diagnostic); ok {
		return *sink
	}
	return nil
}
