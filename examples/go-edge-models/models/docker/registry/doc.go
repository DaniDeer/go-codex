// Package registry declares the DOCUMENTED, REUSABLE contract for the
// Docker Registry HTTP API v2 (a.k.a. the OCI Distribution Spec) surface
// this module works with: the REST routes, request/response domain
// structs and their codecs, and the MCP tool declarations for GetTags/
// GetImageMetadata. Everything in this package is pure data — no
// *http.Client, no I/O, no auth flow — safe for ANY application to import
// standalone (e.g. to build a different HTTP client, generate an OpenAPI/
// AsyncAPI spec, validate/parse image references, or expose these routes
// through a different MCP server).
//
// The CONCRETE IMPLEMENTATION built on top of this contract — the
// Client interface + NewGenericClient (a real, persistent,
// per-registry-host-bound *rest.Client), and the Bearer-token
// challenge/token-exchange auth flow — lives in the SIBLING package
// examples/go-edge-models/app/registry instead; the registry-agnostic,
// imageURL-resolving convenience (including ready-made MCP tool-handler
// bindings) is that SAME package's own Dispatcher type. This package
// never imports either of them (the dependency only ever goes the other
// way); both import this package for every declarative type/route/tool
// they build on.
//
// ALL auth-related concerns (Credentials, the Bearer/Basic
// Authorization-header encodings, the WWW-Authenticate challenge, the
// token-exchange response body, and the "bearerAuth" scheme metadata
// GetTagsRoute/GetManifestRoute attach via .Use(...)) live in the
// SIBLING subpackage models/docker/registry/auth — NOT in this root
// package — see that subpackage's own doc comment for the full rationale
// and file map. This package's remaining files hold only the
// REGISTRY-AGNOSTIC route/domain-type declarations.
//
// File layout — ONE FILE PER OPERATION, not per layer: each operation's
// route declaration and request/response types+codecs (and, where
// applicable, its MCP tool declaration) ALL live together, so
// understanding or changing one operation never requires jumping across
// files. There is deliberately no client.go/routes.go/mcptools.go
// aggregator file — the package itself (`import
// ".../models/docker/registry"`) is already the one clear import, and
// this doc comment is the one index a reader needs.
//
//   - ping.go: PingRoute — no MCP tool of its own (an auth probe has no
//     LLM-facing value); no client function either (used internally by
//     app/registry's auth flow).
//   - gettags.go: GetTagsReq/GetTagsRoute, TagsList/TagsListCodec, and
//     GetTagsToolReq/GetTagsTool (the declared MCP tool contract).
//   - getimagemetadata.go: ManifestMediaType/ManifestMediaTypeCodec (an
//     ENUM, via validate.OneOf, over the 4 known OCI/Docker manifest
//     media types), SupportedManifestMediaTypes,
//     AcceptManifestTypesDeclaration (a codec-backed Middleware
//     attaching GetManifestRoute's fixed Accept header directly, at
//     declaration time — unlike Bearer auth, it is a TRUE CONSTANT with
//     no per-client credential, so it needs no deferred attachment),
//     GetManifestReq/GetManifestRoute (the underlying single-manifest
//     route app/registry's GetImageMetadata composes, up to twice, for
//     manifest-list resolution), GetImageMetadataReq/ManifestMetadata
//     and their codecs (GetImageMetadataReqCodec/ManifestMetadataCodec
//     — declared as plain codex.Struct codecs, NOT a rest.Route, since
//     GetImageMetadata is a multi-call client-side orchestration with
//     no single dialable Method+Path — see this file's own doc comment
//     for the full rationale), and GetImageMetadataTool (the declared
//     MCP tool contract — reuses GetImageMetadataReq/ManifestMetadata
//     directly, since they're already a single registry-agnostic
//     shape). ManifestMetadata.Image is a docker.Image built by
//     app/registry reusing this package's ImageRef.ToImage() and
//     overriding Digest with the registry-resolved content digest.
//   - imageref.go: ImageRef (registry/repository/reference), its codec,
//     the ToImage/ImageRefFromImage mapper to/from docker.Image,
//     ParseImageRef/FormatImageRef, splitDockerDomain, and
//     ImageRefParseError.
//   - config.go: DockerHubRegistryHost (a re-export of
//     dockerhub.RegistryHost — see that constant's own doc comment) and
//     the Docker-Hub-only domain aliases imageref.go's parser needs
//     (dockerHubLegacyDomain/dockerHubDomain/dockerHubOfficialPrefix).
//     GHCR/MCR have NO equivalent re-export here — nothing in THIS
//     package needs their host constants; see ghcr.RegistryHost's own
//     doc comment.
//   - auth/ (subpackage): ALL auth-related concerns — Credentials (the
//     shared Username/Password base type every other Credentials type
//     in this module is built from), the Bearer/Basic Authorization-
//     header codecs, the WWW-Authenticate challenge, the Docker
//     auth-scope string, the token-exchange response body, and the
//     "bearerAuth" scheme metadata (BearerAuthScheme/
//     BearerAuthSchemeName/BearerCredential/BearerAuthDeclaration) —
//     see that subpackage's own doc comment for the full file map.
//   - generic/ (subpackage): the any-OCI-registry fallback's own
//     Credentials (a plain alias of auth.Credentials)/
//     RegistryCredentials — used ONLY by app/registry's generic fallback
//     client (NewGenericClient), for any registry host OTHER than the 3
//     known ones below.
//   - dockerhub/, ghcr/, mcr/ (subpackages): registry-SPECIFIC models —
//     each declares its OWN RegistryHost constant and its OWN DISTINCT
//     named Credentials type (auth.Credentials under the hood, with its
//     own codec composed on top of auth.CredentialsCodec — see each
//     subpackage's own doc comment) — see "SHARED ROUTES, PER-REGISTRY
//     SPECIFICS" below for the line between what lives here (this root
//     package) vs. in one of these.
//
// This package's PUBLIC SURFACE is exactly what a caller needs to declare
// a wire-compatible client/server/spec against this registry API:
//
//  1. The exported rest.Route values (PingRoute, GetTagsRoute,
//     GetManifestRoute) — pass any of them directly to
//     adapters/nethttp.Call (via a nethttp.Caller wrapping your own
//     *http.Client, retry policy, or observer), or call .ClientHandle()
//     on one and drive the lower-level api/rest.CallWithTransport
//     yourself. GetTagsRoute/GetManifestRoute both declare
//     Security, so driving them directly means chaining your own
//     nethttp.CredentialFunc onto the route via .ClientMW(...) — this
//     package has no exported helper for that (that's app/registry's
//     job); write one against the target registry's actual auth
//     requirements, or use app/registry.NewGenericClient (a known host)
//     or app/registry.Dispatcher (registry-agnostic, from an image
//     URL) instead.
//  2. The domain structs and codecs (ImageRef in imageref.go, TagsList in
//     gettags.go, GetTagsReq in gettags.go, GetManifestReq/
//     GetImageMetadataReq/ManifestMetadata in getimagemetadata.go) —
//     needed to call (1) and read its results, or to serialize/validate
//     these shapes independently of any HTTP call at all. For
//     credentials, see models/docker/registry/auth (the shared base),
//     models/docker/registry/generic (the any-registry fallback's own),
//     or dockerhub/ghcr/mcr (each known registry's own typed shape).
//  3. GetTagsTool/GetImageMetadataTool (gettags.go/getimagemetadata.go) —
//     declared, UNREGISTERED MCP tool contracts. Register them against
//     your own mcp.Builder and pair the resulting handle with a handler
//     function of your own choosing — app/registry.Dispatcher's own
//     NewGetTagsToolHandler()/NewGetImageMetadataToolHandler() methods
//     are the ready-made (registry-agnostic, closure-based) bindings, but
//     this package makes no assumption about how you implement the
//     handler.
//
// This package has NO dependency on the sibling models/iotedge package,
// and NEVER imports app/registry — it models an entirely separate Docker
// HTTP API (the registry API), not the create-options wire format, and
// stays a pure declaration a consumer can depend on without pulling in
// any concrete HTTP client machinery beyond what api/rest itself needs.
//
// SHARED ROUTES, PER-REGISTRY SPECIFICS — this package's ROOT-level
// routes/domain types (PingRoute/GetTagsRoute/GetManifestRoute,
// TagsList, ImageRef, GetImageMetadataReq/ManifestMetadata, the MCP tool
// declarations) work IDENTICALLY regardless of which OCI-compliant
// registry a caller points them at (Docker Hub, GHCR, MCR, or any other
// OCI Distribution Spec implementation) — the wire format genuinely IS
// identical across all three known registries, so these stay here,
// undeduplicated, rather than copy-pasted into each registry-specific
// subpackage below. See app/registry's own doc.go (and
// app/registry/dockerhub/.../ghcr/.../mcr's) for how their end-to-end
// test coverage proves this.
//
// This package DOES have per-registry subpackages for CREDENTIALS
// specifically — a deliberate reversal of an earlier round's "no
// per-registry subpackage at this layer" decision: models/docker/
// registry/dockerhub, .../ghcr, and .../mcr each declare that ONE
// registry's own SPECIFIC models (its own typed Credentials type + its
// own RegistryHost constant) — NOT a duplicate route/codec set. The line
// is: anything genuinely IDENTICAL across all three stays HERE (this
// root package); anything registry-SPECIFIC (today: credentials;
// potentially, in the future, a genuine wire-shape divergence one
// registry introduces) belongs in that registry's own subpackage
// instead. A caller working against an OCI-compliant registry with no
// dedicated subpackage of its own still uses this root package's routes
// directly, plus models/docker/registry/generic's
// Credentials/RegistryCredentials/WithCredentialsByRegistry — see
// app/registry.NewGenericClient's own doc comment for that
// generic-fallback client.
package registry
