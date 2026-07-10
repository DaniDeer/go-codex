package stream

// Stream[T] is a typed reactive stream with explicit error separation.
//
// Values carries successful items; Errors carries per-item errors. Both channels
// are closed when the stream terminates (source closed or context cancelled).
//
// Consumers MUST drain both channels concurrently — reading only from Values
// while ignoring Errors will cause the goroutine writing to Errors to block,
// leaking resources. Use [Drain] as the safe default sink: it handles both
// channels in a single select loop.
//
// The nil-channel pattern is used throughout the package to disable a channel
// once it is closed, preventing accidental reads from a closed channel in select
// statements. All operators follow this convention internally.
type Stream[T any] struct {
	// Values carries successfully processed items.
	Values <-chan T

	// Errors carries per-item errors. The stream continues after each error —
	// a failing item does not terminate the pipeline. Use [MapErr] to recover
	// from errors or reclassify them.
	Errors <-chan error
}
