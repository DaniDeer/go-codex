package iotedge

import (
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose"
	fromcompose "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/fromcompose"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	"github.com/DaniDeer/go-codex/ports"
)

// ── ImportDockerComposeAsUseCase ─────────────────────────────────────────────

// ImportDockerComposeAsUseCase reads composeFilePath via
// [dockercompose.NewFile] (a plain, arbitrary-path [ports.File] port —
// see its own doc comment), converts the result into a scaffold
// iothub.LayeredDeployment via fromcompose.ConvertProject, and writes
// the result as useCaseName's own use-case template file under basePath
// — reusing usecase.NewFile's EXISTING templated file port, zero new
// file-layout code. Returns every [fromcompose.Warning] the conversion
// produced (so a caller/CLI can print them — see fromcompose's own doc
// comment for the "scaffold, not full fidelity" contract these warnings
// document) alongside any hard I/O/decode error.
//
// A caller that already has a decoded dockercompose.Project in memory
// (e.g. from a different source than a local file) should call
// fromcompose.ConvertProject directly instead — this function exists
// purely as the concrete "read from disk, write to disk" convenience,
// mirroring [ReadUseCase]/[UpdateUseCaseModuleImage]'s own file-I/O
// wrapper convention (now built on [ports.File] throughout, not raw
// os.ReadFile/os.WriteFile).
func ImportDockerComposeAsUseCase(basePath usecase.BasePath, useCaseName usecase.Name, composeFilePath string, opts ports.FileOptions) ([]fromcompose.Warning, error) {
	project, err := dockercompose.NewFile(composeFilePath).Read(nil, opts)
	if err != nil {
		return nil, err
	}

	deployment, warnings := fromcompose.ConvertProject(project)

	if _, err := usecase.NewFile(basePath).Write(
		map[string]string{"usecase_name": string(useCaseName)},
		deployment,
		opts,
	); err != nil {
		return warnings, err
	}

	return warnings, nil
}

// ── ExportUseCaseAsDockerCompose ──────────────────────────────────────────

// ExportUseCaseAsDockerCompose is the REVERSE of
// [ImportDockerComposeAsUseCase]: reads useCaseName's own deployment
// manifest under basePath via [usecase.NewFile], converts it back into
// a Compose project via fromcompose.ConvertDeployment, and writes the
// result to composeFilePath via [dockercompose.NewFile] (the same
// plain, arbitrary-path [ports.File] port [ImportDockerComposeAsUseCase]
// reads through). Returns every [fromcompose.Warning] the reverse
// conversion produced (e.g. a restart policy with no exact Compose
// equivalent) alongside any hard I/O/decode error.
//
// See fromcompose's own doc comment for what this reverse direction
// CANNOT recover (a module's original build context, mem_reservation,
// and its ORIGINAL pre-sanitization service name) — the resulting
// compose file is a faithful STARTING POINT, not a byte-for-byte
// reconstruction of whatever compose file (if any) originally produced
// this use case.
func ExportUseCaseAsDockerCompose(basePath usecase.BasePath, useCaseName usecase.Name, composeFilePath string, opts ports.FileOptions) ([]fromcompose.Warning, error) {
	deployment, err := ReadUseCase(basePath, useCaseName, opts)
	if err != nil {
		return nil, err
	}

	project, warnings := fromcompose.ConvertDeployment(deployment)

	if _, err := dockercompose.NewFile(composeFilePath).Write(nil, project, opts); err != nil {
		return warnings, err
	}

	return warnings, nil
}
