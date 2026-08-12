package iotedge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DaniDeer/go-codex/ports"
)

func TestNewConfigDir_ListDiscoversUseCases(t *testing.T) {
	dir := t.TempDir()

	manifest := sampleManifest()
	raw, err := ConfigFileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile usecase1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usecase2.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile usecase2: %v", err)
	}
	// Stray non-conforming file — must be silently excluded.
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile .gitkeep: %v", err)
	}

	configDir := NewConfigDir(dir)
	entries, err := configDir.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2 (stray .gitkeep excluded): %+v", len(entries), entries)
	}

	useCases := map[string]bool{}
	for _, e := range entries {
		useCases[e.Vars["useCase"]] = true
		if e.Kind != ports.EntryFile {
			t.Errorf("entry %q Kind = %v, want EntryFile", e.Name, e.Kind)
		}
	}
	if !useCases["usecase1"] || !useCases["usecase2"] {
		t.Errorf("discovered use cases = %v, want usecase1 and usecase2", useCases)
	}
}

func TestNewConfigDir_ListThenReadDiscoveredManifest(t *testing.T) {
	dir := t.TempDir()

	manifest := sampleManifest()
	raw, err := ConfigFileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	configDir := NewConfigDir(dir)
	entries, err := configDir.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}

	// Discovery (List) feeds directly into NewConfigFile's own path — the
	// same declarative flow app/iotedge would use to read whichever use
	// case a caller picked.
	fh := NewConfigFile(filepath.Join(dir, entries[0].Name))
	got, err := fh.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read discovered manifest: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Status != "running" {
		t.Errorf("Status = %q, want running", got.ModulesContent.EdgeAgent["factory-dashboard"].Status)
	}
}
