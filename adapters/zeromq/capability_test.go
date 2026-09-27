package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

// mockCapabilityObserver spies on RecordCapabilityApplied calls.
type mockCapabilityObserver struct {
	stats.NoopObserver
	applied []string
}

func (o *mockCapabilityObserver) RecordCapabilityApplied(location, capability string) {
	o.applied = append(o.applied, location+":"+capability)
}

// capableSocket embeds mockSocket and additionally implements
// [HWMSetter]/[ConflateSetter].
type capableSocket struct {
	*mockSocket
	hwm      int
	conflate bool
}

func (s *capableSocket) SetHWM(n int) error {
	s.hwm = n
	return nil
}

func (s *capableSocket) SetConflate(on bool) error {
	s.conflate = on
	return nil
}

func TestHWM_ImplementsCapability(t *testing.T) {
	var _ Capability = HWM(10)
	if got := HWM(10).CapabilityName(); got != "HWM" {
		t.Errorf("want CapabilityName %q, got %q", "HWM", got)
	}
}

func TestConflate_ImplementsCapability(t *testing.T) {
	var _ Capability = Conflate(true)
	if got := Conflate(true).CapabilityName(); got != "Conflate" {
		t.Errorf("want CapabilityName %q, got %q", "Conflate", got)
	}
}

func TestResolveCapabilities(t *testing.T) {
	hwm, hwmSet, conflate, conflateSet := resolveCapabilities([]Capability{HWM(10), Conflate(true)})
	if !hwmSet || hwm != 10 {
		t.Errorf("want HWM(10) set, got %v set=%v", hwm, hwmSet)
	}
	if !conflateSet || !bool(conflate) {
		t.Errorf("want Conflate(true) set, got %v set=%v", conflate, conflateSet)
	}
}

// TestServeSubscribers_Capabilities_AppliedToSocket confirms
// SubscribeOptions.Capabilities is applied to the socket via
// HWMSetter/ConflateSetter (when implemented) and reported via
// stats.CapabilityObserver.
func TestServeSubscribers_Capabilities_AppliedToSocket(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{}}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	obs := &mockCapabilityObserver{}
	sub := sensorChannel("sensors/hwm").WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions(SubscribeOptions[sensorReading]{
			Capabilities: []Capability{HWM(42), Conflate(true)},
			Observer:     obs,
		})
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}
	caller := newCaller(sock, ev)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("ServeSubscribers: %v", err)
	}

	if sock.hwm != 42 {
		t.Errorf("want HWM(42) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
	wantApplied := map[string]bool{"sensors/hwm:HWM": false, "sensors/hwm:Conflate": false}
	for _, a := range obs.applied {
		if _, ok := wantApplied[a]; ok {
			wantApplied[a] = true
		}
	}
	for k, found := range wantApplied {
		if !found {
			t.Errorf("want RecordCapabilityApplied for %q, got %v", k, obs.applied)
		}
	}
}

// TestApplyCapabilities_NoopWhenSocketDoesNotImplementSetters confirms
// applyCapabilities is a documented no-op (no panic, no error) when sock
// doesn't implement HWMSetter/ConflateSetter.
func TestApplyCapabilities_NoopWhenSocketDoesNotImplementSetters(t *testing.T) {
	sock := &mockSocket{}
	obs := &mockCapabilityObserver{}
	applyCapabilities(sock, []Capability{HWM(10), Conflate(true)}, obs, "sensors/x")
	if len(obs.applied) != 0 {
		t.Errorf("want no RecordCapabilityApplied calls when socket doesn't implement setters, got %v", obs.applied)
	}
}
