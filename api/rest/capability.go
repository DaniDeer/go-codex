package rest

import (
	"fmt"
	"log/slog"
)

// CapabilityRequirement declares one adapter-defined protocol-native
// capability requirement (Tier 3a — Explicit, Sealed, per docs/roadmap/
// capability-requirement-composition.md's three-tier vocabulary) at the
// route level — the REST-side counterpart of [events.CapabilityRequirement]/
// [reqreply.CapabilityRequirement]. Own type, in its own package,
// deliberately NOT shared with api/events/api/reqreply (mirrors
// [middleware.Disposition]'s own D-0004 placement precedent — cheap to
// duplicate, avoids an api/rest → api/events import that would otherwise
// exist for no other reason than this one small struct).
//
// CapabilityRequirement is declared alongside a route's other [RouteOpt]
// values, independent of which adapter (if any) eventually supplies a
// matching sealed Capability value at declare time via that adapter's own
// ServeOptions/CallOptions.Capabilities field.
//
// As of this writing, NO shipped REST adapter supplies a concrete
// Capability value — HTTP (adapters/nethttp/adapters/chi) has no QoS/HWM
// concept, and the first non-HTTP REST-eligible transport (a future
// ZeroMQ REQ/REP adapter, see docs/roadmap/zeromq-rest-adapter.md) has
// not been implemented yet. This mirrors this doc's own "declare first,
// adapter satisfies second, adapter may lag behind declaration"
// philosophy exactly — the MECHANISM below is fully generic and
// adapter-agnostic, shippable and testable (via fake types) independent
// of any concrete adapter existing yet, same as MQTT5's still-pending
// Message Expiry Interval/Shared Subscriptions capabilities.
//
// CapabilityRequirement implements [RouteOpt]: pass it directly to
// [NewRoute]. It renders as a generic "x-codex-capabilities" OpenAPI
// vendor-extension array (one entry per declared CapabilityRequirement)
// on the route's Operation object — chosen over a per-capability vendor
// field so the renderer never needs a change when a new capability type
// is introduced. Mirrors AsyncAPI's "x-capabilities" naming, adapted to
// OpenAPI's own "x-" extension convention.
type CapabilityRequirement struct {
	// Name identifies the capability (e.g. "QoS", "HWM"). Must match the
	// name a supplying adapter's Capability value reports via
	// [CapabilityNameOf] for [CheckCapabilityCoverage] to recognize it.
	Name string
	// Description is shown in the OpenAPI spec for this capability.
	Description string
	// MinLevel, when non-nil, requires the supplied capability matching
	// Name to ALSO implement [LeveledCapability] and report a Level() >=
	// *MinLevel — a genuine VALUE check, not just presence. When nil,
	// presence-by-Name is sufficient.
	MinLevel *int
}

func (r CapabilityRequirement) applyRoute(rb *routeBuilder) {
	rb.requirements = append(rb.requirements, r)
}

// CapabilityName is implemented by an adapter's own sealed Capability
// concrete types to report a human-readable name for
// [CheckCapabilityCoverage]/[stats.CapabilityObserver] purposes. Optional —
// [CapabilityNameOf] falls back to a %T-derived name when a capability
// value does not implement this interface. Byte-identical shape to
// [events.CapabilityName]/[reqreply.CapabilityName].
type CapabilityName interface{ CapabilityName() string }

// CapabilityNameOf returns c's declared name via [CapabilityName], falling
// back to a %T-derived name (e.g. "zeromqrest.QoS") when c does not
// implement [CapabilityName].
func CapabilityNameOf(c any) string {
	if n, ok := c.(CapabilityName); ok {
		return n.CapabilityName()
	}
	return fmt.Sprintf("%T", c)
}

// LeveledCapability is an OPTIONAL extension to an adapter's own sealed
// Capability value (mirrors [CapabilityName]'s existing optional-interface
// pattern — api/rest never imports an adapter package to use this).
// Implement Level() to let a [CapabilityRequirement] with a non-nil
// MinLevel (e.g. from RequireQoS/RequireHWM) be checked for VALUE, not
// just presence, by [CheckCapabilityCoverage].
type LeveledCapability interface {
	CapabilityName
	// Level returns this capability's own ordinal value — semantics are
	// entirely up to the adapter; CheckCapabilityCoverage only ever
	// compares a supplied Level() against the SAME-NAMED requirement's
	// own MinLevel, never across different capability names.
	Level() int
}

