package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// CountingObserver implements [stats.Observer] and [stats.SecurityObserver]
// — merges what were TWO separate observer types across the retired
// adapters-chi/-security and adapters-nethttp/-security examples into
// ONE, shared by BOTH servers built in this example.
type CountingObserver struct {
	stats.NoopObserver // satisfies RecordSubscribe/RecordPublish (unused for HTTP)

	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int
	rejections     int
	latencies      []time.Duration
}

func (o *CountingObserver) RecordRequest(_ string, _ string, statusCode int, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total++
	if o.byStatus == nil {
		o.byStatus = make(map[int]int)
	}
	o.byStatus[statusCode]++
	o.latencies = append(o.latencies, d)
}

func (o *CountingObserver) RecordValidationError(location, _, _ string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.valErrorsByLoc == nil {
		o.valErrorsByLoc = make(map[string]int)
	}
	o.valErrorsByLoc[location]++
}

func (o *CountingObserver) RecordSecurityRejection(_, _ string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rejections++
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total requests      : %d\n", o.total)
	for code, n := range o.byStatus {
		fmt.Printf("  HTTP %-3d             : %d\n", code, n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs %-8s   : %d\n", "("+loc+")", n)
	}
	fmt.Printf("  security rejections : %d\n", o.rejections)
	if len(o.latencies) > 0 {
		var sum time.Duration
		for _, l := range o.latencies {
			sum += l
		}
		fmt.Printf("  avg latency          : %v\n", sum/time.Duration(len(o.latencies)))
	}
}

var _ stats.Observer = (*CountingObserver)(nil)
var _ stats.SecurityObserver = (*CountingObserver)(nil)
