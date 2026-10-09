package middleware_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
)

// fakeBoundRouteBuilder is a minimal, in-isolation [middleware.BoundRouteBuilder]
// implementation — records every append call for assertion, independent
// of any specific api package's real routeBuilder/Subscriber/Publisher.
type fakeBoundRouteBuilder struct {
	middlewareHandlers       []any
	clientMiddlewareHandlers []any
	specContributions        []any
	securityDeclarations     []struct {
		name string
		sec  *middleware.SecurityDeclaration
	}
}

func (b *fakeBoundRouteBuilder) AppendMiddlewareHandler(h any) {
	b.middlewareHandlers = append(b.middlewareHandlers, h)
}

func (b *fakeBoundRouteBuilder) AppendClientMiddlewareHandler(h any) {
	b.clientMiddlewareHandlers = append(b.clientMiddlewareHandlers, h)
}

func (b *fakeBoundRouteBuilder) AppendSpecContribution(c any) {
	b.specContributions = append(b.specContributions, c)
}

func (b *fakeBoundRouteBuilder) AppendSecurityDeclaration(name string, sec *middleware.SecurityDeclaration) {
	b.securityDeclarations = append(b.securityDeclarations, struct {
		name string
		sec  *middleware.SecurityDeclaration
	}{name, sec})
}

var _ middleware.BoundRouteBuilder = (*fakeBoundRouteBuilder)(nil)

// fakeBoundContributor is a minimal [middleware.BoundContributor][int]
// implementation (Req=int, the simplest possible discriminator type).
type fakeBoundContributor struct {
	name string
	sec  *middleware.SecurityDeclaration
}

func (f fakeBoundContributor) ApplyBoundRoute(rb middleware.BoundRouteBuilder) {
	rb.AppendMiddlewareHandler("handler:" + f.name)
	if f.sec != nil {
		rb.AppendSecurityDeclaration(f.name, f.sec)
	}
}

func (f fakeBoundContributor) BoundReqWitness(int) {}

func (f fakeBoundContributor) MiddlewareName() string { return f.name }

var (
	_ middleware.BoundContributor[int] = fakeBoundContributor{}
	_ middleware.BoundNamed            = fakeBoundContributor{}
)

// fakeBoundClientContributor is a minimal
// [middleware.BoundClientContributor][int] implementation.
type fakeBoundClientContributor struct{ name string }

func (f fakeBoundClientContributor) ApplyBoundClientRoute(rb middleware.BoundRouteBuilder) {
	rb.AppendClientMiddlewareHandler("client-handler:" + f.name)
}

func (f fakeBoundClientContributor) BoundReqWitness(int) {}

var _ middleware.BoundClientContributor[int] = fakeBoundClientContributor{}

func TestBoundContributor_ApplyBoundRoute_AppendsMiddlewareHandler(t *testing.T) {
	var b fakeBoundRouteBuilder
	c := fakeBoundContributor{name: "auth"}
	c.ApplyBoundRoute(&b)

	if len(b.middlewareHandlers) != 1 || b.middlewareHandlers[0] != "handler:auth" {
		t.Fatalf("middlewareHandlers = %v, want [handler:auth]", b.middlewareHandlers)
	}
	if len(b.securityDeclarations) != 0 {
		t.Fatalf("securityDeclarations = %v, want none (no Security declared)", b.securityDeclarations)
	}
}

func TestBoundContributor_ApplyBoundRoute_AppendsSecurityDeclarationWhenPresent(t *testing.T) {
	sec := middleware.NewSecurityDeclaration("bearerAuth", route.BearerScheme("JWT"), []string{"read"}, nil)
	var b fakeBoundRouteBuilder
	c := fakeBoundContributor{name: "auth", sec: sec}
	c.ApplyBoundRoute(&b)

	if len(b.securityDeclarations) != 1 {
		t.Fatalf("securityDeclarations = %v, want 1 entry", b.securityDeclarations)
	}
	if b.securityDeclarations[0].name != "auth" || b.securityDeclarations[0].sec != sec {
		t.Errorf("securityDeclarations[0] = %+v, want {name: auth, sec: %p}", b.securityDeclarations[0], sec)
	}
}

func TestBoundClientContributor_ApplyBoundClientRoute_AppendsClientMiddlewareHandler(t *testing.T) {
	var b fakeBoundRouteBuilder
	c := fakeBoundClientContributor{name: "auth"}
	c.ApplyBoundClientRoute(&b)

	if len(b.clientMiddlewareHandlers) != 1 || b.clientMiddlewareHandlers[0] != "client-handler:auth" {
		t.Fatalf("clientMiddlewareHandlers = %v, want [client-handler:auth]", b.clientMiddlewareHandlers)
	}
}

func TestBoundNameOf_ReturnsNameWhenBoundNamed(t *testing.T) {
	c := fakeBoundContributor{name: "my-scheme"}
	if got := middleware.BoundNameOf(c); got != "my-scheme" {
		t.Errorf("BoundNameOf(c) = %q, want %q", got, "my-scheme")
	}
}

func TestBoundNameOf_ReturnsEmptyWhenNotBoundNamed(t *testing.T) {
	// fakeBoundClientContributor does NOT implement MiddlewareName() —
	// confirms the graceful "" fallback, not a panic.
	c := fakeBoundClientContributor{name: "my-scheme"}
	if got := middleware.BoundNameOf(c); got != "" {
		t.Errorf("BoundNameOf(c) = %q, want empty (c does not implement BoundNamed)", got)
	}
}

func TestBoundNameOf_ReturnsEmptyForNil(t *testing.T) {
	if got := middleware.BoundNameOf(nil); got != "" {
		t.Errorf("BoundNameOf(nil) = %q, want empty", got)
	}
}

// TestBoundContributor_ReqWitness_DiscriminatesOnReq confirms the
// BoundReqWitness discriminator trick actually works: a value satisfying
// BoundContributor[int] must NOT satisfy BoundContributor[string] — this
// is the exact bug class (see middleware.BoundContributor's own doc
// comment) the witness method exists to prevent.
func TestBoundContributor_ReqWitness_DiscriminatesOnReq(t *testing.T) {
	var v any = fakeBoundContributor{name: "auth"}
	if _, ok := v.(middleware.BoundContributor[int]); !ok {
		t.Fatalf("fakeBoundContributor must satisfy BoundContributor[int]")
	}
	if _, ok := v.(middleware.BoundContributor[string]); ok {
		t.Fatalf("fakeBoundContributor must NOT satisfy BoundContributor[string] — BoundReqWitness(int) should discriminate against string")
	}
}