// CheckCapabilityCoverage returns [*CapabilityCoverageError] when:
//   - declared contains a [CapabilityRequirement] with NO matching
//     supplied capability at all (by [CapabilityNameOf]) — reported in
//     Missing; OR
//   - a MATCHING supplied capability exists, the requirement's MinLevel is
//     non-nil, and EITHER the supplied value doesn't implement
//     [LeveledCapability] OR its Level() is below MinLevel — reported in
//     Insufficient.
//
// If supplied contains two capabilities with the same [CapabilityNameOf]
// name, the LAST one in the slice wins. Declaring the same Name twice in
// declared is allowed (redundant, harmless) — each occurrence is
// evaluated independently.
//
// Opt-in, exactly like security's [CheckCoverage]: not compiler-enforced,
// called automatically by each adapter's own bulk Attach/Serve dispatch
// at startup, so callers never have to remember to invoke it by hand.
func CheckCapabilityCoverage(routeLabel string, declared []CapabilityRequirement, supplied []any) error {
	have := make(map[string]any, len(supplied))
	for _, c := range supplied {
		have[CapabilityNameOf(c)] = c // last-wins on duplicate Name
	}
	var missing []string
	var insufficient []LevelMismatch
	for _, d := range declared {
		sc, ok := have[d.Name]
		if !ok {
			missing = append(missing, d.Name)
			continue
		}
		if d.MinLevel == nil {
			continue
		}
		lc, ok := sc.(LeveledCapability)
		if !ok {
			insufficient = append(insufficient, LevelMismatch{Name: d.Name, Required: *d.MinLevel, Supplied: -1})
			continue
		}
		if lc.Level() < *d.MinLevel {
			insufficient = append(insufficient, LevelMismatch{Name: d.Name, Required: *d.MinLevel, Supplied: lc.Level()})
		}
	}
	if len(missing) == 0 && len(insufficient) == 0 {
		return nil
	}
	return &CapabilityCoverageError{Route: routeLabel, Missing: missing, Insufficient: insufficient}
}

// CapabilityCoverageError is returned by [CheckCapabilityCoverage],
// aggregating every declared [CapabilityRequirement] that was found
// unmet — either entirely Missing, or present but Insufficient (a value
// below the declared MinLevel, or not implementing [LeveledCapability] at
// all despite MinLevel being set).
type CapabilityCoverageError struct {
	Route        string
	Missing      []string
	Insufficient []LevelMismatch
}

// LevelMismatch reports one [CapabilityRequirement] whose declared
// MinLevel was not met by the matching supplied capability.
type LevelMismatch struct {
	Name     string
	Required int
	Supplied int // -1 when the matching capability didn't implement [LeveledCapability] at all
}

func (e *CapabilityCoverageError) Error() string {
	return fmt.Sprintf("api/rest: route %q declares capabilities %v missing, %v insufficient", e.Route, e.Missing, e.Insufficient)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e *CapabilityCoverageError) LogValue() slog.Value {
	mismatches := make([]any, len(e.Insufficient))
	for i, m := range e.Insufficient {
		mismatches[i] = slog.GroupValue(
			slog.String("name", m.Name),
			slog.Int("required", m.Required),
			slog.Int("supplied", m.Supplied),
		)
	}
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.Any("missing", e.Missing),
		slog.Any("insufficient", mismatches),
	)
}

// VerifyCapabilityCoverage is a thin-adapter convenience wrapper over
// [CheckCapabilityCoverage]: it skips the call entirely when declared is
// empty (the common case — no requirements declared), and converts
// supplied (an adapter's own concrete `[]<pkg>.Capability` slice) to
// `[]any` via a generic loop — pure type erasure, zero protocol-specific
// logic. Every adapter's own Serve/Attach dispatch coverage check should
// use this instead of hand-rolling the guard+conversion+call sequence.
func VerifyCapabilityCoverage[C any](routeLabel string, declared []CapabilityRequirement, supplied []C) error {
	if len(declared) == 0 {
		return nil
	}
	anySupplied := make([]any, len(supplied))
	for i, c := range supplied {
		anySupplied[i] = c
	}
	return CheckCapabilityCoverage(routeLabel, declared, anySupplied)
}

// HeaderCapableTransport is an OPTIONAL capability interface an adapter's
// own per-request carrier type implements when it can extract
// HTTP-header-equivalent key/value pairs from its wire format — Tier 2
// (Implicit) capability checking's structural counterpart to Tier 3a's
// [LeveledCapability] (the difference: Tier 2 asserts on the ADAPTER'S
// TRANSPORT TYPE, since there is no per-declare "supplied capabilities
// slice" for headers/cookies/query the way there is for QoS).
// `ExtractHeaders` is a REAL, callable method (docs/roadmap/
// capability-requirement-composition.md's Phase 6 — promoted from a
// zero-cost, never-invoked marker method to a genuine per-request
// extraction interface) — adapters/nethttp/adapters/chi's own
// per-request `httpCarrier{r}` implement it by wrapping the request's
// own header map; [CheckParamKindCoverage] still only TYPE-ASSERTS
// against this interface (never invokes it) at Attach/Serve setup time,
// so a zero-value carrier remains safe to use for that one check.
type HeaderCapableTransport interface{ ExtractHeaders() map[string]string }

