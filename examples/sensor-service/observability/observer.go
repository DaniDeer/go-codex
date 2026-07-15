// Package observability holds the cross-cutting observer for the sensor
// service. One CountingObserver instance is fanned out (stats.NewFanout) with
// a LoggingObserver in main() and stored once in the context — every adapter
// (HTTP, MQTT, SQL, file, stream) resolves it automatically when its
// Options.Observer is nil.
package observability

import (
	"fmt"
	"sync"
	"time"
)

// CountingObserver is an in-memory [stats.Observer] + [stats.SQLObserver] +
// [stats.StreamObserver]. In production replace the maps and counters with
// Prometheus CounterVecs or OpenTelemetry instruments — the interface methods
// are identical.
type CountingObserver struct {
	mu             sync.Mutex
	requests       map[int]int    // HTTP status code → request count
	subscribes     int            // MQTT RecordSubscribe calls (alert publishes via adaptermqtt.Publish)
	publishes      int            // successful MQTT publish calls
	streamItems    int            // stream.Apply RecordStreamItem calls (one per MQTT sensor reading)
	valErrors      map[string]int // location → validation error count
	sqlValidations int
	migrations     int
}

// NewCountingObserver returns a ready-to-use CountingObserver.
func NewCountingObserver() *CountingObserver {
	return &CountingObserver{
		requests:  make(map[int]int),
		valErrors: make(map[string]int),
	}
}

// RecordRequest implements [stats.Observer].
func (o *CountingObserver) RecordRequest(_ string, _ string, status int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests[status]++
}

// RecordSubscribe implements [stats.Observer].
func (o *CountingObserver) RecordSubscribe(_ string, success bool, _ time.Duration) {
	if success {
		o.mu.Lock()
		o.subscribes++
		o.mu.Unlock()
	}
}

// RecordPublish implements [stats.Observer].
func (o *CountingObserver) RecordPublish(_ string, success bool, _ time.Duration) {
	if success {
		o.mu.Lock()
		o.publishes++
		o.mu.Unlock()
	}
}

// RecordValidationError implements [stats.Observer].
func (o *CountingObserver) RecordValidationError(location, _, _ string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.valErrors[location]++
}

// RecordValidation implements [stats.SQLObserver].
func (o *CountingObserver) RecordValidation(_, _ string, _ time.Duration, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sqlValidations++
}

// RecordMigration implements [stats.SQLObserver].
func (o *CountingObserver) RecordMigration(_, _ string, _ int64, _ time.Duration, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.migrations++
}

// RecordStreamItem implements [stats.StreamObserver].
func (o *CountingObserver) RecordStreamItem(_ string, success bool, _ time.Duration) {
	if success {
		o.mu.Lock()
		o.streamItems++
		o.mu.Unlock()
	}
}

// Print writes the counters summary to stdout.
func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Println("\n── Observer summary ─────────────────────────────────────")
	fmt.Printf("  HTTP requests by status : %v\n", o.requests)
	fmt.Printf("  MQTT publishes  (ok)    : %d\n", o.publishes)
	fmt.Printf("  Stream items processed  : %d\n", o.streamItems)
	fmt.Printf("  SQL validations called  : %d\n", o.sqlValidations)
	fmt.Printf("  Migrations applied      : %d\n", o.migrations)
	if len(o.valErrors) > 0 {
		fmt.Printf("  Validation errors       : %v\n", o.valErrors)
	} else {
		fmt.Println("  Validation errors       : none")
	}
	fmt.Println("─────────────────────────────────────────────────────────")
}
