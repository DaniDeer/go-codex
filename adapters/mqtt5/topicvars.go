package mqtt5

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/internal/templatematch"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// TopicVarsFromMessage is the inverse of [events.ChannelHandle.BuildTopic].
// It matches the concrete MQTT topic from msg against the channel's topic
// template and returns the extracted variable values — the mqtt5 equivalent
// of adapters/mqtt's existing TopicVarsFromMessage (same behavior, adapted
// for [*pahomqtt5.Publish]).
//
// Template syntax:
//   - {varName} — matches exactly one topic level; the segment value is
//     captured under "varName" in the returned map.
//   - Literal segments — must match the concrete topic exactly.
//   - MQTT single-level wildcard (+) — matches exactly one level (anonymous
//     capture is omitted from the result map).
//   - MQTT multi-level wildcard (#) as the last template segment — matches
//     all remaining levels; the remaining path is captured under "#" in the
//     result map.
//
// Typical usage — channel defined with go-codex template variables, and
// declared with merge-capable [events.NewTopicParam] fields so the extracted
// vars can be merged directly into the payload via
// [events.ChannelHandle.DecodeMerged] (this is exactly what [Subscribe]
// does internally whenever the channel declares merge fields):
//
//	vars, err := mqtt5.TopicVarsFromMessage(sensorChannel, msg)
//	// vars["sensorID"] == "f47ac10b-..."
//
// Returns [TopicMismatchError] if the concrete topic does not match the
// template structure (wrong number of levels or a literal segment does not
// match). Returns [events.InvalidTopicError] if the concrete topic fails the
// builder-level topic codec (see [events.WithTopicConstraints]). Returns
// [events.TopicParamError] if an extracted variable fails its registered
// [events.TopicParam] codec.
func TopicVarsFromMessage[T any](handle *events.ChannelHandle[T], msg *pahomqtt5.Publish) (map[string]string, error) {
	vars, err := matchTopicTemplate(handle.Topic, msg.Topic)
	if err != nil {
		return nil, err
	}
	if err := handle.ValidateTopic(msg.Topic); err != nil {
		return nil, err
	}
	if err := handle.ValidateTopicVars(vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// TopicMismatchError is returned by [TopicVarsFromMessage] when the received
// topic does not match the structure of the channel's topic template.
// Mirrors adapters/mqtt's TopicMismatchError exactly.
//
// Use errors.As to inspect the mismatched segments:
//
//	var mm mqtt5.TopicMismatchError
//	if errors.As(err, &mm) {
//	    log.Printf("topic %q does not match template %q", mm.Topic, mm.Template)
//	}
type TopicMismatchError struct {
	Template string // the channel topic template (e.g. "sensors/{sensorID}/data")
	Topic    string // the received concrete topic (e.g. "sensors/abc/extra/data")
}

func (e TopicMismatchError) Error() string {
	return fmt.Sprintf("topic %q does not match template %q", e.Topic, e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e TopicMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("topic", e.Topic),
	)
}

// matchTopicTemplate performs the level-by-level matching of a concrete MQTT
// topic against a go-codex topic template. Delegates to
// [templatematch.MatchMQTTWildcard] — the shared, module-internal core also
// used by adapters/mqtt (same MQTT wildcard semantics), since
// api/internal.MatchTemplate deliberately has no wildcard support and
// adapters/mqtt5 cannot import that api/*-only package anyway. See
// docs/roadmap/merge-field-remaining-gaps.md (G2).
func matchTopicTemplate(template, topic string) (map[string]string, error) {
	return templatematch.MatchMQTTWildcard(template, topic, func(template, topic string) error {
		return TopicMismatchError{Template: template, Topic: topic}
	})
}
