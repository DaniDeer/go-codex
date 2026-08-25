// Temporary demo program — NOT part of the repo. Converts the example
// docker-compose.yml to an IoT Edge deployment manifest, and the example
// usecase1.json deployment manifest to a docker-compose.yml, using
// app/iotedge's ImportDockerComposeAsUseCase/ExportUseCaseAsDockerCompose
// directly — the SAME entry points a real caller/CLI would use, which
// now read/write both the Compose file (via dockercompose.NewFile) and
// the use-case manifest (via usecase.NewFile) through go-codex's
// ports.File, not raw os.ReadFile/os.WriteFile.
//
// Run from the repo root: go run ./examples/go-edge-models/examples/temp
package main

import (
	"fmt"
	"os"

	iotedgeapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/iotedge"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	"github.com/DaniDeer/go-codex/ports"
)

const (
	composeIn      = "examples/go-edge-models/examples/docker-compose/docker-compose.yml"
	tempBasePath   = "examples/go-edge-models/examples/temp" // {tempBasePath}/usecases/factory-stack.json
	existingBase   = "examples/go-edge-models/examples"      // {existingBase}/usecases/usecase1.json already exists
	outComposeYAML = "examples/go-edge-models/examples/temp/usecase1.docker-compose.yml"
)

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}

func mustName(s string) usecase.Name {
	n, err := usecase.NewName(s)
	must(err, "construct use case name "+s)
	return n
}

func mustBasePath(s string) usecase.BasePath {
	bp, err := usecase.NewBasePath(s)
	must(err, "construct base path "+s)
	return bp
}

func main() {
	// ── 1. docker-compose.yml → IoT Edge deployment manifest ────────────
	//
	// ImportDockerComposeAsUseCase reads composeIn via dockercompose.NewFile
	// (ports.File[dockercompose.Project]), converts it via
	// fromcompose.ConvertProject, and writes the result as the "factory-stack"
	// use case under tempBasePath via usecase.NewFile — landing at
	// tempBasePath + "/usecases/factory-stack.json" (usecase.NewFile's own
	// templated path, CreateDirs makes the new "usecases" subdir).
	factoryStack := mustName("factory-stack")
	importWarnings, err := iotedgeapp.ImportDockerComposeAsUseCase(
		mustBasePath(tempBasePath), factoryStack, composeIn,
		ports.FileOptions{CreateDirs: true},
	)
	must(err, "import docker-compose.yml as use case")
	fmt.Printf("=== docker-compose.yml -> deployment manifest (factory-stack) ===\n")
	fmt.Printf("warnings: %d\n", len(importWarnings))
	for _, w := range importWarnings {
		fmt.Printf("  - %s\n", w.Error())
	}
	fmt.Printf("wrote %s/usecases/%s.json\n\n", tempBasePath, factoryStack)

	// ── 2. usecase1.json deployment manifest → docker-compose.yml ──────
	//
	// ExportUseCaseAsDockerCompose reads the ALREADY-EXISTING "usecase1" use
	// case under existingBase via usecase.NewFile, converts it via
	// fromcompose.ConvertDeployment, and writes the result to outComposeYAML
	// via dockercompose.NewFile — the reverse of step 1.
	usecase1 := mustName("usecase1")
	exportWarnings, err := iotedgeapp.ExportUseCaseAsDockerCompose(
		mustBasePath(existingBase), usecase1, outComposeYAML,
		ports.FileOptions{CreateDirs: true},
	)
	must(err, "export usecase1 as docker-compose.yml")
	fmt.Printf("=== usecase1.json -> docker-compose.yml ===\n")
	fmt.Printf("warnings: %d\n", len(exportWarnings))
	for _, w := range exportWarnings {
		fmt.Printf("  - %s\n", w.Error())
	}
	fmt.Printf("wrote %s\n", outComposeYAML)
}
