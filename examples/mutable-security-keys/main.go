// Package mutable-security-keys demonstrates codex.Mutable[T] and
// codex.Cacheable[T] composed with REAL adapter security hooks — proof
// that both containers are PURE OPT-IN CONVENIENCE, not a new API
// surface: SecurityFunc/CredentialFunc are already plain closures, so a
// caller captures a *codex.Mutable[T]/*codex.Cacheable[T] and calls
// .Get() inside the closure body — the SAME hooks a plain static value
// or hand-rolled cache would use.
//
// Two scenes, wired to ONE real HTTP round-trip (httptest server + a
// real nethttp.Call client):
//
//   - SERVER side — codex.Mutable[T]: a background rotation loop calls
//     Set on a schedule (simulating a JWKS/API-key refresh). The
//     nethttp.Options.SecurityFunc closure calls keys.Get() on EVERY
//     request — always sees the CURRENT key, no restart, no polling
//     loop of its own.
//   - CLIENT side — codex.Cacheable[T]: a TTL-bearing credential cache.
//     The nethttp.CallOptions.CredentialFunc closure checks Get()'s
//     freshness bool; when stale it "fetches" a new credential (a stand-
//     in for a real token-issuer call) and Set()s it before use. This is
//     the deliberate CONTRAST to Mutable[T]'s push model: nobody pushes
//     into a Cacheable[T] — a reader decides when to refresh. (Fully
//     automating this fetch-on-stale step is ports.RefreshingCacheable[T]'s
//     job — a separate, not-yet-shipped roadmap item; this example wires
//     the refresh manually, on purpose.)
//
// The one real gotcha this example is careful to avoid: BOTH closures
// call .Get() INSIDE the closure body, on every invocation — never
// hoisted out to a local variable at construction time. Hoisting it
// would silently freeze the value forever, defeating the whole point of
// using a live container instead of a plain static one.
//
// # Running
//
// go run ./examples/mutable-security-keys
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// reloadObserver logs every Mutable[T]/Cacheable[T] reload/invalidate —
// the same shape a real stats.Observer-based type would satisfy
// structurally (see stats.AsReloadObserver/AsInvalidateObserver for
// bridging an existing stats.Observer value into this position instead
// of a dedicated type like this one).
type reloadObserver struct{ name string }

func (o reloadObserver) RecordReload(location string, success bool, _ time.Duration) {
	fmt.Printf("  [observer:%s] reload %q: success=%v\n", o.name, location, success)
}

func (o reloadObserver) RecordInvalidate(location string) {
	fmt.Printf("  [observer:%s] invalidate %q\n", o.name, location)
}

// secureResp is the response body of the demo's one secured route.
type secureResp struct{ Message string }

var secureRespCodec = codex.Struct[secureResp](
	codex.RequiredField("message", codex.String(),
		func(r secureResp) string { return r.Message },
		func(r *secureResp, v string) { r.Message = v },
	),
)

