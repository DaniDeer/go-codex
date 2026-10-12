package registry

import (
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/dockerhub"
)

// This file is the SINGLE source of truth, AT THIS ROOT PACKAGE, for
// every registry-host identity constant imageref.go's own parsing logic
// needs. DockerHubRegistryHost itself is NOT declared here — it is a
// re-export of dockerhub.RegistryHost (that subpackage's own constant is
// now the canonical source of truth for this value — see
// dockerhub.RegistryHost's own doc comment); re-exported here purely
// because imageref.go needs it alongside this file's own Docker-Hub-only
// domain aliases.
//
// GHCRRegistryHost/MCRRegistryHost/knownRegistryHosts used to live here
// too, but had no consumer in THIS package (only
// models/docker/registry/generic's RegistryCredentialsCodec needed
// them, and that package now sources ghcr.RegistryHost/mcr.RegistryHost
// directly rather than through this re-export) — removed to avoid an
// unused, drifting duplicate of what ghcr.RegistryHost/mcr.RegistryHost
// (their own packages) already declare.

const (
	// dockerHubLegacyDomain is the domain name Docker CLI accepts as an
	// alias for dockerHubDomain in an image reference (e.g.
	// "index.docker.io/library/alpine"). Used only by imageref.go's own
	// parsing logic (splitDockerDomain).
	dockerHubLegacyDomain = "docker.io"
	// dockerHubDomain is the canonical Docker Hub domain as it appears in
	// an image reference when no other registry host is given. Used only
	// by imageref.go's own parsing/formatting logic
	// (parseImageRefString/ImageRefFromImage).
	dockerHubDomain = "docker.io"
	// DockerHubRegistryHost re-exports dockerhub.RegistryHost (the
	// canonical source of truth) — see this file's own doc comment.
	DockerHubRegistryHost = dockerhub.RegistryHost
	// dockerHubOfficialPrefix is prepended to a single-segment repository
	// name resolved against Docker Hub (e.g. "alpine" -> "library/alpine").
	// Used only by imageref.go's own parsing logic.
	dockerHubOfficialPrefix = "library"
)
