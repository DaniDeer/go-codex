package file

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// This file implements api/file's "full parity" Declarative Middleware
// mechanism: a Bound<Role>Middleware[T, In, Out] type PER ROLE (mirrors
// [github.com/DaniDeer/go-codex/api/events.BoundSubscribeMiddleware]/
// [BoundPublishMiddleware]'s exact fuse-declare-and-implement shape),
// attached via that role's own XxxBoundMW(bm any) method, dispatched via
// middleware.go's runBoundMiddleware (called by every xxxCore function
// in dispatch.go BEFORE any Backend call).
//
// Read/Create/Patch/List keep a *T parameter on Fn (a natural singular
// domain value exists for these 4 roles — mirrors events' msg *T
// exactly, modulo the Read/List nil-at-invoke-time caveat documented on
// roleBoundMW). Delete/Exists/Copy/Move/Mkdir/BatchDelete drop it (no
// single domain value exists for these 6 roles) — a well-justified,
// structurally-necessary adaptation, not an oversight.

// ── Read ─────────────────────────────────────────────────────────────────

// BoundReadMiddleware is Read's bound middleware — see this file's own
// package-level doc comment for the shared design.
type BoundReadMiddleware[T, In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, v *T, in In) (Out, error)
}

// NewBoundReadMiddleware builds a [BoundReadMiddleware] from a
// [middleware.Declaration] and its Fn.
func NewBoundReadMiddleware[T, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, v *T, in In) (Out, error)) BoundReadMiddleware[T, In, Out] {
	return BoundReadMiddleware[T, In, Out]{mw: NewMiddleware(decl), fn: fn}
}

