package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoCapabilityMechanism shows [file.Capability] — mirrors
// adapters/mqtt5's sealed Capability model:
//   - [file.AccessMode] (ReadOnly) rejects Create BEFORE any Backend
//     call at all — a declare-time Capability, not a per-call option.
//   - [file.NativePOSIXMode] ALWAYS wins over a conflicting per-call
//     WriteOptions.Perm — "Capability overrides per-call option" (see
//     docs/roadmap/file-api.md's precedence rule).
func demoCapabilityMechanism(ctx context.Context, client *file.Client, rt file.Router, dir string) {
	readOnlyCreator := resources.Notes.WithCreate(file.Create{}).WithCapabilities(file.AccessModeReadOnly)
	readOnlyHandle, err := readOnlyCreator.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(read-only create): %v", err)
	}
	_, err = client.Create(ctx, readOnlyHandle, resources.Note{Title: "x", Body: "y"}, map[string]string{"id": "readonly-test"})
	var unsupported file.BackendUnsupportedOptionError
	if errors.As(err, &unsupported) {
		fmt.Println("confirmed: AccessModeReadOnly rejects Create BEFORE any Backend call")
	} else {
		log.Fatalf("expected BackendUnsupportedOptionError, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "readonly-test.json")); !os.IsNotExist(statErr) {
		log.Fatalf("readonly-test.json should NOT exist — Capability must reject before any Backend call")
	}

	// NativePOSIXMode ALWAYS wins over a conflicting per-call Perm.
	forcedPerm := os.FileMode(0640)
	nativeMode := file.NativePOSIXMode(0600)
	strictCreator := resources.Notes.WithCreate(file.Create{}).WithCapabilities(nativeMode)
	strictHandle, err := strictCreator.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(posix-mode create): %v", err)
	}
	if _, err := client.Create(ctx, strictHandle, resources.Note{Title: "x", Body: "y"}, map[string]string{"id": "perm-test"}, file.WriteOptions{Perm: forcedPerm}); err != nil {
		log.Fatalf("create (perm-test): %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "perm-test.json"))
	if err != nil {
		log.Fatalf("stat perm-test.json: %v", err)
	}
	fmt.Printf("perm-test.json mode=%v (NativePOSIXMode 0600 won over WriteOptions.Perm 0640)\n", info.Mode().Perm())

	_ = os.Remove(filepath.Join(dir, "perm-test.json"))
}
