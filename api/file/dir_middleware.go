package file

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// BoundListDirMiddleware is ListDir's bound middleware — no-T shape,
// mirrors [BoundDeleteMiddleware] exactly ([Dir] has no single domain
// value to offer any role, unlike Read/Create/Patch/List's own *T).
type BoundListDirMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundListDirMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundListDirMiddleware[In, Out] {
	return BoundListDirMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityListDirMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundListDirMiddleware[In, Out] {
	return BoundListDirMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundListDirMiddleware[In, Out]) invoke(ctx context.Context, _ any, _ map[string]string) (map[string]string, map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	return nil, grantedScopesOf(out), nil
}
func (bm BoundListDirMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundListDirMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

// ListDirBoundMW attaches bm — a type mismatch records l.core.buildErr,
// surfaced later by [ListDirer.Handle].
func (l ListDirer) ListDirBoundMW(bm any) ListDirer {
	v, ok := bm.(noTBoundMWShape)
	if !ok {
		l.core.buildErr = boundMismatchErr(l.core, bm)
		return l
	}
	l.core.boundMWs = append(append([]roleBoundMW{}, l.core.boundMWs...), roleBoundMW{
		name: v.boundName(), security: v.boundSecurity(), invoke: v.invoke,
	})
	return l
}

// BoundDeleteDirMiddleware is DeleteDir's bound middleware — no-T shape,
// mirrors [BoundDeleteMiddleware] exactly.
type BoundDeleteDirMiddleware[In, Out any] struct {
	mw Middleware[In, Out]
	fn func(ctx context.Context, in In) (Out, error)
}

func NewBoundDeleteDirMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundDeleteDirMiddleware[In, Out] {
	return BoundDeleteDirMiddleware[In, Out]{mw: NewMiddleware(decl), fn: fn}
}

func BoundSecurityDeleteDirMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundDeleteDirMiddleware[In, Out] {
	return BoundDeleteDirMiddleware[In, Out]{
		mw: NewMiddleware(middleware.Declaration[In, Out]{
			Name: "declare-security:" + schemeName, InCodec: codex.Struct[In](), OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme, scopes, nil),
		}),
		fn: fn,
	}
}

func (bm BoundDeleteDirMiddleware[In, Out]) invoke(ctx context.Context, _ any, _ map[string]string) (map[string]string, map[string][]string, error) {
	var in In
	out, err := bm.fn(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	return nil, grantedScopesOf(out), nil
}
func (bm BoundDeleteDirMiddleware[In, Out]) boundName() string { return bm.mw.Name }
func (bm BoundDeleteDirMiddleware[In, Out]) boundSecurity() *middleware.SecurityDeclaration {
	return bm.mw.Security
}

// DeleteDirBoundMW attaches bm — a type mismatch records d.core.buildErr,
// surfaced later by [DeleteDirer.Handle].
func (d DeleteDirer) DeleteDirBoundMW(bm any) DeleteDirer {
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
