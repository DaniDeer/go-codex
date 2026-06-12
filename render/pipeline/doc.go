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
