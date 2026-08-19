// Package forge-patch demonstrates forge.Patch — a governed, named,
// versioned, observable pipeline step that applies a PartialStruct-built
// patch onto a base config value.
//
// Use case: a config service holds a BASE application config in memory
// and periodically receives PATCHES (partial overrides — e.g. from an
// admin API or a config-management system) that must be applied and
// re-validated before taking effect. codex.ApplyPatch does the actual
// merge; forge.Patch wraps it so the "apply patch" step participates in
// the SAME Registry/observer/pipeline-spec machinery as every other
// governed computation step in the service.
//
// Run with: go run ./examples/forge-patch
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/render/pipeline"
	"github.com/DaniDeer/go-codex/validate"
)

// AppConfig is the BASE config — every field always present.
type AppConfig struct {
	LogLevel   string
	MaxWorkers int
	Region     string
}

var appConfigCodec = codex.Struct[AppConfig](
	codex.RequiredField("logLevel",
		codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
		func(c AppConfig) string { return c.LogLevel },
		func(c *AppConfig, v string) { c.LogLevel = v },
	),
	codex.RequiredField("maxWorkers",
		codex.Int().Refine(validate.RangeInt(1, 100)),
		func(c AppConfig) int { return c.MaxWorkers },
		func(c *AppConfig, v int) { c.MaxWorkers = v },
	),
	codex.RequiredField("region",
		codex.String().Refine(validate.NonEmptyString),
		func(c AppConfig) string { return c.Region },
		func(c *AppConfig, v string) { c.Region = v },
	),
)

// AppConfigPatch is the PATCH — every field independently optional
// (nil = untouched), built via codex.PartialField/PartialStruct.
type AppConfigPatch struct {
	LogLevel   *string
	MaxWorkers *int
}

var appConfigPatchCodec = codex.PartialStruct[AppConfigPatch](
	codex.PartialField("logLevel",
		codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
		func(p AppConfigPatch) *string { return p.LogLevel },
		func(p *AppConfigPatch, v *string) { p.LogLevel = v },
	),
	codex.PartialField("maxWorkers",
		codex.Int().Refine(validate.RangeInt(1, 100)),
		func(p AppConfigPatch) *int { return p.MaxWorkers },
		func(p *AppConfigPatch, v *int) { p.MaxWorkers = v },
	),
)

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}

func main() {
	// forge.Patch: a NAMED, VERSIONED, contract-hashed pipeline step —
	// register it in a Registry, and its Kind ("patch") + two named
	// input ports ("base"/"patch") show up in the rendered pipeline spec
	// just like any other forge Function.
	applyConfigPatch := forge.Patch("applyConfigPatch", "1.0.0",
		appConfigCodec, appConfigPatchCodec,
		forge.FunctionMeta{Description: "Applies an AppConfigPatch onto the current AppConfig."},
	)

	reg := forge.NewRegistry("Config Service", "1.0.0")
	applyConfigPatch.Register(reg)

	base := AppConfig{LogLevel: "info", MaxWorkers: 4, Region: "eu-west-1"}
	fmt.Printf("base config:    %+v\n", base)

	// Admin API receives a partial override: bump maxWorkers, leave
	// logLevel and region untouched.
	newMax := 16
	patch := AppConfigPatch{MaxWorkers: &newMax}

	updated, err := applyConfigPatch.Apply(forge.PatchInput[AppConfig, AppConfigPatch]{
		Base:  base,
		Patch: patch,
	})
	must(err, "applyConfigPatch.Apply")
	fmt.Printf("updated config: %+v\n", updated)

	// An invalid patch value still gets caught — the merged result is
	// re-validated through appConfigCodec's own Refine constraints.
	badLevel := "trace" // not in the allowed OneOf set
	_, err = applyConfigPatch.Apply(forge.PatchInput[AppConfig, AppConfigPatch]{
		Base:  updated,
		Patch: AppConfigPatch{LogLevel: &badLevel},
	})
	fmt.Printf("invalid patch rejected: %v\n", err)

	// The pipeline spec shows applyConfigPatch's Kind="patch" and its two
	// named input ports — no different from Map/Filter/Reduce/MapValues.
	fmt.Println("\n── Pipeline YAML spec ──")
	spec, err := pipeline.Render(reg.Spec())
	must(err, "pipeline.Render")
	fmt.Println(string(spec))
}
