package fromcompose

import (
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose"
)

// placeholderImagePrefix marks a synthesized image reference as one
// that MUST be replaced before the resulting deployment can actually be
// pushed to a device. Lowercase (matching an OCI image reference's own
// repository-segment constraint) so the synthesized reference itself
// round-trips through docker.ImageCodec/iothub.ImageCodec without
// failing validation before a caller even gets to replace it.
const placeholderImagePrefix = "replace-me/"

// placeholderServiceName is the GENERIC placeholder image name the pure
// serviceToModuleConfig mapping produces for EVERY build-only/unparseable-
// image service — serviceToModuleConfig operates on a bare
// dockercompose.Service value with NO access to that service's own name
// (names live one level up, as the project's services-map KEY, not part
// of the Service value itself — see dockercompose.ServiceName's own doc
// comment). personalizePlaceholderImage (below) patches this generic
// name to the ACTUAL sanitized service name afterward, once the name
// IS back in scope — a small, explicit, name-aware fixup layered on top
// of the name-agnostic codec, not hidden inside it.
const placeholderServiceName = "service"

// ── The bidirectional Service <-> ModuleConfig codec ─────────────────────────
//
// ModuleConfigFromServiceCodec is a codex.Codec[iothub.ModuleConfig]
// built via codex.MapCodecValidated, backed by
// dockercompose.ServiceCodec's OWN wire shape — Decode reads Compose-
// service wire data (any format.YAML/format.JSON-produced `any` value)
// and produces an iothub.ModuleConfig; Encode does the reverse. Because
// MapCodecValidated always builds BOTH directions from one declaration,
// this single value IS the "map Codec A to Codec B, and vice versa"
// primitive the sibling models/docker/dockercompose and models/azure/iothub
// packages' own wire codecs compose into — format.YAML(ModuleConfigFromServiceCodec)
// works directly, with ZERO hand-rolled JSON/YAML walking anywhere in
// this package.
//
// serviceToModuleConfig/moduleConfigToService are deliberately PURE,
// deterministic, warning-free, and NAME-AGNOSTIC (a dockercompose.Service
// value carries no name of its own) — every name-dependent or
// warning-worthy decision (sanitizing a service name, personalizing a
// placeholder image, recording that a decision was lossy/approximated)
// is computed SEPARATELY, once the name IS back in scope, by
// personalizePlaceholderImage/warningsForService below — see this
// package's own doc comment for why warnings don't fit
// MapCodecValidated's plain (B, error) signature.
//
// MapCodecValidated calls iothub.ModuleConfigCodec.Validate(b) after
// every decode — every Refine constraint iothub.ModuleConfig's own
// fields declare (Status's oneOf, RestartPolicy's oneOf, Type's fixed
// value, Version's non-empty check) is enforced AUTOMATICALLY, with
// ZERO duplicated validation code in this package.
var ModuleConfigFromServiceCodec = c.MapCodecValidated(
	dockercompose.ServiceCodec, iothub.ModuleConfigCodec,
	serviceToModuleConfig, moduleConfigToService,
)

// serviceToModuleConfig is Decode's pure mapping function (Compose ->
// IoT Edge): resolves an image (parsing svc.Image, or falling back to
// the GENERIC placeholder — see placeholderServiceName's own doc
// comment — when absent/unparseable, which is what makes this function
// ALWAYS succeed), reuses svc.CreateOptionsFor() for CreateOptions
// (now infallible and warning-free — Ports/Healthcheck/Ulimits are
// already codec-native by the time ServiceCodec decodes svc, so there
// is nothing left to fail or warn about here), maps Restart via
// restartPolicyFor, maps Environment via envVarsFor, and sets the
// Type/Status/Version constants every converted module shares.
func serviceToModuleConfig(svc dockercompose.Service) (iothub.ModuleConfig, error) {
	img := docker.Image{Name: placeholderImagePrefix + placeholderServiceName, Tag: "latest"}
	if svc.HasImage() {
		if parsed, err := docker.NewImageFromStr(svc.Image); err == nil {
			img = parsed
		}
	}

	createOptions := svc.CreateOptionsFor()
	restartPolicy, _ := restartPolicyFor(svc.Restart)

	return iothub.ModuleConfig{
		Settings: iothub.ModuleSettings{
			Image:         img,
			CreateOptions: createOptions,
		},
		Env:           envVarsFor(svc.Environment),
		Type:          "docker",
		Status:        "running",
		RestartPolicy: restartPolicy,
		Version:       "Will be automatically overwritten",
	}, nil
}

