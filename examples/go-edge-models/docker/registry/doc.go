// Package registry models the Docker Registry HTTP API v2 (a.k.a. the OCI
// Distribution Spec) surface needed to retrieve, for a given container
// image reference: its available tags, and lean top-level manifest
// metadata (schema version, media type, content digest, total size) —
// deliberately excluding per-layer detail.
//
// Unlike the sibling docker package (pure codec modeling, zero I/O), this
// package is a REST CLIENT: it declares api/rest.Route values for every
// registry endpoint (routes.go) AND provides a thin orchestration layer
// (client.go) implementing the registry's Bearer auth-challenge flow and
// automatic multi-arch manifest-list resolution — both of which require
// issuing HTTP calls, so they cannot live inside a pure codex.Codec.
//
// Consumption layers, in the order a caller encounters them:
//
//  1. routes.go's exported rest.Route values (PingRoute, GetTagsRoute,
//     GetManifestRoute, GetTokenRoute) are the PRIMARY contract — call
//     .ClientHandle() on any of them and drive adapters/nethttp.Call
//     directly, with your own *http.Client, retry policy, or observer.
//  2. client.go's GetTags/GetImageMetadata are a convenience layer built ON
//     TOP of (1) — they compose the routes with the auth flow and
//     manifest-list resolution so a caller doesn't have to reimplement
//     that orchestration.
//
// This package has NO dependency on the sibling iotedge or docker
// packages — it models an entirely separate Docker HTTP API (the registry
// API), not the create-options wire format.
package registry
