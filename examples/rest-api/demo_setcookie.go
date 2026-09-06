package main

import (
	"fmt"
	"net/http/httptest"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// demoSetCookie exercises chiadapter.SetCookie/CookieOptions.WithCodec —
// direct, low-level cookie-write validation, using the SAME codec as the
// read-path CookieParam (routes.ProfileRoute's session_token). One
// codec definition, two boundaries: request-side merge AND response-side
// write.
func demoSetCookie() {
	fmt.Println("=== chiadapter.SetCookie — validates before writing Set-Cookie ===")
	sessionCodec := codex.String().Refine(validate.NonEmptyString)

	rec := httptest.NewRecorder()
	if err := chiadapter.SetCookie(rec, "session_token", "refreshed-session-token", chiadapter.CookieOptions{
		Codec:  &sessionCodec,
		MaxAge: 3600,
	}); err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  Valid:   Set-Cookie: %s\n", rec.Header().Get("Set-Cookie"))
	}

	rec2 := httptest.NewRecorder()
	err := chiadapter.SetCookie(rec2, "session_token", "", chiadapter.CookieOptions{}.WithCodec(sessionCodec))
	fmt.Printf("  Invalid: error=%v, Set-Cookie=%q\n", err, rec2.Header().Get("Set-Cookie"))
	fmt.Println()
}
