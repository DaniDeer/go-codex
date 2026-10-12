// Package auth holds EVERY auth-related concern for the OCI Distribution
// Spec / Docker Registry HTTP API v2: both the token-EXCHANGE side
// (Credentials — the shared Username/Password base type every other
// Credentials type in this module is built from — and the Bearer/Basic
// Authorization header encodings used to obtain and present a token) and
// the token-USE side (BearerAuthScheme/BearerCredential/
// BearerAuthDeclaration — attaching an already-obtained Bearer token to
// the actual GetTagsRoute/GetManifestRoute calls), plus the generic
// OCI/Docker Distribution Spec wire plumbing those two sides are built
// from: the WWW-Authenticate challenge, the Docker auth-scope string, and
// the registry token-endpoint response body.
//
// This package is PUBLIC (no `internal/` path segment) — a deliberate
// choice, migrated from what used to be a true Go internal package
// (examples/go-edge-models/internal/registry) specifically so this
// auth-flow plumbing becomes part of the officially reusable, declared
// contract: a caller building their OWN registry client against a
// registry this module doesn't model concretely yet can still reuse this
// package's Credentials/challenge-parsing/scope-formatting/token-
// decoding logic, instead of reimplementing RFC 6750 / Docker
// Distribution's auth spec from scratch.
//
// This package is the SINGLE place every Credentials-shaped type in this
// module is ultimately built from:
//
//   - models/docker/registry/generic.Credentials is a plain ALIAS of
//     this package's Credentials (no extra constraint — the generic
//     fallback client has no single known registry to guard against).
//   - models/docker/registry/{dockerhub,ghcr,mcr}.Credentials are each a
//     DISTINCT named type (`type Credentials auth.Credentials`), with
//     their own CredentialsCodec composed ON TOP OF this package's
//     CredentialsCodec (ghcr.CredentialsCodec adds an EXTRA "Username
//     required" constraint via .RefineFunc(...) — the ONLY registry-
//     specific divergence today) — never redeclaring the struct or its
//     base field codecs.
//   - app/registry/internal's shared auth-flow mechanics
//     (Authenticator.CredentialFor, BearerAuthClientMW) operate on this
//     package's Credentials DIRECTLY — every self-contained registry
//     client converts its own typed Credentials into this shape once, at
//     construction time.
//
// Files are organized ONE PER CONCEPT:
//
//   - credentials.go — Credentials (the shared base type), its base
//     CredentialsCodec/NewCredentials, BearerTokenCodec, and
//     BasicAuthCodec (the Authorization header encodings the
//     auth-token exchange needs — BasicAuthCodec's input/output type IS
//     Credentials, the SAME type a caller supplies to WithCredentials).
//   - security.go — BearerAuthSchemeName, BearerAuthScheme,
//     BearerCredential, BearerAuthDeclaration (the token-USE concern —
//     migrated from models/docker/registry's own security.go; see that
//     package's doc comment for why it moved here).
//   - challenge.go — Challenge, ChallengeCodec, WWWAuthenticateCodec
//     (decodes directly from an http.Header set), and the
//     WWW-Authenticate Bearer-challenge parse/format functions.
//   - scope.go — DockerScope, ActionsConstraint, DockerScopeCodec, and
//     the Docker auth-scope string parse/format functions.
//   - token.go — TokenResponse, TokenResponseCodec (registry token
//     endpoint response body).
//
// The manifest/platform-selector half of what used to live in
// internal/registry (not auth-specific) moved to the sibling models/util
// package instead — see that package's own doc comment.
//
// IMPORTANT for future work: this package must stay PURELY GENERIC OCI/
// Docker Distribution Spec auth plumbing — it must never absorb a
// registry-specific quirk (e.g. a GHCR-only token-exchange extension);
// registry-specific integrations belong in
// models/docker/registry/{dockerhub,ghcr,mcr} and
// app/registry/{dockerhub,ghcr,mcr} instead.
package auth
