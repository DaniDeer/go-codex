package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoGrantedScopesContextField exercises the GrantedScopes extraction +
// middleware.CheckScopes mechanism a Security-gated BoundReadMiddleware
// drives.
//
// Unlike api/rest/api/events (which have a SERVER-side handler the
// GrantedScopes value can be propagated INTO via middleware.ContextField
// — see examples/rest-api's own demo of the same name), api/file has no
// handler concept at all: a verb call returns directly to the caller,
// there is no later server-side code the granted scopes need to reach.
// The real analog available at THIS layer is GrantedScopes extraction +
// enforcement itself — shown here as a correct-scope success and a
// wrong-scope rejection, side by side.
func demoGrantedScopesContextField(ctx context.Context, client *file.Client, rt file.Router) {
	alice := map[string]string{"id": "alice"}

	fmt.Println("=== correct scope granted (\"read\") ===")
	correct := resources.Notes.WithRead(file.Read{}).
		ReadBoundMW(file.BoundSecurityReadMiddleware[resources.Note, struct{}, resources.GrantedScopes](
			"apiKey", file.SecurityScheme{}, []string{"read"},
			func(ctx context.Context, v *resources.Note, in struct{}) (resources.GrantedScopes, error) {
				return resources.GrantedScopes{GrantedScopes: map[string][]string{"apiKey": {"read"}}}, nil
			},
		)).
		WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	correctHandle, err := correct.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(correct scope): %v", err)
	}
	if _, err := client.Read(ctx, correctHandle, alice); err != nil {
		log.Fatalf("read (correct scope): %v", err)
	}
	fmt.Println("  succeeded")

	fmt.Println("=== known credential, WRONG scope granted (\"write\", not \"read\") ===")
	wrong := resources.Notes.WithRead(file.Read{}).
		ReadBoundMW(file.BoundSecurityReadMiddleware[resources.Note, struct{}, resources.GrantedScopes](
			"apiKey", file.SecurityScheme{}, []string{"read"},
			func(ctx context.Context, v *resources.Note, in struct{}) (resources.GrantedScopes, error) {
				return resources.GrantedScopes{GrantedScopes: map[string][]string{"apiKey": {"write"}}}, nil
			},
		)).
		WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	wrongHandle, err := wrong.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(wrong scope): %v", err)
	}
	_, err = client.Read(ctx, wrongHandle, alice)
	var secErr file.SecurityRejectedError
	if errors.As(err, &secErr) {
		fmt.Println("  confirmed: rejected with SecurityRejectedError (granted \"write\", required \"read\")")
	} else {
		log.Fatalf("expected SecurityRejectedError, got: %v", err)
	}
}
