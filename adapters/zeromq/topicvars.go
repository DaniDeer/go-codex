package zeromq

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/DaniDeer/go-codex/api/events"
)

// templateVarRe matches {varName} placeholders in a topic template.
var templateVarRe = regexp.MustCompile(`\{([^}]+)\}`)

// TopicVarsFromMessage is the inverse of [events.ChannelHandle.BuildTopic].
// It matches a concrete ZeroMQ topic (the first frame of a [topic, payload]
// message, as read by [Subscribe]) against the channel's topic template and
// returns the extracted variable values — the zeromq equivalent of
// [adapters/mqtt5.TopicVarsFromMessage] and [adapters/mqtt.TopicVarsFromMessage],
// adapted for zeromq's plain-string topic (no message struct to extract it
// from; ZeroMQ PUB/SUB frames the topic as its own frame).
//
// Template syntax: "{varName}" placeholders capture everything up to the
// next "/" (never crossing a segment boundary); literal text must match
// exactly. No MQTT-style wildcard support (+/#) — ZeroMQ PUB/SUB topic
// filtering is prefix-based, not wildcard-based, so channel topic templates
// declared for zeromq are not expected to contain them.
//
// Typical usage — channel defined with go-codex template variables declared
// via merge-capable [events.NewTopicParam] fields, so the extracted vars can
// be merged directly into the payload via [events.ChannelHandle.DecodeMerged]
// (this is exactly what [Subscribe] does internally whenever the channel
// declares merge fields):
//
//	vars, err := zeromq.TopicVarsFromMessage(sensorChannel, topic)
//	// vars["sensorID"] == "f47ac10b-..."
//
// Returns [TopicMismatchError] if the concrete topic does not match the
// template structure (wrong number of segments or a literal segment does not
// match). Returns [events.InvalidTopicError] if the concrete topic fails the
// builder-level topic codec (see [events.WithTopicConstraints]). Returns
// [events.TopicParamError] if an extracted variable fails its registered
// [events.TopicParam] codec.
func TopicVarsFromMessage[T any](handle *events.ChannelHandle[T], topic string) (map[string]string, error) {
	vars, err := matchTopicTemplate(handle.Topic, topic)
	if err != nil {
		return nil, err
	}
	if err := handle.ValidateTopic(topic); err != nil {
		return nil, err
	}
	if err := handle.ValidateTopicVars(vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// TopicMismatchError is returned by [TopicVarsFromMessage] when the received
// topic does not match the structure of the channel's topic template. Mirrors
// [adapters/mqtt5.TopicMismatchError]/[adapters/mqtt.TopicMismatchError]
// (same shape, no wildcard-specific fields since zeromq templates carry none).
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

// matchTopicTemplate matches a concrete zeromq topic against a go-codex
// topic template ("{varName}" placeholders, no wildcard support). This is a
// local copy rather than a shared core (adapters/zeromq cannot import the
// internal-only api/internal package it would otherwise reuse; see
// docs/roadmap/merge-field-remaining-gaps.md's G2 — a shared, non-wildcard
// core across mqtt/mqtt5/zeromq/ports.File remains a deferred, low-risk
// follow-up, not a blocker).
func matchTopicTemplate(template, topic string) (map[string]string, error) {
	var pattern strings.Builder
	pattern.WriteString("^")
	var names []string
	lastEnd := 0
	for _, loc := range templateVarRe.FindAllStringIndex(template, -1) {
		start, end := loc[0], loc[1]
		pattern.WriteString(regexp.QuoteMeta(template[lastEnd:start]))
		names = append(names, template[start+1:end-1])
		pattern.WriteString("([^/]+)")
		lastEnd = end
	}
	pattern.WriteString(regexp.QuoteMeta(template[lastEnd:]))
	pattern.WriteString("$")

	re := regexp.MustCompile(pattern.String())
	m := re.FindStringSubmatch(topic)
	if m == nil {
		return nil, TopicMismatchError{Template: template, Topic: topic}
	}
	vars := make(map[string]string, len(names))
	for i, name := range names {
		vars[name] = m[i+1]
	}
	return vars, nil
}
