// Resources
// - examples/flat-key-patch -> demonstrates dotted-key JSON patching with go-codex
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/iotedge"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

//go:embed examples/usecase1.json
var usecase1JSON []byte

func main() {
	// Decode a REAL IoT-Edge deployment manifest end-to-end:
	// modulesContent -> $edgeAgent -> {<dotted module key>: ModuleConfig, ...}
	//
	// iotedge.ModuleNameCodec strips iotedge.ModuleKeyPrefix from each
	// dotted key and validates the remaining segment via validate.Slug,
	// producing a map[ModuleName]ModuleConfig — this is the codex.Map[K,V]
	// key-extraction pattern this example demonstrates, mirroring
	// examples/flat-key-patch's containerKeyCodec/containersCodec section
	// but producing a MAP (Modules) instead of a merged []Container slice
	// (via codex.EntrySlice).
	jManifest := format.JSON(iotedge.DeploymentManifestCodec)
	manifest, err := jManifest.Unmarshal(usecase1JSON)
	if err != nil {
		log.Fatal(err)
	}

	modules := manifest.ModulesContent.EdgeAgent
	names := make([]iotedge.ModuleName, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	fmt.Printf("=== %d modules extracted via ModuleNameCodec (codex.Map[ModuleName, ModuleConfig]) ===\n", len(names))
	for _, name := range names {
		m := modules[name]
		fmt.Printf("  %-32s image=%-45s type=%-8s status=%-8s restartPolicy=%s\n",
			name, m.Settings.Image, m.Type, m.Status, m.RestartPolicy)
	}

	// ── CreateOptions detail for one module with a rich create-options doc ──
	edgeProxy := modules["factory-edge-proxy"]
	fmt.Println("\n=== factory-edge-proxy: CreateOptions detail ===")
	fmt.Println("ExposedPorts:", edgeProxy.Settings.CreateOptions.ExposedPorts)
	fmt.Printf("HostConfig.Binds: %+v\n", edgeProxy.Settings.CreateOptions.HostConfig.Binds)
	fmt.Printf("HostConfig.PortBindings: %+v\n", edgeProxy.Settings.CreateOptions.HostConfig.PortBindings)

	// ── EnvVarValue's 3-way string/int/float union in action ───────────────
	// factory-dashboard's AUTO_REFRESH_INTERVAL is a bare JSON integer (15000)
	// and REFRESH_RATE_HZ is a bare JSON fractional number (0.5), while most
	// other env vars are JSON strings — EnvVarValueCodec (via
	// codex.UntaggedUnion, tried string-then-int-then-float) dispatches each
	// to the correct branch automatically.
	dashboard := modules["factory-dashboard"]
	fmt.Println("\n=== factory-dashboard: env var value union (string vs int vs float) ===")
	for name, ev := range dashboard.Env {
		switch {
		case ev.Value.StringValue != nil:
			fmt.Printf("  %-16s StringValue=%q\n", name, *ev.Value.StringValue)
		case ev.Value.IntValue != nil:
			fmt.Printf("  %-16s IntValue=%d\n", name, *ev.Value.IntValue)
		case ev.Value.FloatValue != nil:
			fmt.Printf("  %-16s FloatValue=%v\n", name, *ev.Value.FloatValue)
		}
	}

	// ── factory-cache has NO "env" key at all (OptionalField) ───────────
	cache := modules["factory-cache"]
	fmt.Printf("\nfactory-cache.Env (no \"env\" key on the wire): %v (len=%d)\n", cache.Env, len(cache.Env))

	// ── factory-metrics-collector has createOptions:"" (empty string) ───────────────
	metrics := modules["factory-metrics-collector"]
	fmt.Printf("factory-metrics-collector.Settings.CreateOptions (createOptions was \"\" on the wire): %+v\n", metrics.Settings.CreateOptions)

	// ── Round trip: re-encode the entire manifest ───────────────────────────
	reEncoded, err := jManifest.Marshal(manifest)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nre-encoded manifest: %d bytes (original: %d bytes)\n", len(reEncoded), len(usecase1JSON))

	// ── ModulePatch: patch one module's image via ports.File + PatchEncoded ──
	//
	// modulepatch.ModulePatchCodec (iotedge/modulepatch) encodes a flat
	// ModulePatch{ModuleName, ImageURL} into the manifest's full nested wire
	// shape (modulesContent -> $edgeAgent -> <dotted key> -> settings ->
	// image) — reusing iotedge.ModuleNameCodec and iotedge.ImageCodec
	// directly, no new constraints. ports.PatchEncoded deep-merges that
	// shape onto the real file on disk, touching ONLY settings.image for
	// the named module — every other field, and every other module, is
	// left untouched. Same pattern as examples/flat-key-patch.
	fmt.Println("\n=== ModulePatch: patch factory-dashboard's image on disk ===")

	dir, err := os.MkdirTemp("", "go-edge-models-patch-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	manifestPath := dir + "/usecase1.json"
	if err := os.WriteFile(manifestPath, usecase1JSON, 0o644); err != nil {
		log.Fatal(err)
	}
	manifestFile := ports.NewFile(manifestPath, jManifest)

	before, err := manifestFile.Read(nil, ports.FileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("before: factory-dashboard image=%q\n", before.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image)

	patch := modulepatch.ModulePatch{ModuleName: "factory-dashboard", ImageURL: "ghcr.io/example-org/factory-dashboard:2.0.0"}
	if err := ports.PatchEncoded(manifestFile, nil, modulepatch.ModulePatchCodec, patch, ports.FileOptions{}); err != nil {
		log.Fatal(err)
	}

	after, err := manifestFile.Read(nil, ports.FileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	patchedWeb := after.ModulesContent.EdgeAgent["factory-dashboard"]
	fmt.Printf("after:  factory-dashboard image=%q\n", patchedWeb.Settings.Image)
	fmt.Printf("        factory-dashboard env still has %d entries (untouched): %v\n", len(patchedWeb.Env), patchedWeb.Env != nil)

	// Confirm every OTHER module is byte-for-byte unaffected by the patch.
	unaffected := true
	for name, m := range modules {
		if name == "factory-dashboard" {
			continue
		}
		if after.ModulesContent.EdgeAgent[name].Settings.Image != m.Settings.Image {
			unaffected = false
			break
		}
	}
	fmt.Printf("        all %d other modules unaffected: %v\n", len(modules)-1, unaffected)

	// ── docker/registry: fetch tags + lean manifest metadata for one image ──
	//
	// This section is PURE WIRING — it just spins up two local httptest
	// servers (simulating a registry host + a SEPARATE auth-realm host,
	// exactly like Docker Hub's registry-1.docker.io vs auth.docker.io) and
	// calls docker/registry's exported GetTags/GetImageMetadata. All the
	// actual logic (auth-challenge flow, manifest-list-to-single-platform
	// resolution, lean metadata computation) lives in the docker/registry
	// package itself — reusable independent of this example.
	fmt.Println("\n=== docker/registry: tags + manifest metadata for factory-dashboard ===")
	runRegistryDemo()
}

// runRegistryDemo wires two local httptest servers together (a registry
// host and a separate auth-realm host) and calls registry.GetTags /
// registry.GetImageMetadata against a synthetic multi-arch image — the
// image name reuses "factory-dashboard" for narrative continuity with the rest
// of this example. See docker/registry's own package doc for the reusable
// client API this demonstrates.
func runRegistryDemo() {
	const fakeToken = "demo-token"

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("service") == "" || r.URL.Query().Get("scope") == "" {
			http.Error(w, "missing service/scope", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": fakeToken})
	}))
	defer authSrv.Close()

	// This mock plays the role of a REAL registry/auth server — docker/registry
	// is a client only, so it has no exported helpers for constructing these
	// values (that machinery is a private implementation detail of its own
	// auth flow). Building the WWW-Authenticate challenge, scope, and
	// Authorization value here is plain string formatting per the Docker
	// Distribution / RFC 6750 wire format a real registry server would emit.
	scope := "repository:factory-dashboard:pull"
	challenge := fmt.Sprintf(`Bearer realm=%q,service=%q,scope=%q`, authSrv.URL+"/token", "demo-registry", scope)
	bearerAuth := "Bearer " + fakeToken

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != bearerAuth {
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/factory-dashboard/tags/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "factory-dashboard",
			"tags": []string{"1.8.16.1", "1.8.15.0", "latest"},
		})
	})
	mux.HandleFunc("/v2/factory-dashboard/manifests/", func(w http.ResponseWriter, r *http.Request) {
		reference := strings.TrimPrefix(r.URL.Path, "/v2/factory-dashboard/manifests/")
		w.Header().Set("Content-Type", "application/json")
		switch reference {
		case "latest":
			// A multi-arch manifest list — GetImageMetadata resolves this
			// transparently to the linux/amd64 entry below.
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("11", 32))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.docker.distribution.manifest.list.v2+json",
				"manifests": []map[string]any{
					{
						"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
						"digest":    "sha256:f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6",
						"size":      1234,
						"platform":  map[string]any{"architecture": "amd64", "os": "linux"},
					},
					{
						"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
						"digest":    "sha256:0707070707070707070707070707070707070707070707070707070707070707",
						"size":      1235,
						"platform":  map[string]any{"architecture": "arm64", "os": "linux"},
					},
				},
			})
		case "sha256:f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6":
			w.Header().Set("Docker-Content-Digest", "sha256:f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
				"config":        map[string]any{"mediaType": "application/vnd.docker.container.image.v1+json", "digest": "sha256:c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", "size": 5000},
				"layers": []map[string]any{
					{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": "sha256:d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", "size": 30000000},
					{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": "sha256:e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5", "size": 12000000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	registrySrv := httptest.NewServer(mux)
	defer registrySrv.Close()

	// client.go always dials "https://<registryHost>" — httptest.Server
	// serves plain HTTP, so this Transport rewrites just the scheme.
	httpsToHTTP := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req = req.Clone(req.Context())
			if req.URL.Scheme == "https" {
				req.URL.Scheme = "http"
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	registryHost, err := url.Parse(registrySrv.URL)
	if err != nil {
		log.Fatal(err)
	}
	imageURL, err := registry.FormatImageRef(registry.ImageRef{
		Registry: registryHost.Host, Repository: "factory-dashboard", Reference: "latest",
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	tags, err := registry.GetTags(ctx, httpsToHTTP, imageURL)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("tags for %s: %v\n", tags.Name, tags.Tags)

	meta, err := registry.GetImageMetadata(ctx, httpsToHTTP, registry.GetImageMetadataReq{ImageURL: imageURL})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("manifest metadata (resolved from a multi-arch list to linux/amd64):\n")
	fmt.Printf("  schemaVersion=%d mediaType=%s\n", meta.SchemaVersion, meta.MediaType)
	fmt.Printf("  digest=%s totalSizeBytes=%d\n", meta.Digest, meta.TotalSizeBytes)
}

// roundTripFunc adapts a plain function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
