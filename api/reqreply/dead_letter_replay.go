package reqreply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
)

// UnrecognizedDeadLetterSourceError is returned by [Client.
// ReplayDeadLetter]/[Client.ReplayDeadLetterAsync] when envelope.
// SourceTopic does not match any route registered on the [*Client] (via
// a prior [Client.Call]/[Client.CallAsync]/[Client.RegisterRoute]).
// Deliberately NOT a reuse of [UnrecognizedForwardError]
// (docs/roadmap/reqreply-forward-bounce.md) — even though the
// underlying condition (a registry miss) is mechanically identical, a
// replay caller was never "forwarding" anything, and a distinct type
// keeps the error message/semantics clear for that caller (see
// docs/roadmap/reqreply-dead-letter-replay.md's "Structured error").
type UnrecognizedDeadLetterSourceError struct {
	SourceTopic string
}

func (e UnrecognizedDeadLetterSourceError) Error() string {
	return fmt.Sprintf("api/reqreply: dead-letter source topic %q does not match any route registered on this Client", e.SourceTopic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnrecognizedDeadLetterSourceError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("source_topic", e.SourceTopic),
	)
}

// ReplayDeadLetter reissues a dead-lettered request captured in envelope
// (docs/roadmap/reqreply-dead-letter-replay.md) — the ops/replay
// workflow for recovering a message a route's [DeadLetter] fallback
// previously captured (docs/design/d-0005-error-handling.md's Topic 4).
//
// Republishes envelope.Payload VERBATIM to envelope.SourceTopic — NEVER
// re-decodes/re-encodes it first: DeadLetter fires for BOTH a decode
// failure and an unmatched business error, so re-decoding here would
// fail identically for the decode-failure case, making replay
// impossible for exactly the case most likely to need it (see "The key
// simplification" in the roadmap doc).
//
// Resolves envelope.SourceTopic against THIS Client's own registry
// directly ([UnrecognizedDeadLetterSourceError] on a miss,
// [DuplicateTopicTypeMismatchError] on a poisoned entry) and performs ITS
// OWN loop-free hop-1 dispatch — deliberately NOT [Client.Call]'s/
// [Client.followForward]'s chain-following loop for this FIRST hop: that
// loop unconditionally compares an "originating route"'s declared
// security scheme against every hop's target, which has no answer for a
// COLD start (there is no prior authenticated context to compare
// against — Replay starts fresh from a [DeadLetterEnvelope], not from an
// in-flight [Client.Call]). Forcing the shared loop to run from hop 1
// would reject replay against ANY route declaring security — almost
// certainly the most common real-world case a replay tool exists to
// recover. This mirrors [Client.Call]'s OWN shape: its hop 1 (t.Call)
// also always happens OUTSIDE the loop; only a RECOGNIZED forward reply
// ever enters it.
//
// ONLY if THIS first reply is itself a [ForwardError] does
// ReplayDeadLetter hand off to the EXISTING, UNCHANGED chain-following
// loop (via [Client.followForwardFromHandle]) for hop 2 onward — at that
// point the just-resolved SourceTopic route legitimately becomes the
// "most recently authenticated hop" the security-scheme check compares
// subsequent targets against, exactly mirroring an ordinary forward
// chain's own hop-2+ semantics. This also avoids a MaxForwards
// accounting asymmetry: had hop 1 been pushed into the shared loop
// directly, it would have consumed 1 unit of the MaxForwards budget an
// ordinary Call's own hop 1 never does.
//
// envelope.Properties (Phase 2.0's own wire-level properties addendum)
// is threaded into the hop-1 [ClientTransport.CallRaw] call exactly the
// way [Client.followForwardFromHandle] threads [ForwardError.Properties]
// into every later hop — the mechanism that makes a header-delivered
// credential recoverable at replay time in the first place.
//
// Returns [NoClientTransportAttachedError] if [Client.Attach] was never
// called.
func (c *Client) ReplayDeadLetter(ctx context.Context, envelope DeadLetterEnvelope, opts ...ClientCallOptions) (any, error) {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}

	targetHandle, err := c.resolveDeadLetterSource(envelope.SourceTopic)
	if err != nil {
		return nil, err
	}

	replyBytes, callErr := t.CallRaw(ctx, envelope.SourceTopic, envelope.Payload, envelope.Properties)
	if callErr == nil {
		return decodeResponseReflect(targetHandle, replyBytes)
	}

	var fwd ForwardError
	if errors.As(callErr, &fwd) {
		return c.followForwardFromHandle(ctx, t, targetHandle, fwd, opts...)
	}

	var rawErr ForwardRawErrorReply
	if errors.As(callErr, &rawErr) {
		return decodeErrorForReflect(targetHandle, rawErr)
	}

	return nil, callErr
}

// ReplayDeadLetterAsync is the non-blocking counterpart to [Client.
// ReplayDeadLetter] — returns a *[Future][Resp] (as any) IMMEDIATELY,
// resolved later when the correlated reply arrives, reusing [Client.
// CallAsync]'s own goroutine-wraps-a-synchronous-call SHAPE but NOT its
// exact [FutureFactory]-resolution STEP: CallAsync resolves FutureFactory
// from the route VALUE its own caller passes in
// ([recoverRegistryEntry]); ReplayDeadLetterAsync has no such value —
// only envelope.SourceTopic (a string) — so its FutureFactory instead
// comes from the SAME registry-resolved handle [Client.ReplayDeadLetter]'s
// own hop-1 lookup recovers.
//
// Returns [NoClientTransportAttachedError] if [Client.Attach] was never
// called — checked up front, synchronously, mirroring [Client.CallAsync].
func (c *Client) ReplayDeadLetterAsync(ctx context.Context, envelope DeadLetterEnvelope, opts ...ClientCallOptions) (any, error) {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}

	targetHandle, err := c.resolveDeadLetterSource(envelope.SourceTopic)
	if err != nil {
		return nil, err
	}
	ff, ok := targetHandle.(FutureFactory)
	if !ok {
		return nil, TransportTypeMismatchError{Want: "reqreply.FutureFactory", Got: fmt.Sprintf("%T", targetHandle)}
	}
	future, resolve := ff.NewFutureAny()
	go func() {
		resp, err := c.ReplayDeadLetter(ctx, envelope, opts...)
		resolve(resp, err)
	}()
	return future, nil
}

// resolveDeadLetterSource resolves sourceTopic against c's registry —
// [UnrecognizedDeadLetterSourceError] on a miss, [DuplicateTopicTypeMismatchError]
// on a poisoned entry — shared by both [Client.ReplayDeadLetter] and
// [Client.ReplayDeadLetterAsync]'s own hop-1 lookups.
func (c *Client) resolveDeadLetterSource(sourceTopic string) (any, error) {
	c.mu.RLock()
	entry, ok := c.routes[sourceTopic]
	c.mu.RUnlock()
	if !ok {
		return nil, UnrecognizedDeadLetterSourceError{SourceTopic: sourceTopic}
	}
	if entry.poisoned {
		return nil, DuplicateTopicTypeMismatchError{
			Topic:        sourceTopic,
			ExistingType: reflect.TypeOf(entry.Handle).String(),
			NewType:      entry.conflictingType,
		}
	}
	return entry.Handle, nil
}
