package rest

import "fmt"

// QoSLevel is a DECLARATION-ONLY vocabulary for [RequireQoS] — it never
// crosses into an adapter's own sealed Capability type. Its int values
// intentionally mirror MQTT's own wire QoS byte (0/1/2), matching
// [events.QoSLevel]/[reqreply.QoSLevel] numerically, but this type itself
// is never compared against or converted to those — only used to shape
// [CapabilityRequirement.Description] and to set
// [CapabilityRequirement.MinLevel] for [CheckCapabilityCoverage]'s
// value-aware check via the supplied capability's own [LeveledCapability]
// implementation. Own type, not shared with events'/reqreply's — same
// "cheap to duplicate, avoid a cross-API import" reasoning as
// [CapabilityRequirement].
type QoSLevel int

const (
	AtMostOnce QoSLevel = iota
	AtLeastOnce
	ExactlyOnce
)

// String returns a human-readable label used in [RequireQoS]'s generated
// [CapabilityRequirement.Description].
func (l QoSLevel) String() string {
	switch l {
	case AtLeastOnce:
		return "at-least-once"
	case ExactlyOnce:
		return "exactly-once"
	default:
		return "at-most-once"
	}
}

// RequireQoS declares, at the ROUTE level — independent of any concrete
// adapter — that this route needs AT LEAST the given QoS level from
// whichever adapter attaches. Sugar over
// CapabilityRequirement{Name: "QoS", ...}.
//
// As of this writing, no shipped REST adapter supplies a concrete QoS
// value — HTTP has no QoS concept. This mechanism exists for a FUTURE
// non-HTTP REST-eligible transport (see docs/roadmap/zeromq-rest-adapter.md)
// to satisfy, mirroring this codebase's own "declare first, adapter
// satisfies second" philosophy.
//
// Example:
//
//	route := rest.NewRoute[Req, Resp]("POST", "/compute/add", reqCodec, respCodec,
//	    rest.RequireQoS(rest.AtLeastOnce),
//	)
func RequireQoS(level QoSLevel) RouteOpt {
	min := int(level)
	return CapabilityRequirement{
		Name:        "QoS",
		Description: fmt.Sprintf("requires at least %s delivery", level),
		MinLevel:    &min,
	}
}

// RequireHWM declares that this route needs high-water-mark backpressure
// control of AT LEAST minimum. Sugar over
// CapabilityRequirement{Name: "HWM", ...}, checked for VALUE (not just
// presence) via the supplied capability's [LeveledCapability]
// implementation, same mechanism [RequireQoS] uses.
func RequireHWM(minimum int) RouteOpt {
	min := minimum
	return CapabilityRequirement{
		Name:        "HWM",
		Description: fmt.Sprintf("requires HWM >= %d", minimum),
		MinLevel:    &min,
	}
}