func main() {
	keyCodec := codex.String().Refine(validate.NonEmptyString)

	// ── SERVER: codex.Mutable[T] — the signing key a real JWKS/API-key
	// rotation loop would refresh in the background. ──────────────────
	keys, err := codex.NewMutable("server-signing-key", "key-v1", keyCodec,
		codex.WithReloadObserver[string](reloadObserver{name: "server-keys"}))
	if err != nil {
		panic(err)
	}

	// ── CLIENT: codex.Cacheable[T] — a TTL-bearing credential cache.
	// Starts already matching the server's key; its short TTL means it
	// can go stale on its own, independent of any server-side rotation. ──
	cred, err := codex.NewCacheable("client-credential-cache", "key-v1", keyCodec,
		200*time.Millisecond,
		codex.WithCacheableReloadObserver[string](reloadObserver{name: "client-cred"}))
	if err != nil {
		panic(err)
	}

	// ── Spec: ONE secured route, declared with
	// middleware.SecurityScheme — completely unaware that
	// Mutable[T]/Cacheable[T] exist. Nothing about the route/handle/
	// builder changes to support either container; that is the point.
	// The runtime enforcement (a middleware.ServerImplementation built
	// inline below) is supplied SEPARATELY, at Register time — its
	// extraction Fn calls keys.Get() INSIDE the closure body, on every
	// request — never hoisted to a local outside the closure. ──────────
	secureDeclMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, &keyCodec)

	secureRoute := rest.NewRoute[struct{}, secureResp]("GET", "/secure",
		codex.Empty, secureRespCodec,
		rest.RouteMeta{OperationID: "secureEndpoint"},
	).Use(secureDeclMw)

	secureImplMw := middleware.ServerImplementation{
		Name:      "implement-scopes:bearerAuth",
		Satisfies: []string{"bearerAuth"},
		Fn: func(_ context.Context, r *http.Request, _ *struct{}) (map[string][]string, error) {
			current := keys.Get() // ← read fresh on EVERY request, not hoisted
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token != current {
				return nil, errors.New("signing key mismatch")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	}

	servedRoute := secureRoute.WithHandler(func(_ context.Context, _ struct{}) (secureResp, error) {
		return secureResp{Message: "welcome"}, nil
	}).HandleMW(&secureDeclMw, secureImplMw.Fn)
	mux, err := nethttp.ServeOne(servedRoute)
	if err != nil {
		panic(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// fetchFreshCredential simulates a real token-issuer round-trip a
	// production CredentialFunc would make (e.g. an OAuth token endpoint
	// or JWKS fetch) — here it stands in for that external call by
	// reading the server's CURRENT key directly, purely to keep this a
	// single-process, network-free example.
	fetchFreshCredential := func() string { return keys.Get() }

	// ── Client: the credential-providing Fn, attached via
	// Route.ClientMW (paired against secureDeclMw), calls cred.Get()
	// INSIDE the closure body on every call, refreshing via Set() when
	// stale — never hoisted. ──
	securedClientRoute := secureRoute.ClientMW(&secureDeclMw,
		func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
			val, fresh := cred.Get() // ← the bool MUST be handled explicitly; Cacheable never hides it
			if !fresh {
				val = fetchFreshCredential()
				if err := cred.Set(val); err != nil {
					return nil, err
				}
			}
			h := make(http.Header)
			h.Set("Authorization", "Bearer "+val)
			return h, nil
		})
	// handle is built ONCE and reused by every call below — the
	// recommended pattern for many calls to the same route.
	handle := securedClientRoute.ClientHandle()

	call := func(label string) {
		_, err := nethttp.CallWithHandle(context.Background(), srv.Client(), srv.URL, handle, struct{}{},
			nethttp.CallOptions{
				// A 401 means the cached credential no longer matches the
				// server's rotated key — invalidate so the NEXT call
				// refetches instead of retrying the same stale value.
				// Mirrors nethttp.NewCachingCredentialFunc's own
				// invalidate-on-401 wiring.
				OnCredentialRejected: cred.Invalidate,
			})
		status := "200 OK"
		if err != nil {
			var statusErr nethttp.UnexpectedStatusError
			if errors.As(err, &statusErr) {
				status = fmt.Sprintf("%d rejected", statusErr.StatusCode)
			} else {
				status = "error: " + err.Error()
			}
		}
		fmt.Printf("[%s] GET /secure → %s\n", label, status)
	}

	fmt.Println("=== 1. Fresh credential matches the server's key ===")
	call("call 1")

	fmt.Println("\n=== 2. Server rotates its signing key (Mutable[T].Set) ===")
	if err := keys.Set("key-v2"); err != nil {
		panic(err)
	}

	fmt.Println("\n=== 3. Client's cache is still within its TTL — presents the OLD key, rejected ===")
	call("call 2")

	fmt.Println("\n=== 4. OnCredentialRejected invalidated the cache — next call refetches ===")
	call("call 3")

	fmt.Println("\n=== 5. Natural TTL expiry (no rotation this time) still triggers a refetch ===")
	time.Sleep(250 * time.Millisecond)
	call("call 4")
}