// CookieCapableTransport is [HeaderCapableTransport]'s cookie sibling.
// A transport that structurally cannot carry cookies (e.g. a future
// ZeroMQ REQ/REP adapter — see docs/roadmap/zeromq-rest-adapter.md's
// Open Design Decision #1) correctly, permanently omits this — a
// compiler-visible, diagnosable-at-attach-time outcome, not a silent gap.
type CookieCapableTransport interface{ ExtractCookies() map[string]string }

// QueryCapableTransport is [HeaderCapableTransport]'s query-param
// sibling. Two methods, not one: `opts.MultiValueQueryParams` toggles
// between first-value-wins (`ExtractQuery`) and full multi-value
// (`ExtractQueryMulti`) semantics at every existing call site — both
// forms remain reachable through the carrier, matching that toggle
// exactly.
type QueryCapableTransport interface {
	ExtractQuery() map[string]string
	ExtractQueryMulti() map[string][]string
}

// UnsupportedParamKindError is returned by an adapter's Serve/Attach
// dispatch (ONCE, at setup — never per-request) when a route declares a
// requirement for a param Kind ("Header"/"Cookie"/"Query", including one
// implied by a declared [SecurityScheme]'s In field) that the adapter's
// own transport type does not implement the matching
// [HeaderCapableTransport]/[CookieCapableTransport]/[QueryCapableTransport]
// marker for. Fails FAST at attach time — not a confusing per-request
// validation failure discovered only when a caller happens to hit that
// specific route.
type UnsupportedParamKindError struct {
	Kind    string
	Adapter string
}

func (e UnsupportedParamKindError) Error() string {
	return fmt.Sprintf("api/rest: adapter %q does not support %s parameters", e.Adapter, e.Kind)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnsupportedParamKindError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind),
		slog.String("adapter", e.Adapter),
	)
}

// RequiredParamKinds scans handle's declared param requirements —
// [RouteHandle.HeaderParamNames]/[CookieParamNames]/[QueryParamNames]
// (covering BOTH plain-opt and middleware-merged declarations, per the
// applyParamDeclarations merge already confirmed to populate these
// FIELDS before RouteHandle construction) AND every declared
// [SecurityScheme]'s In field ("header"/"query"/"cookie", e.g.
// route.APIKeyScheme) — returning the SET of param kinds ("Header",
// "Cookie", "Query") this route genuinely requires. An adapter's own
// Serve/Attach dispatch uses this to decide which
// HeaderCapableTransport/CookieCapableTransport/QueryCapableTransport
// markers it must implement, via [UnsupportedParamKindError] on a
// mismatch.
//
// Bearer/OAuth2 [SecurityScheme]s conventionally rely on the Header
// capability (the Authorization header, pure HTTP convention) but have
// NO explicit In field the way APIKey does — this dependency is an
// ACCEPTED, UNENFORCED assumption, not structurally checkable here.
func RequiredParamKinds(headerNames, cookieNames, queryNames []string, schemes map[string]SecurityScheme) map[string]bool {
	kinds := make(map[string]bool, 3)
	if len(headerNames) > 0 {
		kinds["Header"] = true
	}
	if len(cookieNames) > 0 {
		kinds["Cookie"] = true
	}
	if len(queryNames) > 0 {
		kinds["Query"] = true
	}
	for _, s := range schemes {
		switch s.In {
		case "header":
			kinds["Header"] = true
		case "cookie":
			kinds["Cookie"] = true
		case "query":
			kinds["Query"] = true
		}
	}
	return kinds
}

// CheckParamKindCoverage checks EVERY kind in requiredKinds (as produced
// by [RequiredParamKinds]) against transport's own optional capability
// interfaces ([HeaderCapableTransport]/[CookieCapableTransport]/
// [QueryCapableTransport]), returning the FIRST [UnsupportedParamKindError]
// found (deterministic order: Header, Cookie, Query) — a thin-adapter
// convenience wrapper, mirroring [VerifyCapabilityCoverage]'s own
// "don't hand-roll the guard+assert sequence per adapter" philosophy.
// adapter names the calling adapter for the error message. Called ONCE,
// at Serve/AttachServer/Call/AttachClient setup — never per-request.
func CheckParamKindCoverage(adapter string, requiredKinds map[string]bool, transport any) error {
	if requiredKinds["Header"] {
		if _, ok := transport.(HeaderCapableTransport); !ok {
			return UnsupportedParamKindError{Kind: "Header", Adapter: adapter}
		}
	}
	if requiredKinds["Cookie"] {
		if _, ok := transport.(CookieCapableTransport); !ok {
			return UnsupportedParamKindError{Kind: "Cookie", Adapter: adapter}
		}
	}
	if requiredKinds["Query"] {
		if _, ok := transport.(QueryCapableTransport); !ok {
			return UnsupportedParamKindError{Kind: "Query", Adapter: adapter}
		}
	}
	return nil
}
