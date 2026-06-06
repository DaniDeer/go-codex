// Package pipeline renders a forge.PipelineSpec as a YAML document.
//
// The output format mirrors OpenAPI/AsyncAPI: a top-level "pipelineSpec" version
// field, an "info" section, a "functions" list (each entry has governance metadata,
// contract hash, and input/output schemas), and a "graph" section listing inferred
// dependency edges (which function produces each input).
//
// Typical usage:
//
//	reg := forge.NewRegistry("OEE Calculation Pipeline", "1.0.0").
//	    WithDescription("Signed, governed OEE computation pipeline.")
//	availabilityCalc.Register(reg)
//	performanceCalc.Register(reg)
//	oeeCalc.Register(reg)
//
//	yamlBytes, err := pipeline.Render(reg.Spec())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(string(yamlBytes))
package pipeline

import (
	"fmt"

	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/render/internal/schemarender"
	"gopkg.in/yaml.v3"
)

// Render serialises spec as a pipelineSpec YAML document.
//
// The output is deterministic for a given spec (functions appear in registration
// order, graph edges in consumer-then-input order). Suitable for git diffs.
func Render(spec forge.PipelineSpec) ([]byte, error) {
	doc := buildDocument(spec)
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("render/pipeline: marshal YAML: %w", err)
	}
	return out, nil
}

func buildDocument(spec forge.PipelineSpec) map[string]any {
	doc := map[string]any{
		"pipelineSpec": "1.0",
		"info":         buildInfo(spec.Info),
	}
	if len(spec.Functions) > 0 {
		doc["functions"] = buildFunctions(spec.Functions)
	}
	if len(spec.Graph) > 0 {
		doc["graph"] = buildGraph(spec.Graph)
	}
	return doc
}

func buildInfo(info forge.PipelineInfo) map[string]any {
	m := map[string]any{
		"title":   info.Title,
		"version": info.Version,
	}
	if info.Description != "" {
		m["description"] = info.Description
	}
	if info.Author != "" {
		m["author"] = info.Author
	}
	if info.ApprovedBy != "" {
		m["approvedBy"] = info.ApprovedBy
	}
	if info.ApprovedAt != "" {
		m["approvedAt"] = info.ApprovedAt
	}
	return m
}

func buildFunctions(fns []forge.FunctionSpec) []map[string]any {
	out := make([]map[string]any, 0, len(fns))
	for _, fn := range fns {
		out = append(out, buildFunction(fn))
	}
	return out
}

func buildFunction(fn forge.FunctionSpec) map[string]any {
	m := map[string]any{
		"name":    fn.Name,
		"version": fn.Version,
		"hash":    fn.Hash,
	}
	if fn.Kind != forge.FunctionKindScalar {
		m["kind"] = fn.Kind
	}
	if fn.Wraps != "" {
		m["wraps"] = fn.Wraps
	}
	if fn.Description != "" {
		m["description"] = fn.Description
	}
	if fn.Author != "" {
		m["author"] = fn.Author
	}
	if fn.ApprovedBy != "" {
		m["approvedBy"] = fn.ApprovedBy
	}
	if fn.ApprovedAt != "" {
		m["approvedAt"] = fn.ApprovedAt
	}

	inputs := make([]map[string]any, 0, len(fn.Inputs))
	for _, inp := range fn.Inputs {
		inputs = append(inputs, map[string]any{
			"name":   inp.Name,
			"schema": schemarender.SchemaObject(inp.Schema),
		})
	}
	m["inputs"] = inputs

	m["output"] = map[string]any{
		"name":   fn.Output.Name,
		"schema": schemarender.SchemaObject(fn.Output.Schema),
	}
	return m
}

func buildGraph(edges []forge.GraphEdge) []map[string]any {
	out := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		entry := map[string]any{
			"function":   e.Function,
			"input":      e.Input,
			"producedBy": e.ProducedBy,
		}
		out = append(out, entry)
	}
	return out
}