// moduleConfigToService is Encode's pure mapping function (IoT Edge ->
// Compose): reuses dockercompose.ServiceFromCreateOptions for every
// field CreateOptions carries, then resolves Image/Build (a PLACEHOLDER
// image — detected via isPlaceholderImage, the SAME predicate
// personalizePlaceholderImage/warningsForService use — reverses to
// Build:{Context:"."}/Image:"", since reproducing the placeholder text
// into Compose would be meaningless; "." is a sensible minimal context
// placeholder — the module's ORIGINAL build context is never
// recoverable from an IoT Edge deployment manifest, which carries no
// build information at all; any other image formats back to a plain
// string via docker.Image.String()), Environment (via envFromEnvVars),
// and Restart (via composeRestartFor's reverse table).
func moduleConfigToService(mc iothub.ModuleConfig) (dockercompose.Service, error) {
	svc := dockercompose.ServiceFromCreateOptions(mc.Settings.CreateOptions)

	if isPlaceholderImage(mc.Settings.Image) {
		svc.Build = dockercompose.Build{Context: "."}
	} else {
		svc.Image = mc.Settings.Image.String()
	}

	svc.Environment = envFromEnvVars(mc.Env)

	restart, _ := composeRestartFor(mc.RestartPolicy)
	svc.Restart = restart

	return svc, nil
}

// isPlaceholderImage reports whether img is one of THIS package's own
// synthesized placeholder images (see placeholderImagePrefix) — the
// SINGLE predicate serviceToModuleConfig's fallback, moduleConfigToService's
// Build detection, personalizePlaceholderImage, and warningsForService
// all share, so "what counts as a placeholder" is decided in exactly
// one place.
func isPlaceholderImage(img docker.Image) bool {
	return strings.HasPrefix(img.Name, placeholderImagePrefix)
}

// personalizePlaceholderImage patches mc's GENERIC placeholder image
// (see placeholderServiceName) to the service's ACTUAL sanitized name,
// once that name is back in scope — called by both ConvertService and
// ConvertProject (via ConvertProject's own post-decode pass) so neither
// re-implements the other's logic. A no-op when mc's image is not a
// placeholder.
func personalizePlaceholderImage(mc *iothub.ModuleConfig, moduleName iothub.ModuleName) {
	if isPlaceholderImage(mc.Settings.Image) {
		mc.Settings.Image.Name = placeholderImagePrefix + string(moduleName)
	}
}

// ── Warnings (computed OUTSIDE the pure codec, by re-deriving facts) ─────────

// warningsForService recomputes every [Warning] for one service by
// comparing the ORIGINAL Compose input to the resulting ModuleConfig —
// reusing the EXACT SAME predicates (sanitizeModuleName, restartPolicyFor,
// isPlaceholderImage) the pure to/from functions above call internally,
// never a re-implementation of the conversion logic itself. Ports have
// no warning case anymore: a malformed `ports:` entry now fails
// dockercompose.ServiceCodec.Decode itself (see
// [docker.PortMappingCodec]), so this function is never even reached
// for a service with an unparseable port.
func warningsForService(name dockercompose.ServiceName, svc dockercompose.Service, moduleName iothub.ModuleName, mc iothub.ModuleConfig) []Warning {
	var warnings []Warning

	if string(moduleName) != string(name) {
		warnings = append(warnings, Warning{
			ServiceName: string(name),
			Kind:        WarningSanitizedName,
			Message:     "service name is not a valid IoT Edge module name (lowercase alphanumeric + hyphens); sanitized to " + string(moduleName),
		})
	}

	if isPlaceholderImage(mc.Settings.Image) {
		msg := "service declares build: instead of image:; using a placeholder image — replace before deploying"
		if svc.HasImage() {
			_, err := docker.NewImageFromStr(svc.Image)
			msg = "image " + quoteOrEmpty(svc.Image) + " could not be parsed (" + err.Error() + "); using a placeholder image — replace before deploying"
		}
		warnings = append(warnings, Warning{ServiceName: string(name), Kind: WarningPlaceholderImage, Message: msg})
	}

	if _, approximated := restartPolicyFor(svc.Restart); approximated {
		warnings = append(warnings, Warning{
			ServiceName: string(name),
			Kind:        WarningRestartPolicyApproximated,
			Message:     "restart policy " + quoteOrEmpty(svc.Restart) + " has no exact IoT Edge equivalent; approximated as " + string(mc.RestartPolicy),
		})
	}

	return warnings
}

