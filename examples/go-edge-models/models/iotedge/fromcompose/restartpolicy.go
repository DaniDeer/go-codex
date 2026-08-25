package fromcompose

import (
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
)

// restartPolicyFor maps Compose's `restart:` string to an
// iothub.RestartPolicy. approximated is true when the mapping is NOT an
// exact equivalent (currently only "unless-stopped", which has no
// direct IoT Edge analog) or when compose was entirely unrecognized —
// in BOTH cases the caller should emit a WarningRestartPolicyApproximated.
//
// Mapping table:
//
//   - "no", "" (omitted — Compose's own documented default) -> never
//   - "always"                                              -> always (exact)
//   - "on-failure", "on-failure:N" (":N" retry-count suffix
//     stripped — no IoT Edge equivalent for a max-retry count) -> on-failure (exact)
//   - "unless-stopped"                                       -> always (APPROXIMATED — closest analog, not exact)
//   - anything else (unrecognized)                           -> always (APPROXIMATED — safest fallback)
func restartPolicyFor(compose string) (policy iothub.RestartPolicy, approximated bool) {
	switch {
	case compose == "" || compose == "no":
		return "never", false
	case compose == "always":
		return "always", false
	case compose == "on-failure" || strings.HasPrefix(compose, "on-failure:"):
		return "on-failure", false
	case compose == "unless-stopped":
		return "always", true
	default:
		return "always", true
	}
}

// composeRestartFor is restartPolicyFor's REVERSE: maps an
// iothub.RestartPolicy back to a Compose `restart:` string. exact is
// false when the mapping is NOT precise — currently only
// iothub.RestartPolicy("on-unhealthy"), which has NO Compose equivalent
// AT ALL (Compose's restart policies are entirely process-exit-code
// driven; it has no concept of a HEALTHCHECK-triggered restart), so it
// is approximated as "always" (the safest fallback, matching
// restartPolicyFor's own unrecognized-value fallback).
//
// Mapping table:
//
//   - never       -> "no"         (exact)
//   - always      -> "always"     (exact)
//   - on-failure  -> "on-failure" (exact)
//   - on-unhealthy -> "always"    (APPROXIMATED — no Compose equivalent)
func composeRestartFor(policy iothub.RestartPolicy) (compose string, exact bool) {
	switch policy {
	case "never":
		return "no", true
	case "always":
		return "always", true
	case "on-failure":
		return "on-failure", true
	default:
		// "on-unhealthy", or any future/unrecognized enum value.
		return "always", false
	}
}

// RestartPolicyFromComposeCodec is a Codec[iothub.RestartPolicy] backed
// by Compose's own `restart:` STRING wire shape — built via
// codex.MapCodecSafe wrapping [restartPolicyFor]/[composeRestartFor]
// (single source of truth for the mapping tables; this codec just gives
// it a Codec shape for callers that don't need the "approximated"/
// "exact" signal warningsForService/warningsForModuleConfig report
// separately as a [Warning]).
var RestartPolicyFromComposeCodec = c.MapCodecSafe(
	c.String(),
	func(compose string) iothub.RestartPolicy {
		policy, _ := restartPolicyFor(compose)
		return policy
	},
	func(policy iothub.RestartPolicy) (string, error) {
		compose, _ := composeRestartFor(policy)
		return compose, nil
	},
)
