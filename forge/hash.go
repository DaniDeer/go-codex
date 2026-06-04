package forge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// computeHash returns "sha256:<hex>" over the canonical JSON of the computation
// contract: (name, version, inputs[].schema, output.schema).
//
// Governance fields (Author, ApprovedBy, ApprovedAt) are excluded — changing who
// approved a function does not alter what it computes, so the hash stays stable.
func computeHash(name, version string, inputs []PortSpec, output PortSpec) string {
	type contract struct {
		Name    string     `json:"name"`
		Version string     `json:"version"`
		Inputs  []PortSpec `json:"inputs"`
		Output  PortSpec   `json:"output"`
	}
	data, _ := json.Marshal(contract{
		Name:    name,
		Version: version,
		Inputs:  inputs,
		Output:  output,
	})
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h)
}
