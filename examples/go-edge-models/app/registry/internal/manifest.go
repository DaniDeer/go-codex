package internal

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/util"
)

// This file holds the manifest-list-to-single-platform resolution logic
// EVERY self-contained registry client's GetImageMetadata method needs
// (PlatformMatches/FormatPlatformSelector) and its error types
// (NestedManifestListError/PlatformNotFoundError) -- the shared MECHANICS
// so "fully self-contained client" doesn't mean "triplicated manifest-
// list resolution logic." The declarative contract itself
// (GetManifestRoute, GetImageMetadataReq/ManifestMetadata and their
// codecs, GetImageMetadataTool) lives in the sibling
// models/docker/registry package instead. Each client calls its own
// *rest.Client.Call(ctx, manifestRoute, regmodels.GetManifestReq{...})
// directly, inline, at its own 2 call sites (list resolution +
// platform-specific fetch) -- no shared wrapper is needed for that
// single line, unlike this file's own genuinely multi-step resolution
// logic.

// PlatformMatches reports whether d's platform matches selector — a
// plain struct-field comparison over already-decoded values (d.Platform
// via util.ManifestDescriptorCodec, selector via
// util.PlatformSelectorCodec) — no string splitting in business logic.
func PlatformMatches(d util.ManifestDescriptor, selector util.PlatformSelector) bool {
	return d.Platform != nil && d.Platform.OS == selector.OS && d.Platform.Architecture == selector.Architecture
}

// FormatPlatformSelector reconstructs an "os/arch" platform selector
// string from its os/arch parameters — a thin wrapper around
// util.PlatformSelectorCodec.Encode.
func FormatPlatformSelector(osName, arch string) (string, error) {
	raw, err := util.PlatformSelectorCodec.Encode(util.PlatformSelector{OS: osName, Architecture: arch})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// NestedManifestListError is returned by GetImageMetadata when a resolved
// manifest-list entry's digest ALSO resolves to a manifest list — only
// one level of manifest-list resolution is supported.
type NestedManifestListError struct {
	Registry   string
	Repository string
	Reference  string
}

func (e NestedManifestListError) Error() string {
	return fmt.Sprintf("nested manifest list at %s/%s:%s (only one level of resolution is supported)",
		e.Registry, e.Repository, e.Reference)
}
func (e NestedManifestListError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("registry", e.Registry),
		slog.String("repository", e.Repository),
		slog.String("reference", e.Reference),
	)
}

// PlatformNotFoundError is returned by GetImageMetadata when a manifest
// list has no entry matching the requested platform.
type PlatformNotFoundError struct {
	Platform  string
	Available []string
}

func (e PlatformNotFoundError) Error() string {
	return fmt.Sprintf("platform %q not found in manifest list (available: %v)", e.Platform, e.Available)
}
func (e PlatformNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("platform", e.Platform),
		slog.Any("available", e.Available),
	)
}