// warningsForModuleConfig recomputes every reverse-direction [Warning]
// for one module — currently just the restart-policy approximation
// (composeRestartFor's reverse table has ONE lossy case,
// iothub.RestartPolicy("on-unhealthy"), which has no Compose equivalent
// at all). Build-context and mem_reservation unrecoverability are
// PERMANENT, documented one-way losses, not something a caller needs to
// "fix" — see this package's own doc comment — so they stay doc-comment
// only, not a Warning.
func warningsForModuleConfig(moduleName iothub.ModuleName, mc iothub.ModuleConfig, svc dockercompose.Service) []Warning {
	var warnings []Warning
	if _, exact := composeRestartFor(mc.RestartPolicy); !exact {
		warnings = append(warnings, Warning{
			ServiceName: string(moduleName),
			Kind:        WarningRestartPolicyApproximated,
			Message:     "IoT Edge restart policy " + quoteOrEmpty(string(mc.RestartPolicy)) + " has no exact Compose equivalent; approximated as " + quoteOrEmpty(svc.Restart),
		})
	}
	return warnings
}

// ── Key codec (ServiceName <-> ModuleName) ───────────────────────────────────

// serviceNameToModuleNameCodec is the KEY codec ServicesToModulesCodec
// (project.go) pairs with ModuleConfigFromServiceCodec: decode direction
// (Compose service name string -> iothub.ModuleName) sanitizes via
// sanitizeModuleName (infallible, matching MapCodecSafe's `to` contract
// exactly); encode direction (iothub.ModuleName -> string) is a trivial
// infallible passthrough — a module name is ALREADY a valid Compose
// service name.
var serviceNameToModuleNameCodec = c.MapCodecSafe(
	c.String(),
	func(s string) iothub.ModuleName { return iothub.ModuleName(sanitizeModuleName(s)) },
	func(n iothub.ModuleName) (string, error) { return string(n), nil },
)

// ── Public single-service entry points ───────────────────────────────────────

// ConvertService converts ONE Compose service into an iothub.ModuleName
// + iothub.ModuleConfig pair, plus any [Warning]s the conversion
// produced. Routes the actual value transformation THROUGH
// ModuleConfigFromServiceCodec (re-encoding svc via
// dockercompose.ServiceCodec, then decoding that SAME wire value via
// the bidirectional codec) rather than calling serviceToModuleConfig
// directly — demonstrating the "map Codec A to Codec B" pattern in
// real use, not just declaring it unused. Always succeeds (never
// returns an error) — matches this package's "scaffold, not full
// fidelity" contract; see this package's own doc comment.
func ConvertService(name dockercompose.ServiceName, svc dockercompose.Service) (iothub.ModuleName, iothub.ModuleConfig, []Warning) {
	moduleName, _ := serviceNameToModuleNameCodec.Decode(string(name))

	raw, err := dockercompose.ServiceCodec.Encode(svc)
	if err != nil {
		// svc is an in-memory Go value this package's own callers built
		// (or that dockercompose.ServiceCodec itself just decoded) — an
		// encode failure here would mean a genuinely malformed Service,
		// which ServiceCodec's own construction doesn't allow; kept as a
		// defensive fallback producing a minimal, still-usable module
		// rather than panicking.
		return moduleName, fallbackModuleConfig(moduleName), []Warning{{
			ServiceName: string(name), Kind: WarningPlaceholderImage,
			Message: "service could not be encoded (" + err.Error() + "); using a placeholder image — replace before deploying",
		}}
	}

	mc, err := ModuleConfigFromServiceCodec.Decode(raw)
	if err != nil {
		// ModuleConfigCodec.Validate rejecting our OWN pure mapping's
		// output would be a bug in serviceToModuleConfig, not a real
		// service; same defensive fallback as above.
		return moduleName, fallbackModuleConfig(moduleName), []Warning{{
			ServiceName: string(name), Kind: WarningPlaceholderImage,
			Message: "internal conversion error (" + err.Error() + "); using a placeholder image — replace before deploying",
		}}
	}

	personalizePlaceholderImage(&mc, moduleName)
	return moduleName, mc, warningsForService(name, svc, moduleName, mc)
}

// fallbackModuleConfig is ConvertService's defensive minimal-but-valid
// ModuleConfig for the (practically unreachable) case where the
// bidirectional codec itself fails — see ConvertService's own comments.
func fallbackModuleConfig(moduleName iothub.ModuleName) iothub.ModuleConfig {
	return iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: placeholderImagePrefix + string(moduleName), Tag: "latest"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "Will be automatically overwritten",
	}
}

