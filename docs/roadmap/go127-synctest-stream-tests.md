# `testing/synctest` for `stream` package's time-based tests — `stream`

> **Status:** Idea / investigation — Explore mode. No code changes yet.
> [← Back to Roadmap](index.md)
>
> Spun out of a coverage review of
> [Go 1.27 Generic Methods](go127-generic-methods.md), which also
> surveyed this toolchain's actual `api/go1.27.txt` for other relevant
> stdlib changes beyond generic methods.

## The idea

`testing/synctest` is stabilized (no longer behind `GOEXPERIMENT`) in
this toolchain (confirmed directly against
`$(go env GOROOT)/api/go1.27.txt`). It lets a test run goroutines inside
a "bubble" with a fake, instantly-advanceable clock — `time.Sleep`,
timers, and tickers inside the bubble resolve immediately in wall-clock
terms, while still observing real happens-before ordering.

`stream`'s own API surface is heavily time-based: `Buffer` (count +
`maxWait` timeout), `Debounce`, `Throttle`, `Window` (fixed ticker),
`SlidingWindow` — every one of these takes a real `time.Duration` and,
today, presumably has tests that either sleep for real wall-clock time
(slow test suite) or use artificially tiny durations to keep tests fast
(flaky under CI load). `testing/synctest.Run`/`synctest.Sleep` could
make these tests both deterministic and fast.

## Why this is worth a look

- Faster, more reliable CI for exactly the part of `stream` most prone
  to timing flakiness.
- No source-code change to `stream` itself — this is a test-only
  investigation, not an API change.

## Open questions (not yet resolved)

1. **Current state** — do `stream/time_test.go` (or wherever these
   tests live) already use real sleeps, artificially short durations, or
   some other synchronization technique? Needs a direct look before
   assuming `synctest` is a strict improvement.
2. **Bubble compatibility** — `synctest.Run` requires everything
   time-sensitive under test to be reachable from within the bubble
   (goroutines spawned outside it see real time). Need to confirm
   `stream`'s internal goroutines (the ones actually calling
   `time.Sleep`/`time.NewTicker`) are started from inside the test's
   `synctest.Run` closure, not via some already-running background
   worker these tests can't control.
3. **Partial vs. full adoption** — should this apply to all of
   `Buffer`/`Debounce`/`Throttle`/`Window`/`SlidingWindow`'s tests, or
   only the ones currently slowest/flakiest?

## Next step

Read the existing tests for `stream/time.go`'s five functions, classify
each by its current timing strategy, then prototype one `synctest.Run`
conversion (most likely `Debounce` or `Throttle`, the simplest shape) as
a proof of concept before committing to a full sweep.
