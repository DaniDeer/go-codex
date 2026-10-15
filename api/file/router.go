package file

// routingInfo carries the resolved path PREFIX a Handle was built with —
// the ONE construction point for Router-group composition (see
// docs/roadmap/file-api.md's "Router/Middleware integration" section):
// a Router-grouped role is registered via `.Handle(client,
// file.WithRouter(prefix))` ONCE, producing a Handle that already
// carries the resolved, prefixed path; every subsequent Client dispatch
// call reuses THAT Handle, never re-deriving the path.
//
// NOTE (Phase 1, reduced scope vs. the roadmap's fuller design): this is
// a SIMPLIFIED prefix-composition mechanism — a plain string prefix
// prepended at Handle time — rather than full integration with
// d-0008's internal/router.Router[Target]/Routable[Target] value type
// (the mechanism api/rest/api/events/api/reqreply's own Router.Group
// actually uses). It preserves the critical property the skill's
// Router-composition check cares about (exactly ONE path-construction
// point, no risk of a caller forgetting to re-supply routing per call)
// without yet wiring api/file into the shared Router[Target] machinery.
// Full d-0008 integration (a file.Router type implementing
// router.Routable) is tracked as follow-up work.
type routingInfo struct {
	prefix string
}

// HandleOpt configures a role's .Handle(client, opts...) call.
type HandleOpt interface{ applyHandle(*routingInfo) }

type withRouterOpt struct{ prefix string }

func (o withRouterOpt) applyHandle(r *routingInfo) { r.prefix = o.prefix }

// WithRouter composes prefix onto the built Handle's resolved path — the
// Router-grouped case; see routingInfo's doc comment for the full
// rationale and Phase 1 scope note.
func WithRouter(prefix string) HandleOpt { return withRouterOpt{prefix: prefix} }

// resolvedPath substitutes vars into template, after applying r's
// prefix (if any) — the ONE place Router-group composition happens.
func (r routingInfo) resolvedTemplate(template string) string {
	if r.prefix == "" {
		return template
	}
	return r.prefix + "/" + template
}