// ConvertModuleConfig is the REVERSE of [ConvertService]: converts ONE
// iothub.ModuleConfig back into a dockercompose.ServiceName +
// dockercompose.Service pair, plus any [Warning]s the reverse
// conversion produced. Routes the transformation THROUGH
// ModuleConfigFromServiceCodec's Encode direction (producing Compose-
// service wire data), then decodes that SAME wire value via
// dockercompose.ServiceCodec — the mirror image of ConvertService's own
// Encode-then-Decode routing.
//
// See this package's own doc comment for what this reverse direction
// CANNOT recover: Build context details (a placeholder image reverses
// to Build:{Context:"."}/Image:"", a minimal placeholder — the ORIGINAL
// build context, Dockerfile path, and build args were never captured
// going forward, since IoT Edge deployment manifests carry no build
// information at all), mem_reservation
// (docker.HostConfig never carried it), and the service's ORIGINAL name
// casing (moduleName is already sanitized; ConvertModuleConfig returns
// it UNCHANGED as the ServiceName, since there is no way to recover
// what the original mixed-case/underscore name was).
func ConvertModuleConfig(moduleName iothub.ModuleName, mc iothub.ModuleConfig) (dockercompose.ServiceName, dockercompose.Service, []Warning) {
	name, _ := serviceNameToModuleNameCodec.Encode(moduleName)
	serviceName := dockercompose.ServiceName(name.(string))

	raw, err := ModuleConfigFromServiceCodec.Encode(mc)
	if err != nil {
		return serviceName, dockercompose.Service{}, []Warning{{
			ServiceName: string(moduleName), Kind: WarningPlaceholderImage,
			Message: "module could not be converted back to a Compose service (" + err.Error() + ")",
		}}
	}
	svc, err := dockercompose.ServiceCodec.Decode(raw)
	if err != nil {
		return serviceName, dockercompose.Service{}, []Warning{{
			ServiceName: string(moduleName), Kind: WarningPlaceholderImage,
			Message: "internal conversion error (" + err.Error() + ")",
		}}
	}

	return serviceName, svc, warningsForModuleConfig(moduleName, mc, svc)
}

// envVarsFor converts docker.Env (Compose's own parsed "KEY=VALUE" list)
// into iothub.EnvVars — every value is kept as the STRING union variant,
// since Compose's list-form has no way to distinguish int/float from
// string on the wire anyway (an env var value is ALWAYS a string once
// written as "KEY=VALUE").
func envVarsFor(env docker.Env) iothub.EnvVars {
	if len(env) == 0 {
		return nil
	}
	out := make(iothub.EnvVars, len(env))
	for _, ev := range env {
		v := ev.Value
		out[iothub.EnvVarName(ev.Name)] = iothub.EnvVar{
			Value: iothub.EnvVarValue{StringValue: &v},
		}
	}
	return out
}

// envFromEnvVars is envVarsFor's reverse: formats every iothub.EnvVar's
// value back to a plain string. Delegates DIRECTLY to
// [iothub.FlattenEnvVars] — the sibling azure/iothub package's own
// deliberately one-directional (iotedge -> docker) mapper — rather than
// re-deriving the identical StringValue/IntValue/FloatValue formatting
// logic here; single source of truth for "how an EnvVarValue formats as
// a plain string."
func envFromEnvVars(vars iothub.EnvVars) docker.Env {
	return iothub.FlattenEnvVars(vars)
}

// sanitizeModuleName transforms s into a valid iothub.ModuleName
// (validate.Slug: lowercase alphanumeric segments separated by single
// hyphens, no leading/trailing/duplicate hyphens): lowercases, replaces
// every run of one-or-more non-[a-z0-9] characters with a single
// hyphen, then trims any leading/trailing hyphen. A completely empty or
// all-invalid input falls back to "service" (an extremely unlikely
// edge case for a real Compose file, but avoids ever producing an
// empty module name).
func sanitizeModuleName(s string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	prevHyphen := false
	for _, r := range lower {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		switch {
		case valid:
			b.WriteRune(r)
			prevHyphen = false
		case !prevHyphen:
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return placeholderServiceName
	}
	return result
}

// quoteOrEmpty is a tiny formatting helper so warning messages read
// naturally for both a set value ("unless-stopped") and an omitted one
// (shown as "(empty)" rather than a bare pair of quotes).
func quoteOrEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return "\"" + s + "\""
}
