package main

import (
	"context"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoBoundMiddlewareSplit contrasts the 2 classes of REAL, dispatch-
// time-EXECUTED Declarative Middleware (mirrors every other example's
// own demo_bound_middleware_split.go, applied to api/file's own
// BoundReadMiddleware mechanism):
//
//  1. A plain, non-Security, general-purpose BoundReadMiddleware
//     (Class 1 — "reusable") — never rejects, just observes.
//  2. A Security-gated BoundReadMiddleware (Class 2 — "bound") — the
//     SAME securedRead demo as before, now explicitly contrasted.
//  3. BOTH attached to the SAME role, in attachment order — proving
//     execution order via each Fn's own print statement.
func demoBoundMiddlewareSplit(ctx context.Context, client *file.Client, rt file.Router) {
	alice := map[string]string{"id": "alice"}

	// Class 1 — reusable, no Security requirement at all. Fn never
	// rejects; it just observes the dispatch. v is ALWAYS nil for a
	// Read role's own BoundMiddleware — the Security gate must run
	// BEFORE any Backend call, but a Read's decoded value only exists
	// AFTER one (see TestBoundReadMiddleware_MetadataIn_AlwaysZeroValue's
	// own documented asymmetry for the same reason, applied here to the
	// whole *T pointer, not just its metadata fields).
	auditLog := file.NewBoundReadMiddleware[resources.Note, struct{}, struct{}](
		file.NewDeclaration[struct{}, struct{}]("audit-log", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *resources.Note, in struct{}) (struct{}, error) {
			fmt.Println("  [audit-log middleware] observed a Read dispatch (v is nil pre-Backend-call, by design)")
			return struct{}{}, nil
		},
	)

	// Class 2 — Security-gated. The bound Fn's Out carries GrantedScopes;
	// api/file's dispatch core extracts it via reflection and checks it
	// against the declared Security requirement BEFORE calling the
	// Backend at all.
	securityGate := file.BoundSecurityReadMiddleware[resources.Note, struct{}, resources.GrantedScopes](
		"apiKey", file.SecurityScheme{}, []string{"read"},
		func(ctx context.Context, v *resources.Note, in struct{}) (resources.GrantedScopes, error) {
			fmt.Println("  [security middleware] granting \"read\" scope")
			return resources.GrantedScopes{GrantedScopes: map[string][]string{"apiKey": {"read"}}}, nil
		},
	)

	// Attached alone, no Security requirement — Class 1 alone.
	fmt.Println("=== Class 1 alone (reusable, no Security) ===")
	auditOnly := resources.Notes.WithRead(file.Read{}).ReadBoundMW(auditLog)
	auditOnlyHandle, err := auditOnly.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(audit-only): %v", err)
	}
	if _, err := client.Read(ctx, auditOnlyHandle, alice); err != nil {
		log.Fatalf("read (audit-only): %v", err)
	}

	// Attached alone, WITH a Security requirement — Class 2 alone.
	fmt.Println("=== Class 2 alone (Security-bound) ===")
	securedReader := resources.Notes.WithRead(file.Read{}).
		ReadBoundMW(securityGate).
		WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	securedHandle, err := securedReader.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(secured read): %v", err)
	}
	securedAny, err := client.Read(ctx, securedHandle, alice)
	if err != nil {
		log.Fatalf("secured read: %v", err)
	}
	fmt.Printf("secured read (Security satisfied): %+v\n", securedAny.(resources.Note))

	// STACKED — both attached to the SAME role, in attachment order:
	// auditLog first, then securityGate. Each Fn's own print statement
	// proves the execution order.
	fmt.Println("=== Stacked: audit-log THEN security-gate, same role ===")
	stacked := resources.Notes.WithRead(file.Read{}).
		ReadBoundMW(auditLog).
		ReadBoundMW(securityGate).
		WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	stackedHandle, err := stacked.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(stacked): %v", err)
	}
	if _, err := client.Read(ctx, stackedHandle, alice); err != nil {
		log.Fatalf("read (stacked): %v", err)
	}
}
