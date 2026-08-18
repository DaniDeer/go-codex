package manifesttemplate

import (
	"fmt"
	"log/slog"
	"regexp"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// ── RouteName / Routes ─────────────────────────────────────────────────────────
//
// See keys.go for RouteKeyPrefix and the dotted-key extraction machinery
// (routeKeyConstraint/routeNameCodec/RouteNameCodec) this file's codecs
// are built from.

// RouteName is the route name extracted from a dotted route key, e.g.
// "factory-mqtt-to-ingest" from
// "properties.desired.routes.factory-mqtt-to-ingest".
type RouteName string

// Routes maps each route's extracted name to its full FROM/INTO definition.
type Routes map[RouteName]Route

// ── RouteTarget ────────────────────────────────────────────────────────────────

// RouteTargetKind discriminates a route's INTO target: either a specific
// module endpoint or the special $upstream literal (routing to IoT Hub
// itself, not another module).
type RouteTargetKind string

const (
	// RouteTargetBrokeredEndpoint targets a specific module's input,
	// e.g. INTO BrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest").
	RouteTargetBrokeredEndpoint RouteTargetKind = "brokeredEndpoint"
	// RouteTargetUpstream targets the literal INTO $upstream — routes
	// the message to IoT Hub instead of another module.
	RouteTargetUpstream RouteTargetKind = "upstream"
)

// RouteTarget is a route's INTO target. Endpoint is only meaningful when
// Kind is RouteTargetBrokeredEndpoint; it is empty for RouteTargetUpstream.
type RouteTarget struct {
	Kind     RouteTargetKind
	Endpoint string
}

// NewBrokeredEndpoint builds a RouteTarget for
// INTO BrokeredEndpoint("<topic>") — the common module-to-module case.
func NewBrokeredEndpoint(topic string) RouteTarget {
	return RouteTarget{Kind: RouteTargetBrokeredEndpoint, Endpoint: topic}
}

// UpstreamTarget is the RouteTarget value for INTO $upstream — a plain
// value, not a function, since $upstream takes no parameters.
var UpstreamTarget = RouteTarget{Kind: RouteTargetUpstream}

// ── Route ──────────────────────────────────────────────────────────────────────

// Route is one $edgeHub route definition: FROM From INTO To. Not every
// FROM/INTO variant IoT Edge Hub supports is modeled — WHERE clauses,
// route priority, and TTL are OUT OF SCOPE (no example seen uses them);
// only the plain "FROM <path> INTO BrokeredEndpoint(\"<topic>\")" /
// "FROM <path> INTO $upstream" forms are supported.
type Route struct {
	// From is the source path/topic this route matches, e.g.
	// "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry".
	From string
	To   RouteTarget
}

// routeRe matches the two supported wire forms:
//
//	FROM <from> INTO BrokeredEndpoint("<endpoint>")
//	FROM <from> INTO $upstream
var routeRe = regexp.MustCompile(`^FROM (\S+) INTO (?:BrokeredEndpoint\("([^"]*)"\)|(\$upstream))$`)

// InvalidRouteError reports a $edgeHub route string that doesn't match
// either supported wire form. Implements slog.LogValuer for structured
// logging.
type InvalidRouteError struct {
	Raw string
}

func (e InvalidRouteError) Error() string {
	return fmt.Sprintf(`manifesttemplate: invalid route %q: want "FROM <path> INTO BrokeredEndpoint(\"<topic>\")" or "FROM <path> INTO $upstream"`, e.Raw)
}

// LogValue implements slog.LogValuer for structured logging.
func (e InvalidRouteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("raw", e.Raw),
	)
}

// RouteCodec is Route's canonical codec — HAND-ROLLED (like
// modulepatch.FieldsPatchCodec) because the wire value is a single
// STRING ("FROM ... INTO ..."), not a JSON object, so codex.Struct
// cannot express it.
var RouteCodec = c.Codec[Route]{
	Encode: func(r Route) (any, error) {
		if r.From == "" {
			return nil, InvalidRouteError{Raw: ""}
		}
		switch r.To.Kind {
		case RouteTargetBrokeredEndpoint:
			if r.To.Endpoint == "" {
				return nil, InvalidRouteError{Raw: r.From}
			}
			return fmt.Sprintf(`FROM %s INTO BrokeredEndpoint(%q)`, r.From, r.To.Endpoint), nil
		case RouteTargetUpstream:
			return fmt.Sprintf(`FROM %s INTO $upstream`, r.From), nil
		default:
			return nil, InvalidRouteError{Raw: r.From}
		}
	},
	Decode: func(raw any) (Route, error) {
		s, ok := raw.(string)
		if !ok {
			return Route{}, c.TypeMismatchError{Expected: "string", Got: fmt.Sprintf("%T", raw)}
		}
		m := routeRe.FindStringSubmatch(s)
		if m == nil {
			return Route{}, InvalidRouteError{Raw: s}
		}
		from := m[1]
		if m[3] == `$upstream` {
			return Route{From: from, To: UpstreamTarget}, nil
		}
		return Route{From: from, To: NewBrokeredEndpoint(m[2])}, nil
	},
	Schema: schema.Schema{
		Type:        "string",
		Description: `A $edgeHub route: "FROM <path> INTO BrokeredEndpoint(\"<topic>\")" or "FROM <path> INTO $upstream".`,
	},
}

// RoutesCodec decodes/encodes the flat "$edgeHub" object directly into
// map[RouteName]Route via codex.Map — K=RouteName (extracted from the
// dotted key), V=Route.
var RoutesCodec = c.Map[RouteName, Route](RouteNameCodec, RouteCodec)
