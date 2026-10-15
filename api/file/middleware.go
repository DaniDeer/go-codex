package file

import (
	"context"
	"reflect"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
)

// SecurityScheme mirrors [route.SecurityScheme]'s shape — see
// [github.com/DaniDeer/go-codex/api/events.SecurityScheme]'s identical
// aliasing rationale.
type SecurityScheme = route.SecurityScheme

// Declaration is api/file's public alias for the shared
// [middleware.Declaration] — mirrors
// [github.com/DaniDeer/go-codex/api/events.Declaration]'s identical
// aliasing rationale (an internal/* type must have a per-pattern public
// alias for any real external caller — here, a test or caller building a
// [Middleware] by hand — to spell its type, per this skill's own
// mandatory-requirement #1).
type Declaration[In, Out any] = middleware.Declaration[In, Out]

// NewDeclaration builds a [Declaration] from a name and its Input/Output
// codecs — mirrors [github.com/DaniDeer/go-codex/api/events.NewDeclaration].
func NewDeclaration[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out]) Declaration[In, Out] {
	return middleware.NewDeclaration(name, inCodec, outCodec)
}

// Middleware[In, Out] is api/file's codec-backed middleware declaration —
// mirrors [github.com/DaniDeer/go-codex/api/events.Middleware][In,Out]'s
// Declaration-embedding shape. Declare-time ONLY — carries no Fn; a Fn
// is embedded at construction by [NewBoundReadMiddleware]/
// [BoundSecurityReadMiddleware] (and each role's own equivalent pair) —
// see those for the attach-and-run half.
//
// UNLIKE events' Middleware[In,Out], this type does NOT (yet) offer
// metadata-merge-field methods (a WithMetadataIn/WithMetadataOut
// analogue of events' WithSubscribeTopic/WithSubscribeProperty):
// investigated and found a genuine ordering conflict specific to file —
// Read/List's metadata is only available AFTER the Backend call that
// decodes the resource, but BoundMiddleware's Security gate must run
// BEFORE any Backend call (the established "fail before backend"
// convention). Rather than ship a WithMetadataIn that silently never
// actually populates In for Read/List, this is left as an explicitly
// named, deferred follow-up — see docs/roadmap/file-api.md's "Open
// design decisions." The METADATA AXIS ITSELF (MetadataParam/
// MergedMetadataParam, Resource[T]'s own metadataMergeFields) is fully
// shipped and working (Stage M1) — only its INTEGRATION into
// BoundMiddleware's In/Out is deferred.
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]
}

// NewMiddleware builds a [Middleware] from a [middleware.Declaration].
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out] {
	return Middleware[In, Out]{Declaration: decl}
}

// RouteMiddlewareMarker makes Middleware satisfy [middleware.RouteMiddleware].
func (m Middleware[In, Out]) RouteMiddlewareMarker() {}

// SecurityDeclaration makes Middleware satisfy [middleware.SecurityCarrier].
func (m Middleware[In, Out]) SecurityDeclaration() *middleware.SecurityDeclaration {
	return m.Declaration.Security
}

var (
	_ middleware.RouteMiddleware = Middleware[struct{}, struct{}]{}
	_ middleware.SecurityCarrier = Middleware[struct{}, struct{}]{}
)

// grantedScopesOf extracts out's conventionally-named `GrantedScopes
// map[string][]string` field via reflection — mirrors every other
// pattern's (REST/events/reqreply) identical convention exactly (see
// [BoundSecurityReadMiddleware]'s own doc comment for the full writeup).
// Returns nil when out has no such field, or the field isn't the right
// type — NOT an error (a plain, non-Security BoundMiddleware's Out has
// no reason to carry this field at all).
func grantedScopesOf(out any) map[string][]string {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("GrantedScopes")
	if !f.IsValid() {
		return nil
	}
	gs, ok := f.Interface().(map[string][]string)
	if !ok {
		return nil
	}
	return gs
}

// roleBoundMW is the NON-generic, storable bridge every
// Bound<Role>Middleware[T, In, Out] value reduces to once attached via
// its role's own `XxxBoundMW` method — mirrors
// [github.com/DaniDeer/go-codex/api/events]'s MiddlewareHandler bridge
// type, adapted for file's simpler (metadata-only, no topic/property)
// axis.
//
// invoke's `v any` is the role's own `*T` (Read/Create/Patch/List) or
// nil (Delete/Exists/Mkdir/Copy/Move/BatchDelete — these 6 roles have no
// single domain value to offer; see each Bound<Role>Middleware's own
// doc comment for why). invoke runs BEFORE any Backend call for every
// role (the established "fail before backend" convention Capability
// already set) — for Read/List specifically this means v is ALWAYS nil
// at invoke time too (the decoded value doesn't exist yet), a disclosed,
// intentional divergence from events.BoundSubscribeMiddleware's msg *T
// (always populated there, since events decodes BEFORE dispatching to
// any middleware); a Security-gating BoundReadMiddleware validates the
// REQUEST (credentials/scopes), never the response body — same
// separation REST's own request-time Security gate already has.
type roleBoundMW struct {
	name     string
	security *middleware.SecurityDeclaration
	invoke   func(ctx context.Context, v any) (map[string][]string, error)
}

// runBoundMiddleware executes every bound middleware attached to core,
// in attachment order, collecting GrantedScopes from each, then checks
// core's own declared Security requirements against the merged result —
// called once per dispatch, BEFORE any Backend call, by every xxxCore
// function in dispatch.go. A no-op (nil error, immediately) when core
// has neither bound middleware nor declared Security requirements — the
// common case, zero overhead.
func runBoundMiddleware[T any](ctx context.Context, path string, core roleCore[T], v any) error {
	if len(core.boundMWs) == 0 {
		if len(core.security) == 0 {
			return nil
		}
		return SecurityRejectedError{Path: path, Err: middleware.CheckScopes(core.security, nil)}
	}
	granted := map[string][]string{}
	for _, bmw := range core.boundMWs {
		gs, err := bmw.invoke(ctx, v)
		if err != nil {
			return err
		}
		for scheme, scopes := range gs {
			granted[scheme] = append(granted[scheme], scopes...)
		}
	}
	if len(core.security) == 0 {
		return nil
	}
	if err := middleware.CheckScopes(core.security, granted); err != nil {
		return SecurityRejectedError{Path: path, Err: err}
	}
	return nil
}
