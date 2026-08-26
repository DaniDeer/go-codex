package codex_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── GetterSetter interface satisfaction ─────────────────────────────────────

var _ codex.GetterSetter[string] = (*codex.Mutable[string])(nil)

func TestMutable_ImplementsGetterSetterInterface(t *testing.T) {
	var _ codex.GetterSetter[string] = (*codex.Mutable[string])(nil)
}

// ── Construction ─────────────────────────────────────────────────────────────

func TestNewMutable_ValidInitial_ReturnsMutable(t *testing.T) {
	m, err := codex.NewMutable("test", "hello", codex.String().Refine(validate.NonEmptyString))
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if got := m.Get(); got != "hello" {
		t.Errorf("Get() = %q, want %q", got, "hello")
	}
}

func TestNewMutable_InvalidInitial_ReturnsError(t *testing.T) {
	_, err := codex.NewMutable("test", "", codex.String().Refine(validate.NonEmptyString))
	if err == nil {
		t.Fatal("NewMutable with invalid initial: want error, got nil")
	}
}

func TestMutable_Get_NeverPanics(t *testing.T) {
	m, err := codex.NewMutable("test", "initial", codex.String())
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if got := m.Get(); got != "initial" {
		t.Errorf("Get() = %q, want %q", got, "initial")
	}
}

// ── Set ───────────────────────────────────────────────────────────────────

func TestMutable_SetValid_ReplacesValue(t *testing.T) {
	m, err := codex.NewMutable("test", "first", codex.String().Refine(validate.NonEmptyString))
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if err := m.Set("second"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	if got := m.Get(); got != "second" {
		t.Errorf("Get() = %q, want %q", got, "second")
	}
}

func TestMutable_SetInvalid_KeepsPreviousValue(t *testing.T) {
	m, err := codex.NewMutable("test", "first", codex.String().Refine(validate.NonEmptyString))
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if err := m.Set(""); err == nil {
		t.Fatal("Set(\"\"): want error, got nil")
	}
	if got := m.Get(); got != "first" {
		t.Errorf("Get() after failed Set = %q, want unchanged %q", got, "first")
	}
}

func TestMutable_SetRepeatedly_AllSucceed(t *testing.T) {
	m, err := codex.NewMutable("test", "v1", codex.String())
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	for _, v := range []string{"v2", "v3", "v4"} {
		if err := m.Set(v); err != nil {
			t.Fatalf("Set(%q): unexpected error: %v", v, err)
		}
	}
	if got := m.Get(); got != "v4" {
		t.Errorf("Get() = %q, want %q", got, "v4")
	}
}

func TestMutable_ConcurrentGetSet_NoRace(t *testing.T) {
	m, err := codex.NewMutable("test", 0, codex.Int())
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = m.Set(n)
		}(i)
		go func() {
			defer wg.Done()
			_ = m.Get()
		}()
	}
	wg.Wait()
}

// ── Observer integration ─────────────────────────────────────────────────────

// recordingReloadObserver captures RecordReload calls for assertions —
// deliberately stats-free (proves codex.ReloadObserver's structural
// satisfaction design works standalone).
type recordingReloadObserver struct {
	mu    sync.Mutex
	calls []reloadCall
}

type reloadCall struct {
	location string
	success  bool
}

func (r *recordingReloadObserver) RecordReload(location string, success bool, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, reloadCall{location, success})
}

var _ codex.ReloadObserver = (*recordingReloadObserver)(nil)

func TestMutable_Set_CallsReloadObserverOnSuccess(t *testing.T) {
	obs := &recordingReloadObserver{}
	m, err := codex.NewMutable("jwks", "v1", codex.String(), codex.WithReloadObserver[string](obs))
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if err := m.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	if len(obs.calls) != 1 || obs.calls[0].location != "jwks" || !obs.calls[0].success {
		t.Errorf("unexpected calls: %+v", obs.calls)
	}
}

func TestMutable_Set_CallsReloadObserverOnFailure(t *testing.T) {
	obs := &recordingReloadObserver{}
	m, err := codex.NewMutable("jwks", "v1", codex.String().Refine(validate.NonEmptyString), codex.WithReloadObserver[string](obs))
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if err := m.Set(""); err == nil {
		t.Fatal("Set(\"\"): want error, got nil")
	}
	if len(obs.calls) != 1 || obs.calls[0].location != "jwks" || obs.calls[0].success {
		t.Errorf("unexpected calls: %+v", obs.calls)
	}
}

func TestMutable_Set_NilObserver_NoPanic(t *testing.T) {
	m, err := codex.NewMutable("test", "v1", codex.String())
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if err := m.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
}

// plainObserver does NOT implement codex.ReloadObserver.
type plainObserver struct{}

func TestMutable_Set_PlainObserver_NoPanic(t *testing.T) {
	m, err := codex.NewMutable("test", "v1", codex.String(), codex.WithReloadObserver[string](plainObserver{}))
	if err != nil {
		t.Fatalf("NewMutable: unexpected error: %v", err)
	}
	if err := m.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
}

// ── Example ──────────────────────────────────────────────────────────────

func ExampleMutable() {
	keys, err := codex.NewMutable("jwks-signing-keys", "key-v1", codex.String().Refine(validate.NonEmptyString))
	if err != nil {
		panic(err)
	}
	fmt.Println("current:", keys.Get())

	// Background rotation, e.g. triggered by a JWKS refresh ticker.
	if err := keys.Set("key-v2"); err != nil {
		panic(err)
	}
	fmt.Println("current:", keys.Get())
	// Output:
	// current: key-v1
	// current: key-v2
}
