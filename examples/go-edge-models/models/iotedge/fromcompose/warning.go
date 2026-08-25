package fromcompose

import (
	"fmt"
	"log/slog"
)

// WarningKind identifies which kind of best-effort, lossy, or
// approximated conversion decision a [Warning] reports.
type WarningKind string

const (
	// WarningSanitizedName reports that a Compose service name had to be
	// sanitized (lowercased, underscores/other invalid characters
	// replaced with hyphens) to become a valid iothub.ModuleName.
	WarningSanitizedName WarningKind = "sanitized_name"
	// WarningPlaceholderImage reports that a service declared `build:`
	// instead of `image:` — a synthetic placeholder image reference was
	// generated, which MUST be replaced before the resulting deployment
	// can actually be pushed to a device.
	WarningPlaceholderImage WarningKind = "placeholder_image"
	// WarningRestartPolicyApproximated reports that a service's
	// `restart:` value has no EXACT iothub.RestartPolicy equivalent
	// (currently only "unless-stopped", mapped to the closest analog,
	// "always") or was entirely unrecognized (also mapped to "always").
	WarningRestartPolicyApproximated WarningKind = "restart_policy_approximated"
)

// Warning reports one best-effort, lossy, or approximated decision made
// while converting a single Compose service — collected (never
// returned as an error) so a syntactically valid
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose.Project]
// ALWAYS produces a usable scaffold; see this package's own doc comment
// for the full "scaffold, not full fidelity" contract.
type Warning struct {
	// ServiceName is the ORIGINAL (pre-sanitization) Compose service
	// name the warning is about.
	ServiceName string
	Kind        WarningKind
	Message     string
}

func (w Warning) Error() string {
	return fmt.Sprintf("%s: %s: %s", w.ServiceName, w.Kind, w.Message)
}

// LogValue implements slog.LogValuer for structured logging.
func (w Warning) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("service_name", w.ServiceName),
		slog.String("kind", string(w.Kind)),
		slog.String("message", w.Message),
	)
}
