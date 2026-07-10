// Package stream renders a [stream.TopologySpec] as a human-readable YAML
// stream topology document. The output describes the pipeline's sources,
// operators, and sinks — analogous to [render/pipeline] for forge.PipelineSpec.
//
// Usage:
//
//	topo := stream.NewTopology("Sensor OEE Pipeline", "1.0.0").
//	    WithSource("mqtt/sensors/+", "Raw sensor readings").
//	    WithApply(oeeCalcFn).  // captures forge function hash for auditability
//	    WithFilter("oee < 0.65").
//	    WithSink("mqtt/alerts/oee", "Low-OEE alerts")
//
//	yamlBytes, err := streamrender.Render(topo.Spec())
package stream

import (
	"fmt"

	gstream "github.com/DaniDeer/go-codex/stream"
	"gopkg.in/yaml.v3"
)

// Render serialises spec as a streamTopology YAML document.
// The output is deterministic for a given spec (steps appear in registration order).
func Render(spec gstream.TopologySpec) ([]byte, error) {
	doc := buildDocument(spec)
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("render/stream: marshal YAML: %w", err)
	}
	return out, nil
}

func buildDocument(spec gstream.TopologySpec) map[string]any {
	doc := map[string]any{
		"streamTopology": "1.0",
		"info":           buildInfo(spec.Info),
	}
	if len(spec.Steps) > 0 {
		doc["pipeline"] = buildSteps(spec.Steps)
	}
	return doc
}

func buildInfo(info gstream.TopologyInfo) map[string]any {
	m := map[string]any{
		"title":   info.Title,
		"version": info.Version,
	}
	if info.Description != "" {
		m["description"] = info.Description
	}
	return m
}

func buildSteps(steps []gstream.TopologyStep) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		out = append(out, buildStep(s))
	}
	return out
}

func buildStep(s gstream.TopologyStep) map[string]any {
	m := map[string]any{"kind": string(s.Kind)}
	if s.Name != "" {
		m["name"] = s.Name
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Function != nil {
		fn := map[string]any{
			"function": s.Function.Name,
			"version":  s.Function.Version,
		}
		if s.Function.Hash != "" {
			fn["hash"] = s.Function.Hash
		}
		for k, v := range fn {
			m[k] = v
		}
	}
	return m
}
