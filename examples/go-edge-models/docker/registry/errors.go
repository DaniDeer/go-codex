package registry

import (
	"fmt"
	"log/slog"
)

// This file holds every exported error type returned by this package —
// both the general client-wiring errors (image-reference parsing,
// manifest-list resolution) and the authentication errors (WWW-Authenticate
// challenge parsing, token-exchange failures). Consolidating them here
// (rather than splitting per client.go/auth.go) keeps error shapes and
// their slog.LogValuer implementations easy to compare side-by-side, and
// gives callers doing errors.As-based structured logging a single file to
// consult for this package's full error surface.

// ── Client errors ────────────────────────────────────────────────────────────────────

// ImageRefParseError is returned by ParseImageRef when the input string
// does not match a valid container image reference shape.
type ImageRefParseError struct {
	Input string
	Err   error
}

func (e ImageRefParseError) Error() string {
	return fmt.Sprintf("parse image reference %q: %s", e.Input, e.Err)
}
func (e ImageRefParseError) Unwrap() error { return e.Err }
func (e ImageRefParseError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("input", e.Input), slog.Any("cause", e.Err))
}

// NestedManifestListError is returned by GetImageMetadata when a resolved
// manifest-list entry's digest ALSO resolves to a manifest list — this
// package supports exactly one level of manifest-list resolution.
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

// ── Auth errors ────────────────────────────────────────────────────────────────────

// RegistryAuthChallengeError is returned when a registry's 401 response
// carries a malformed or missing WWW-Authenticate header.
type RegistryAuthChallengeError struct {
	Header string
	Err    error
}

func (e RegistryAuthChallengeError) Error() string {
	return fmt.Sprintf("parse WWW-Authenticate challenge %q: %s", e.Header, e.Err)
}
func (e RegistryAuthChallengeError) Unwrap() error { return e.Err }
func (e RegistryAuthChallengeError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("header", e.Header), slog.Any("cause", e.Err))
}

// RegistryAuthError is returned when the auth realm's token endpoint call
// fails, or the ping request itself fails for a reason other than a clean
// 401 challenge.
type RegistryAuthError struct {
	Registry string
	Err      error
}

func (e RegistryAuthError) Error() string {
	return fmt.Sprintf("authenticate with registry %q: %s", e.Registry, e.Err)
}
func (e RegistryAuthError) Unwrap() error { return e.Err }
func (e RegistryAuthError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("registry", e.Registry), slog.Any("cause", e.Err))
}
