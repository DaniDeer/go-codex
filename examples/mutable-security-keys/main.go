// Package mutable-security-keys demonstrates codex.Mutable[T] — a
// re-validated, hot-reloadable value cell — for the driving use case
// that motivated it: key/credential rotation for a SecurityFunc/
// CredentialFunc closure, without a process restart.
//
// Unlike codex.Immutable[T] (set EXACTLY once, panics on a second Set),
// Mutable[T] can be Set repeatedly — each call re-validates against the
// SAME Codec[T] used at construction, and an invalid Set leaves the
// current value UNCHANGED (last-good-value-wins) instead of corrupting
// live traffic with a bad key.
//
// Two scenes:
//   - A background rotation loop calling Set on a schedule (simulating
//     a JWKS refresh), observed via codex.WithReloadObserver.
//   - A SecurityFunc-shaped closure calling Get() on every simulated
//     request — always sees the CURRENT key, never a stale one, with
//     zero coordination beyond the Mutable cell itself.
//
// # Running
//
// go run ./examples/mutable-security-keys
package main

import (
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// rotationObserver logs every reload attempt — the same shape a real
// stats.Observer-based type would satisfy structurally (see
// stats.AsReloadObserver for bridging an existing stats.Observer value
// into this position instead of a dedicated type like this one).
type rotationObserver struct{}

func (rotationObserver) RecordReload(location string, success bool, _ time.Duration) {
	fmt.Printf("  [observer] reload %q: success=%v\n", location, success)
}

func main() {
	keyCodec := codex.String().Refine(validate.NonEmptyString)

	// ── Construction: the FIRST key, validated like any real runtime input ──
	keys, err := codex.NewMutable("jwks-signing-keys", "key-v1", keyCodec,
		codex.WithReloadObserver[string](rotationObserver{}))
	if err != nil {
		panic(err)
	}

	// ── A SecurityFunc-shaped closure: always reads the CURRENT key ──────
	verifyRequest := func(requestID string) {
		fmt.Printf("request %s: verifying against key %q\n", requestID, keys.Get())
	}

	fmt.Println("=== Before rotation ===")
	verifyRequest("req-1")
	verifyRequest("req-2")

	// ── Background rotation: e.g. a JWKS refresh ticker fires ────────────
	fmt.Println("\n=== Rotating to key-v2 ===")
	if err := keys.Set("key-v2"); err != nil {
		panic(err)
	}
	verifyRequest("req-3")

	// ── An invalid rotation attempt leaves the CURRENT key untouched ─────
	fmt.Println("\n=== Rejected rotation (empty key) ===")
	if err := keys.Set(""); err != nil {
		fmt.Println("  rotation rejected:", err)
	}
	verifyRequest("req-4") // still key-v2 — last-good-value-wins
}
