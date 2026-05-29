package mqtt

import (
	"fmt"
	"strings"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
)

// TopicVarsFromMessage is the inverse of [events.ChannelHandle.BuildTopic].
// It matches the concrete MQTT topic from msg against the channel's topic template
// and returns the extracted variable values.
//
// Template syntax:
//   - {varName} — matches exactly one topic level; the segment value is captured
//     under "varName" in the returned map.
//   - Literal segments — must match the concrete topic exactly.
//   - MQTT single-level wildcard (+) — matches exactly one level (anonymous capture
//     is omitted from the result map).
//   - MQTT multi-level wildcard (#) as the last template segment — matches all
//     remaining levels; the remaining path is captured under "#" in the result map.
//
// Typical usage — channel defined with go-codex template variables:
//
//	sensorChannel, _ := events.AddChannel[SensorReading](b, "sensors/{sensorID}/measurements", ...)
//	client.Subscribe("sensors/+/measurements", 1, mqtt.SubscribeHandler(ctx, sensorChannel,
//	    func(ctx context.Context, r SensorReading) error {
//	        msg, _ := mqtt.MessageFromContext(ctx)
//	        vars, err := mqtt.TopicVarsFromMessage(sensorChannel, msg)
//	        // vars["sensorID"] == "f47ac10b-..."
//	        ...
//	    }, mqtt.SubscribeOptions{}),
//	)
//
// Returns [TopicMismatchError] if the concrete topic does not match the template
// structure (wrong number of levels or a literal segment does not match).
// Returns [events.InvalidTopicError] if the concrete topic fails the builder-level
// topic codec (see [events.WithTopicConstraints]).
// Returns [events.TopicParamError] if an extracted variable fails its registered
// [events.TopicParam] codec.
func TopicVarsFromMessage[T any](handle *events.ChannelHandle[T], msg pahomqtt.Message) (map[string]string, error) {
	vars, err := matchTopicTemplate(handle.Topic, msg.Topic())
	if err != nil {
		return nil, err
	}
	if err := handle.ValidateTopic(msg.Topic()); err != nil {
		return nil, err
	}
	if err := handle.ValidateTopicVars(vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// TopicMismatchError is returned by [TopicVarsFromMessage] when the received
// topic does not match the structure of the channel's topic template.
//
// Use errors.As to inspect the mismatched segments:
//
//	var mm mqtt.TopicMismatchError
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

// matchTopicTemplate performs the level-by-level matching of a concrete MQTT topic
// against a go-codex topic template.
func matchTopicTemplate(template, topic string) (map[string]string, error) {
	tmplParts := strings.Split(template, "/")
	topicParts := strings.Split(topic, "/")

	vars := make(map[string]string)

	for i, seg := range tmplParts {
		// Multi-level wildcard — must be last segment; captures everything remaining.
		if seg == "#" {
			vars["#"] = strings.Join(topicParts[i:], "/")
			return vars, nil
		}

		if i >= len(topicParts) {
			return nil, TopicMismatchError{Template: template, Topic: topic}
		}

		concreteSeg := topicParts[i]

		switch {
		case len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}':
			// {varName} placeholder — capture the concrete segment value.
			name := seg[1 : len(seg)-1]
			vars[name] = concreteSeg

		case seg == "+":
			// Anonymous single-level wildcard — match any segment, no capture.

		default:
			// Literal segment — must match exactly.
			if seg != concreteSeg {
				return nil, TopicMismatchError{Template: template, Topic: topic}
			}
		}
	}

	// All template segments consumed; the concrete topic must have no extra segments.
	if len(topicParts) != len(tmplParts) {
		return nil, TopicMismatchError{Template: template, Topic: topic}
	}

	return vars, nil
}
