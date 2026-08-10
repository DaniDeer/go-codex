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
// batteries-included GetTags/GetImageMetadata client functions, the
// Bearer-token challenge/token-exchange auth flow, and the MCP tool
// handler bindings (NewGetTagsToolHandler/NewGetImageMetadataToolHandler)
// — lives in the SIBLING package examples/go-edge-models/app/registry
// instead. This package never imports app/registry (the dependency only
// ever goes the other way); app/registry imports this package for every
// declarative type/route/tool it builds on.
//
// File layout — ONE FILE PER OPERATION, not per layer: each operation's
// route declaration, request/response types+codecs, and (where
// applicable) its MCP tool declaration ALL live together, so
// understanding or changing one operation never requires jumping across
// files. There is deliberately no client.go/routes.go/mcptools.go
// aggregator file — the package itself (`import
// ".../models/docker/registry"`) is already the one clear import, and
// this doc comment is the one index a reader needs.
//
//   - ping.go: PingRoute — no MCP tool of its own (an auth probe has no
//     LLM-facing value); no client function either (see its own doc
//     comment — used internally by app/registry's auth flow).
//   - security.go: bearerAuthSecurity/bearerAuthScheme — GetTagsRoute/
//     GetManifestRoute's OWN declared security requirement (which auth
//     scheme those routes require). basicAuthSecurity/basicAuthScheme
//     (used only by app/registry's own getTokenRoute, a pure auth-flow
//     implementation detail) live in app/registry's auth.go instead.
//   - gettags.go: GetTagsReq/GetTagsRoute, TagsList/TagsListCodec, and
//     GetTagsToolReq/GetTagsTool (the declared MCP tool contract).
//   - getimagemetadata.go: GetManifestReq/GetManifestRoute (the
//     underlying single-manifest route app/registry's GetImageMetadata
//     composes, up to twice, for manifest-list resolution),
//     GetImageMetadataReq/ManifestMetadata and their codecs
//     (GetImageMetadataReqCodec/ManifestMetadataCodec — declared as
//     plain codex.Struct codecs, NOT a rest.Route, since GetImageMetadata
//     is a multi-call client-side orchestration with no single dialable
//     Method+Path — see this file's own doc comment for the full
//     rationale), and GetImageMetadataTool (the declared MCP tool
//     contract — reuses GetImageMetadataReq/ManifestMetadata directly,
//     since they're already a single registry-agnostic shape).
//     ManifestMetadata.Image is a docker.Image built by app/registry
//     reusing this package's ImageRef.ToImage() and overriding Digest
//     with the registry-resolved content digest.
//   - imageref.go: ImageRef (registry/repository/reference), its codec,
//     the ToImage/ImageRefFromImage mapper to/from docker.Image,
//     ParseImageRef/FormatImageRef, splitDockerDomain, ImageRefParseError,
//     and the Docker-Hub-default constants only this file's own logic
//     needs.
//   - credentials.go: Credentials/RegistryCredentials and their codecs,
//     plus the registry-host constants (ghcrRegistryHost/
//     mcrRegistryHost/knownRegistryHosts) only RegistryCredentialsCodec's
//     key constraint needs (dockerHubRegistryHost itself lives in
//     imageref.go and is referenced from here directly — same package,
//     no import needed).
//
// This package's PUBLIC SURFACE is exactly what a caller needs to declare
// a wire-compatible client/server/spec against this registry API:
//
//  1. The exported rest.Route values (PingRoute, GetTagsRoute,
//     GetManifestRoute) — call .ClientHandle() on any of them and drive
//     adapters/nethttp.Call directly, with your own *http.Client, retry
//     policy, or observer. GetTagsRoute/GetManifestRoute both declare
//     Security, so driving them directly means supplying your own
//     nethttp.CallOptions.CredentialFunc — this package has no exported
//     helper for that (that's app/registry's job); write one against the
//     target registry's actual auth requirements, or use
//     app/registry.GetTags/GetImageMetadata instead.
//  2. The domain structs and codecs (ImageRef in imageref.go, TagsList in
//     gettags.go, GetTagsReq in gettags.go, GetManifestReq/
//     GetImageMetadataReq/ManifestMetadata in getimagemetadata.go,
//     Credentials/RegistryCredentials in credentials.go, and their
//     codecs) — needed to call (1) and read its results, or to serialize/
//     validate these shapes independently of any HTTP call at all.
//  3. GetTagsTool/GetImageMetadataTool (gettags.go/getimagemetadata.go) —
//     declared, UNREGISTERED MCP tool contracts. Register them against
//     your own mcp.Builder and pair the resulting handle with a handler
//     function of your own choosing — app/registry.NewGetTagsToolHandler/
//     NewGetImageMetadataToolHandler are the ready-made
//     (registry-agnostic, closure-based) bindings, but this package makes
//     no assumption about how you implement the handler.
//
// This package has NO dependency on the sibling models/iotedge package,
// and NEVER imports app/registry — it models an entirely separate Docker
// HTTP API (the registry API), not the create-options wire format, and
// stays a pure declaration a consumer can depend on without pulling in
// any concrete HTTP client machinery beyond what api/rest itself needs.
//
// REGISTRY-AGNOSTIC BY DESIGN — this package's routes work IDENTICALLY
// regardless of which OCI-compliant registry a caller points them at
// (Docker Hub, GHCR, MCR, or any other OCI Distribution Spec
// implementation) — see app/registry's own doc.go for how its
// GetTags/GetImageMetadata client functions and their end-to-end test
// coverage prove this. There is deliberately no per-registry subpackage
// (no models/docker/registry/ghcr, no models/docker/registry/mcr) — that
// would work AGAINST this goal. Any registry-specific wire-shape
// difference discovered in the future belongs in the existing generic
// codecs (internal/registry) as an additional optional field/variant,
// never as registry-name branching.
package registry
