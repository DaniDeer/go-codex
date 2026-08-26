package codex

import "time"

// ReloadObserver is implemented by any value that wants to observe
// [Mutable]'s Set/reload events. Deliberately defined HERE, not in
// stats — codex has zero dependency on stats (stats depends on codex,
// not the reverse; giving a codex type a stats.Observer-typed field
// would form a real Go import cycle). Any stats.Observer CONCRETE
// implementation that also defines this one method (stats.NoopObserver/
// LoggingObserver/the internal fanout type all do) satisfies this
// interface STRUCTURALLY — Go interfaces need no explicit "implements"
// declaration — so existing stats-based observers work here with zero
// code changes and zero import in either direction. stats also exposes
// this same interface as stats.ReloadObserver (a type alias) for
// discoverability, plus stats.AsReloadObserver to bridge a
// stats.Observer value into this interface.
type ReloadObserver interface {
	// RecordReload is called on every [Mutable.Set] call, success or
	// failure. location identifies the container instance (the
	// caller-chosen string passed to [NewMutable], e.g.
	// "jwks-signing-keys"). success is false when the new value failed
	// codec validation (the PREVIOUS value remains in effect). duration
	// is the validation call's own cost.
	RecordReload(location string, success bool, duration time.Duration)
}
