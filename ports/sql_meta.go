package ports

import "context"

// sqlMetaCtxKey is the context key used by [WithSQLMeta] / [SQLMetaFromContext].
type sqlMetaCtxKey struct{}

// WithSQLMeta returns a copy of ctx carrying m, retrievable via
// [SQLMetaFromContext]. [SourcePort.Bind], [SinkPort.Bind], [ToolPort.Bind],
// and [IOPort.Connect] call this automatically with the port's declared
// [SQLPattern] before invoking the bound adapter, so the sql adapters can
// default their options' Table/Op fields from the port's declaration without
// a direct dependency on the port.
func WithSQLMeta(ctx context.Context, m SQLPattern) context.Context {
	return context.WithValue(ctx, sqlMetaCtxKey{}, m)
}

// SQLMetaFromContext returns the [SQLPattern] stored by [WithSQLMeta] and
// true, or (zero, false) when none was stored (e.g. the port declared no
// [SQLPattern]).
func SQLMetaFromContext(ctx context.Context) (SQLPattern, bool) {
	m, ok := ctx.Value(sqlMetaCtxKey{}).(SQLPattern)
	return m, ok
}

// adapterContext wraps ctx with the port's declared [IOParam] slice and, when
// the port declared a [SQLPattern], with that metadata — the single place
// where port declarations become adapter-visible context values.
func adapterContext(ctx context.Context, params []IOParam, handles map[string]any) context.Context {
	ctx = WithParams(ctx, params)
	if m, ok := handles[patternKindSQL].(SQLPattern); ok {
		ctx = WithSQLMeta(ctx, m)
	}
	return ctx
}
