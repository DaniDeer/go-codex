// Package observer provides a reusable [stats.FileObserver] implementation
// for examples/file-api — mirrors examples/rest-api/observer's own
// CountingObserver (naming-consistent across every go-codex example
// project).
package observer

import (
	"fmt"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// CountingObserver implements [stats.FileObserver] — counts every
// dispatch-core event by verb, plus successes/failures, so main.go can
// print a final summary after every demo has run.
type CountingObserver struct {
	stats.NoopObserver // satisfies every OTHER Observer interface (unused here)

	mu      sync.Mutex
	byVerb  map[string]int
	success map[string]int
	failure map[string]int
	native  int
	approx  int
}

func (o *CountingObserver) record(verb string, success bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byVerb == nil {
		o.byVerb = make(map[string]int)
		o.success = make(map[string]int)
		o.failure = make(map[string]int)
	}
	o.byVerb[verb]++
	if success {
		o.success[verb]++
	} else {
		o.failure[verb]++
	}
}

func (o *CountingObserver) RecordFileRead(_ string, success bool, _ time.Duration) {
	o.record("read", success)
}
func (o *CountingObserver) RecordFileWrite(_ string, success bool, _ time.Duration) {
	o.record("write", success)
}
func (o *CountingObserver) RecordFileDelete(_ string, success bool, _ time.Duration) {
	o.record("delete", success)
}
func (o *CountingObserver) RecordFileList(_ string, success bool, _ int, _ time.Duration) {
	o.record("list", success)
}
func (o *CountingObserver) RecordFileExists(_ string, success bool, _ time.Duration) {
	o.record("exists", success)
}
func (o *CountingObserver) RecordFileCopy(_, _ string, success bool, _ time.Duration) {
	o.record("copy", success)
}
func (o *CountingObserver) RecordFileMove(_, _ string, success bool, _ time.Duration) {
	o.record("move", success)
}
func (o *CountingObserver) RecordFileMkdir(_ string, success bool, _ time.Duration) {
	o.record("mkdir", success)
}
func (o *CountingObserver) RecordFileBatchDelete(_ []string, successCount, failureCount int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byVerb == nil {
		o.byVerb = make(map[string]int)
		o.success = make(map[string]int)
		o.failure = make(map[string]int)
	}
	o.byVerb["batchdelete"] += successCount + failureCount
	o.success["batchdelete"] += successCount
	o.failure["batchdelete"] += failureCount
}
func (o *CountingObserver) RecordFileRangeRead(_ string, native bool, _, _ int64, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if native {
		o.native++
	} else {
		o.approx++
	}
}

// Print prints a per-verb summary — called once, at the very end of
// main.go, after every demo has run.
func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, verb := range []string{"read", "write", "delete", "list", "exists", "copy", "move", "mkdir", "batchdelete"} {
		if o.byVerb[verb] == 0 {
			continue
		}
		fmt.Printf("  %-12s: %d calls (%d ok, %d failed)\n", verb, o.byVerb[verb], o.success[verb], o.failure[verb])
	}
	fmt.Printf("  %-12s: %d native, %d approximated\n", "range reads", o.native, o.approx)
}

var _ stats.FileObserver = (*CountingObserver)(nil)
