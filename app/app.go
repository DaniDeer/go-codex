package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// Options configures [New].
type Options struct {
	// Observer is injected into [App.Context] via [stats.WithObserver], so
	// every port/adapter bound with that context resolves it automatically.
	// Nil means no injection (ports fall back to NoopObserver as usual).
	// App itself reports lifecycle events through it: a
	// RecordRequest("app.go", name, 200|500, duration) per supervised
	// goroutine exit and RecordRequest("app.shutdown", name, 200|500,
	// duration) per shutdown hook.
	Observer stats.Observer

	// Logger receives lifecycle events (goroutine exits, hook results).
	// Nil means slog.Default().
	Logger *slog.Logger

	// ShutdownTimeout bounds the context passed to shutdown hooks.
	// Zero means 10 seconds.
	ShutdownTimeout time.Duration
}

// hook is one registered shutdown action.
type hook struct {
	name string
	fn   func(ctx context.Context) error
}

// App is a minimal application lifecycle manager: one cancelable root context
// with the observer pre-injected, supervised goroutines (fail-fast), and LIFO
// shutdown hooks. Construct with [New]; zero value is not usable.
//
// Lifecycle:
//  1. [New] — create; [App.Context] is live immediately (no signal handlers
//     are installed until [App.Run]).
//  2. Bind ports/adapters with [App.Context]; register teardown with
//     [App.OnShutdown]; start long-lived work with [App.Go].
//  3. [App.Run] (services: blocks until SIGINT/SIGTERM, parent cancellation,
//     or the first goroutine failure) or [App.Shutdown] (demos/tests: direct
//     teardown). Both share one ordered teardown path.
type App struct {
	obs     stats.Observer
	logger  *slog.Logger
	timeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	wg    sync.WaitGroup // supervised goroutines
	gomu  sync.Mutex
	goErr []error // GoroutineError values, in completion order

	hookmu sync.Mutex
	hooks  []hook // registration order; run LIFO

	shutdownOnce sync.Once
	shutdownErr  error
	shutdownDone chan struct{}
}

// New returns an App with a live, cancelable root context. Constructing an
// App installs no signal handlers — that happens inside [App.Run] only.
func New(opts Options) *App {
	obs := opts.Observer
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := opts.ShutdownTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx := context.Background()
	if obs != nil {
		ctx = stats.WithObserver(ctx, obs)
	} else {
		obs = stats.NoopObserver{}
	}
	ctx, cancel := context.WithCancel(ctx)

	return &App{
		obs:          obs,
		logger:       logger,
		timeout:      timeout,
		ctx:          ctx,
		cancel:       cancel,
		shutdownDone: make(chan struct{}),
	}
}

// Context returns the app's root context: cancelable, with
// [Options.Observer] pre-injected. Use it for every Bind/Feed/Start call.
// It is cancelled when shutdown begins (signal, supervised-goroutine
// failure, parent cancellation, or a direct [App.Shutdown] call).
func (a *App) Context() context.Context { return a.ctx }

// Go runs fn in a supervised goroutine. A non-nil return CANCELS the app
// (fail-fast, errgroup-style) and is collected — wrapped in
// [GoroutineError] — into the error returned by [App.Run]/[App.Shutdown].
// A nil return just logs completion. fn should return when its ctx is done.
//
// Calling Go after shutdown has begun is a safe no-op (logged).
func (a *App) Go(name string, fn func(ctx context.Context) error) {
	select {
	case <-a.ctx.Done():
		a.logger.Warn("app: Go after shutdown ignored", "name", name)
		return
	default:
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		start := time.Now()
		err := fn(a.ctx)
		status := 200
		if err != nil {
			status = 500
			a.gomu.Lock()
			a.goErr = append(a.goErr, GoroutineError{Name: name, Err: err})
			a.gomu.Unlock()
			a.logger.Error("app: goroutine failed", "name", name, "error", err)
			a.cancel() // fail-fast: first failure begins shutdown
		} else {
			a.logger.Info("app: goroutine finished", "name", name)
		}
		a.obs.RecordRequest("app.go", name, status, time.Since(start))
	}()
}