// BoundSecurityReadMiddleware builds a Security-carrying
// [BoundReadMiddleware] — the ONLY way to satisfy a declared
// Reader.WithSecurity requirement (mirrors
// [github.com/DaniDeer/go-codex/api/events.BoundSecuritySubscribeMiddleware]'s
// identical rationale). **Out MUST carry a field literally named
// `GrantedScopes map[string][]string`** — see [grantedScopesOf]'s doc
// comment.
func BoundSecurityReadMiddleware[T, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, v *T, in In) (Out, error)) BoundReadMiddleware[T, In, Out] {
	return BoundReadMiddleware[T, In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name:     "declare-security:" + schemeName,
			InCodec:  codex.Struct[In](),
			OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundReadMiddleware[T, In, Out]) invoke(ctx context.Context, v any) (map[string][]string, error) {
	var in In
	var tp *T
	if v != nil {
		tp, _ = v.(*T)
	}
	out, err := bm.fn(ctx, tp, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundReadMiddleware[T, In, Out]) boundName() string { return bm.mw.Name }

func (bm BoundReadMiddleware[T, In, Out]) boundTWitness(T) {}
func (bm BoundReadMiddleware[T, In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

type readBoundMWShape[T any] interface {
	invoke(ctx context.Context, v any) (map[string][]string, error)
	boundName() string
	boundSecurity() *middleware.SecurityDeclaration
	// boundTWitness is a deliberate, never-called no-op whose SOLE
	// purpose is making T appear in a method SIGNATURE — without it,
	// EVERY Bound<Role>Middleware[X,...] would satisfy readBoundMWShape[Y] for
	// ANY X, Y (a real, previously-documented bug class — see
	// internal/middleware/bound.go's BoundContributor doc comment).
	boundTWitness(T)
}

// ReadBoundMW attaches bm — a [BoundReadMiddleware][T, In, Out] value
// whose T matches THIS Reader's own T type parameter — mirrors
// [github.com/DaniDeer/go-codex/api/events.Subscriber.SubscribeBoundMW]'s
// identical any-accepting, interface-asserted attachment (a mismatch is
// recorded on roleCore.buildErr, surfaced at .Handle() time, never
// panicked).
func (r Reader[T]) ReadBoundMW(bm any) Reader[T] {
	v, ok := bm.(readBoundMWShape[T])
	if !ok {
		r.core.buildErr = boundMismatchErr(r.core, bm)
		return r
	}
	r.core.boundMWs = append(append([]roleBoundMW{}, r.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return r
}

// boundMismatchErr builds a [BoundMiddlewareReqMismatchError] for bm,
// preserving the FIRST recorded buildErr (mirrors events' identical
// "first error wins" semantics).
func boundMismatchErr[T any](core roleCore[T], bm any) error {
	if core.buildErr != nil {
		return core.buildErr
	}
	name := ""
	if n, ok := bm.(interface{ boundName() string }); ok {
		name = n.boundName()
	}
	return BoundMiddlewareReqMismatchError{Path: core.resource.template, Got: bm, Name: name}
}

// ── Create ───────────────────────────────────────────────────────────────

type BoundCreateMiddleware[T, In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, v *T, in In) (Out, error)
}

func NewBoundCreateMiddleware[T, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, v *T, in In) (Out, error)) BoundCreateMiddleware[T, In, Out] {
	return BoundCreateMiddleware[T, In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityCreateMiddleware[T, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, v *T, in In) (Out, error)) BoundCreateMiddleware[T, In, Out] {
	return BoundCreateMiddleware[T, In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundCreateMiddleware[T, In, Out]) invoke(ctx context.Context, v any) (map[string][]string, error) {
	var in In
	var tp *T
	if v != nil {
		tp, _ = v.(*T)
	}
	out, err := bm.fn(ctx, tp, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundCreateMiddleware[T, In, Out]) boundName() string { return bm.mw.Name }

func (bm BoundCreateMiddleware[T, In, Out]) boundTWitness(T) {}
func (bm BoundCreateMiddleware[T, In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

type createBoundMWShape[T any] interface {
	invoke(ctx context.Context, v any) (map[string][]string, error)
	boundName() string
	boundSecurity() *middleware.SecurityDeclaration
	// boundTWitness is a deliberate, never-called no-op whose SOLE
	// purpose is making T appear in a method SIGNATURE — without it,
	// EVERY Bound<Role>Middleware[X,...] would satisfy createBoundMWShape[Y] for
	// ANY X, Y (a real, previously-documented bug class — see
	// internal/middleware/bound.go's BoundContributor doc comment).
	boundTWitness(T)
}

func (c Creator[T]) CreateBoundMW(bm any) Creator[T] {
	v, ok := bm.(createBoundMWShape[T])
	if !ok {
		c.core.buildErr = boundMismatchErr(c.core, bm)
		return c
	}
	c.core.boundMWs = append(append([]roleBoundMW{}, c.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return c
}

// ── Patch ────────────────────────────────────────────────────────────────

type BoundPatchMiddleware[T, In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, v *T, in In) (Out, error)
}

func NewBoundPatchMiddleware[T, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, v *T, in In) (Out, error)) BoundPatchMiddleware[T, In, Out] {
	return BoundPatchMiddleware[T, In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityPatchMiddleware[T, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, v *T, in In) (Out, error)) BoundPatchMiddleware[T, In, Out] {
	return BoundPatchMiddleware[T, In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundPatchMiddleware[T, In, Out]) invoke(ctx context.Context, v any) (map[string][]string, error) {
	var in In
	var tp *T
	if v != nil {
		tp, _ = v.(*T)
	}
	out, err := bm.fn(ctx, tp, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundPatchMiddleware[T, In, Out]) boundName() string { return bm.mw.Name }

func (bm BoundPatchMiddleware[T, In, Out]) boundTWitness(T) {}
func (bm BoundPatchMiddleware[T, In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

type patchBoundMWShape[T any] interface {
	invoke(ctx context.Context, v any) (map[string][]string, error)
	boundName() string
	boundSecurity() *middleware.SecurityDeclaration
	// boundTWitness is a deliberate, never-called no-op whose SOLE
	// purpose is making T appear in a method SIGNATURE — without it,
	// EVERY Bound<Role>Middleware[X,...] would satisfy patchBoundMWShape[Y] for
	// ANY X, Y (a real, previously-documented bug class — see
	// internal/middleware/bound.go's BoundContributor doc comment).
	boundTWitness(T)
}

func (p Patcher[T, P]) PatchBoundMW(bm any) Patcher[T, P] {
	v, ok := bm.(patchBoundMWShape[T])
	if !ok {
		p.core.buildErr = boundMismatchErr(p.core, bm)
		return p
	}
	p.core.boundMWs = append(append([]roleBoundMW{}, p.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return p
}

// ── List ─────────────────────────────────────────────────────────────────

type BoundListMiddleware[T, In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, v *T, in In) (Out, error)
}

func NewBoundListMiddleware[T, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, v *T, in In) (Out, error)) BoundListMiddleware[T, In, Out] {
	return BoundListMiddleware[T, In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityListMiddleware[T, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, v *T, in In) (Out, error)) BoundListMiddleware[T, In, Out] {
	return BoundListMiddleware[T, In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundListMiddleware[T, In, Out]) invoke(ctx context.Context, v any) (map[string][]string, error) {
	var in In
	var tp *T
	if v != nil {
		tp, _ = v.(*T)
	}
	out, err := bm.fn(ctx, tp, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundListMiddleware[T, In, Out]) boundName() string { return bm.mw.Name }

func (bm BoundListMiddleware[T, In, Out]) boundTWitness(T) {}
func (bm BoundListMiddleware[T, In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

type listBoundMWShape[T any] interface {
	invoke(ctx context.Context, v any) (map[string][]string, error)
	boundName() string
	boundSecurity() *middleware.SecurityDeclaration
	// boundTWitness is a deliberate, never-called no-op whose SOLE
	// purpose is making T appear in a method SIGNATURE — without it,
	// EVERY Bound<Role>Middleware[X,...] would satisfy listBoundMWShape[Y] for
	// ANY X, Y (a real, previously-documented bug class — see
	// internal/middleware/bound.go's BoundContributor doc comment).
	boundTWitness(T)
}

func (l Lister[T]) ListBoundMW(bm any) Lister[T] {
	v, ok := bm.(listBoundMWShape[T])
	if !ok {
		l.core.buildErr = boundMismatchErr(l.core, bm)
		return l
	}
	l.core.boundMWs = append(append([]roleBoundMW{}, l.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return l
}

// ── No-T roles: Delete/Exists/Copy/Move/Mkdir/BatchDelete ───────────────
//
// These 6 roles have no single domain value to offer a bound
// middleware's Fn — Fn drops the *T parameter entirely.

// BoundDeleteMiddleware is Delete's bound middleware (no *T — see this
// file's own package-level doc comment for why).
type BoundDeleteMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundDeleteMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundDeleteMiddleware[In, Out] {
	return BoundDeleteMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityDeleteMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundDeleteMiddleware[In, Out] {
	return BoundDeleteMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundDeleteMiddleware[In, Out]) invoke(ctx context.Context, _ any) (map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundDeleteMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundDeleteMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

type noTBoundMWShape interface {
	invoke(ctx context.Context, v any) (map[string][]string, error)
	boundName() string
	boundSecurity() *middleware.SecurityDeclaration
}

func (d Deleter[T]) DeleteBoundMW(bm any) Deleter[T] {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		d.core.buildErr = boundMismatchErr(d.core, bm)
		return d
	}
	d.core.boundMWs = append(append([]roleBoundMW{}, d.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return d
}

// BoundExistsMiddleware is Exists' bound middleware (no *T).
type BoundExistsMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundExistsMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundExistsMiddleware[In, Out] {
	return BoundExistsMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityExistsMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundExistsMiddleware[In, Out] {
	return BoundExistsMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundExistsMiddleware[In, Out]) invoke(ctx context.Context, _ any) (map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundExistsMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundExistsMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

func (e Exister[T]) ExistsBoundMW(bm any) Exister[T] {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		e.core.buildErr = boundMismatchErr(e.core, bm)
		return e
	}
	e.core.boundMWs = append(append([]roleBoundMW{}, e.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return e
}

// BoundCopyMiddleware is Copy's bound middleware (no *T).
type BoundCopyMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundCopyMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundCopyMiddleware[In, Out] {
	return BoundCopyMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityCopyMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundCopyMiddleware[In, Out] {
	return BoundCopyMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundCopyMiddleware[In, Out]) invoke(ctx context.Context, _ any) (map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundCopyMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundCopyMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

func (c Copier[T]) CopyBoundMW(bm any) Copier[T] {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		c.core.buildErr = boundMismatchErr(c.core, bm)
		return c
	}
	c.core.boundMWs = append(append([]roleBoundMW{}, c.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return c
}

// BoundMoveMiddleware is Move's bound middleware (no *T).
type BoundMoveMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundMoveMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundMoveMiddleware[In, Out] {
	return BoundMoveMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityMoveMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundMoveMiddleware[In, Out] {
	return BoundMoveMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundMoveMiddleware[In, Out]) invoke(ctx context.Context, _ any) (map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundMoveMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundMoveMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

func (m Mover[T]) MoveBoundMW(bm any) Mover[T] {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		m.core.buildErr = boundMismatchErr(m.core, bm)
		return m
	}
	m.core.boundMWs = append(append([]roleBoundMW{}, m.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return m
}

// BoundMkdirMiddleware is Mkdir's bound middleware (no *T).
type BoundMkdirMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundMkdirMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundMkdirMiddleware[In, Out] {
	return BoundMkdirMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityMkdirMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundMkdirMiddleware[In, Out] {
	return BoundMkdirMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundMkdirMiddleware[In, Out]) invoke(ctx context.Context, _ any) (map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundMkdirMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundMkdirMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

func (m MkdirCreator[T]) MkdirBoundMW(bm any) MkdirCreator[T] {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		m.core.buildErr = boundMismatchErr(m.core, bm)
		return m
	}
	m.core.boundMWs = append(append([]roleBoundMW{}, m.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return m
}

// BoundBatchDeleteMiddleware is BatchDelete's bound middleware (no *T —
// BatchDelete operates on a LIST of paths, not one single value).
type BoundBatchDeleteMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundBatchDeleteMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundBatchDeleteMiddleware[In, Out] {
	return BoundBatchDeleteMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityBatchDeleteMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundBatchDeleteMiddleware[In, Out] {
	return BoundBatchDeleteMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundBatchDeleteMiddleware[In, Out]) invoke(ctx context.Context, _ any) (map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	return grantedScopesOf(out), nil
}
func (bm BoundBatchDeleteMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundBatchDeleteMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

func (b BatchDeleter[T]) BatchDeleteBoundMW(bm any) BatchDeleter[T] {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		b.core.buildErr = boundMismatchErr(b.core, bm)
		return b
	}
	b.core.boundMWs = append(append([]roleBoundMW{}, b.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return b
}
