package zeromq

import (
	"github.com/DaniDeer/go-codex/api/events"
)

// caller is a client-side convenience holder bundling the two things
// every zeromq pub/sub call needs — a [FramedSocket] and an optional
// [events.Client] — removing repeated sock/eventsClient boilerplate
// across many [subscribe]/[serveOneSubscriber] calls (both internal
// helpers used by this package's public [SubscribeAdapter]/[Attach]
// paths). Confirmed via code this is a direct, 2-field mapping onto
// zeromq's EXISTING [subscribe]/[publish] shape (both already take a
// single client-like sock [FramedSocket] param; zeromq genuinely has no
// router-equivalent — sock already plays the "client" role directly) —
// no new capability needed, unlike e.g. adapters/mqtt(v3)'s Caller (see
// docs/design/d-0002-pubsub-workflow-simplification.md's "Caller's mirroring
// across mqtt(v3)/zeromq" subsection).
//
// Construct once per socket via [newCaller], then pass it to [subscribe]
// (the value-based convenience) or call [caller.ServeSubscribers]/
// [serveOneSubscriber] to consume every channel registered against the
// bundled [events.Client] in one call.
type caller struct {
	sock   FramedSocket
	events *events.Client
}

// newCaller builds a [caller] bound to sock and events. events MAY be nil
// — a nil events.Client produces spec-free [events.ChannelHandle] values
// when [subscribe] calls sub.Handle(caller.events) internally (the common
// case for an application that only wants to consume/produce messages,
// without also registering an AsyncAPI spec) — but MUST be non-nil for
// [caller.ServeSubscribers] to have anything to walk (see
// [events.Client.SubscriberEntries]).
func newCaller(sock FramedSocket, ev *events.Client) *caller {
	return &caller{sock: sock, events: ev}
}