// Supervise starts a non-blocking component (start is called once and
// returns immediately) and supervises its ACTUAL completion the same way
// [App.Go] supervises a blocking function: "finished" is reported only
// once the returned done channel closes, not when start itself returns.
//
// Use this for components whose start call is fire-and-forget but expose a
// completion signal — e.g. a [ports.PipePort]:
//
//	a.Supervise("sensor-pipeline", func(ctx context.Context) <-chan struct{} {
//	    p.Connect(ctx)
//	    return p.Done()
//	})
//
// Without Supervise, wiring such a component through Go directly
// (`a.Go(name, func(ctx) error { p.Connect(ctx); return nil })`) would
// report "finished" the instant Connect returns — essentially immediately,
// not when the component's internal goroutines have actually drained.
//
// Supervise delegates to Go for all status/duration/error bookkeeping —
// the same "app.go" observer event, LIFO-independent fail-fast semantics,
// and after-shutdown no-op behavior apply identically.
func (a *App) Supervise(name string, start func(ctx context.Context) (done <-chan struct{})) {
	select {
	case <-a.ctx.Done():
		a.logger.Warn("app: Supervise after shutdown ignored", "name", name)
		return
	default:
	}
	done := start(a.ctx)
	a.Go(name, func(context.Context) error {
		// Deliberately no ctx.Done() race here: waiting ONLY on done is the
		// entire point — start's component is expected to close done once
		// ITS OWN ctx-triggered teardown actually completes (e.g.
		// ports.PipePort.Done() closes only after Connect's internal
		// goroutines fully exit). Racing against ctx would let this
		// goroutine "finish" before the component has really drained,
		// recreating the exact premature-completion problem Supervise
		// exists to fix.
		<-done
		return nil
	})
}

// OnShutdown registers a hook run during shutdown in LIFO order (last
// registered, first run — matching defer semantics: close what you opened
// last, first). Each hook receives a context bounded by
// [Options.ShutdownTimeout]. Hook errors are collected — wrapped in
// [HookError] — and logged; a failing hook never stops later hooks.
//
// Calling OnShutdown after shutdown has begun is a safe no-op (logged) —
// the hook would never run.
func (a *App) OnShutdown(name string, fn func(ctx context.Context) error) {
	a.hookmu.Lock()
	defer a.hookmu.Unlock()
	select {
	case <-a.ctx.Done():
		a.logger.Warn("app: OnShutdown after shutdown ignored", "name", name)
		return
	default:
	}
	a.hooks = append(a.hooks, hook{name: name, fn: fn})
}

// Run blocks until SIGINT/SIGTERM, parent cancellation, or the first
// supervised-goroutine failure — then performs the ordered teardown: cancel
// [App.Context], wait for all [App.Go] goroutines, run the [App.OnShutdown]
// hooks (LIFO, each bounded by [Options.ShutdownTimeout]), and return
// errors.Join of all goroutine and hook errors (nil on a clean shutdown).
//
// Signal handlers are installed only for the duration of Run.
func (a *App) Run(parent context.Context) error {
	sigCtx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		a.logger.Info("app: shutdown requested", "reason", context.Cause(sigCtx))
	case <-a.ctx.Done():
		// A supervised goroutine failed (fail-fast) or Shutdown was called.
	}
	return a.Shutdown()
}

// Shutdown performs the ordered teardown directly, without signal-waiting:
// cancel [App.Context], wait for supervised goroutines, run the shutdown
// hooks LIFO, and return errors.Join of all collected errors. For demos,
// tests, and callers that own their own run loop.
//
// Shutdown is idempotent: the teardown executes once; concurrent and repeated
// calls block until it completes and return the same result.
func (a *App) Shutdown() error {
	a.shutdownOnce.Do(func() {
		defer close(a.shutdownDone)
		a.cancel()
		a.wg.Wait() // all supervised goroutines have exited

		a.hookmu.Lock()
		hooks := a.hooks
		a.hookmu.Unlock()

		var hookErrs []error
		for i := len(hooks) - 1; i >= 0; i-- { // LIFO
			h := hooks[i]
			start := time.Now()
			hctx, cancel := context.WithTimeout(context.Background(), a.timeout)
			err := runHook(hctx, h)
			cancel()
			status := 200
			if err != nil {
				status = 500
				hookErrs = append(hookErrs, HookError{Name: h.name, Err: err})
				a.logger.Error("app: shutdown hook failed", "name", h.name, "error", err)
			} else {
				a.logger.Info("app: shutdown hook finished", "name", h.name)
			}
			a.obs.RecordRequest("app.shutdown", h.name, status, time.Since(start))
		}

		a.gomu.Lock()
		all := append(append([]error{}, a.goErr...), hookErrs...)
		a.gomu.Unlock()
		a.shutdownErr = errors.Join(all...)
	})
	<-a.shutdownDone
	return a.shutdownErr
}

// runHook runs h.fn bounded by ctx: if fn outlives the deadline, runHook
// returns ctx's error (the goroutine is abandoned — hooks must respect ctx).
func runHook(ctx context.Context, h hook) error {
	done := make(chan error, 1)
	go func() { done <- h.fn(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
