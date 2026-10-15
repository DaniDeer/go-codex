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
// WithMetadataIn/WithMetadataOut populate/encode In/Out against the
// file-metadata axis (see [MetadataBackend]'s doc comment) — but ONLY
// for Create/Patch: their intended metadata is derivable directly from
// the caller-supplied value BEFORE any Backend call (via the same
// mechanism [WriteOptions.Metadata] already uses), so there's no
// ordering conflict for these 2 roles. Read/List's stored metadata is
// only available AFTER the Backend call that decodes the resource, but
// the Security gate must run BEFORE any Backend call — so In stays
// permanently zero-valued for Read/List's own BoundMiddleware (a
// disclosed, permanent asymmetry, not a bug — proven by
// TestBoundReadMiddleware_MetadataIn_AlwaysZeroValue), and
// WithMetadataOut on a Read/List-attached Middleware is accepted but has
// nothing to encode INTO (Read/List never write).
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	metadataFieldsIn  []codex.FieldCodec[In]
	metadataFieldsOut []codex.FieldCodec[Out]
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

// WithMetadataIn declares that p's metadata key should be decoded into
// In before this middleware's Fn runs — see [Middleware]'s own doc
// comment for which roles (Create/Patch only) actually populate it.
func (m Middleware[In, Out]) WithMetadataIn(p MergedMetadataParam[In]) Middleware[In, Out] {
	m.metadataFieldsIn = append(append([]codex.FieldCodec[In]{}, m.metadataFieldsIn...), p.Field)
	return m
}

// WithMetadataOut declares that Out's corresponding field should be
// encoded back into a metadata key once this middleware's Fn returns —
// see [Middleware]'s own doc comment for which roles (Create/Patch only)
// actually persist it.
func (m Middleware[In, Out]) WithMetadataOut(p MergedMetadataParam[Out]) Middleware[In, Out] {
	m.metadataFieldsOut = append(append([]codex.FieldCodec[Out]{}, m.metadataFieldsOut...), p.Field)
	return m
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
//
// md is the CALLER-INTENDED metadata (non-nil only for Create/Patch,
// derived from the resource's own metadataMergeFields via
// deriveWriteMetadata — see dispatch.go's runBoundMiddleware call
// sites); decoded into the bound middleware's own In via
// Middleware.WithMetadataIn, when declared. outMD is this middleware's
// OWN metadata CONTRIBUTION (encoded from Out via WithMetadataOut, when
// declared) — nil when the middleware declares no WithMetadataOut
// fields, or for any role where metadata doesn't apply.
type roleBoundMW struct {
	name     string
	security *middleware.SecurityDeclaration
	invoke   func(ctx context.Context, v any, md map[string]string) (outMD map[string]string, granted map[string][]string, err error)
}

// runBoundMiddleware executes every bound middleware attached to core,
// in attachment order, collecting GrantedScopes AND metadata
// contributions from each, then checks core's own declared Security
// requirements against the merged GrantedScopes — called once per
// dispatch, BEFORE any Backend call, by every xxxCore function in
// dispatch.go. Returns the merged metadata contribution (LAST-WINS on a
// key collision across multiple attached middlewares — mirrors
// ApplyCapabilities's identical "last-wins on a field collision"
// convention) for createCore/patchCore to layer over the resource's own
// derived metadata. A no-op (nil, nil immediately) when core has
// neither bound middleware nor declared Security requirements — the
// common case, zero overhead.
func runBoundMiddleware[T any](ctx context.Context, path string, core roleCore[T], v any, md map[string]string) (outMD map[string]string, err error) {
	if len(core.boundMWs) == 0 {
		if len(core.security) == 0 {
			return nil, nil
		}
		return nil, SecurityRejectedError{Path: path, Err: middleware.CheckScopes(core.security, nil)}
	}
	granted := map[string][]string{}
	merged := map[string]string{}
	for _, bmw := range core.boundMWs {
		bmwOutMD, gs, err := bmw.invoke(ctx, v, md)
		if err != nil {
			return nil, err
		}
		for scheme, scopes := range gs {
			granted[scheme] = append(granted[scheme], scopes...)
		}
		for k, val := range bmwOutMD {
			merged[k] = val
		}
	}
	if len(merged) == 0 {
		merged = nil
	}
	if len(core.security) == 0 {
		return merged, nil
	}
	if err := middleware.CheckScopes(core.security, granted); err != nil {
		return nil, SecurityRejectedError{Path: path, Err: err}
	}
	return merged, nil
}
