package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// noopDepositor discards staged headers/cookies — sufficient for tests that
// only care about the returned domain value.
type noopDepositor struct{}

func (noopDepositor) SetHeader(context.Context, http.Header)         {}
func (noopDepositor) SetCookie(context.Context, string, string, int) {}

// TestBuildUserRecord tests the pure business logic for user creation —
// no HTTP server, no store, no codec decoding required.
func TestBuildUserRecord(t *testing.T) {
	req := routes.CreateUserReq{Name: "Alice", Email: "alice@example.com"}
	record := BuildUserRecord(req)

	if record.Name != req.Name {
		t.Errorf("name: got %q, want %q", record.Name, req.Name)
	}
	if record.Email != req.Email {
		t.Errorf("email: got %q, want %q", record.Email, req.Email)
	}
	if record.ID == "" {
		t.Error("expected non-empty ID")
	}
}

// TestBuildUserResponse tests the pure projection from DB record to HTTP response.
func TestBuildUserResponse(t *testing.T) {
	record := routes.UserRecord{ID: "abc-123", Name: "Bob", Email: "bob@example.com"}
	user := BuildUserResponse(record)

	if user.ID != record.ID {
		t.Errorf("id: got %q, want %q", user.ID, record.ID)
	}
	if user.Name != record.Name {
		t.Errorf("name: got %q, want %q", user.Name, record.Name)
	}
	if user.Email != record.Email {
		t.Errorf("email: got %q, want %q", user.Email, record.Email)
	}
}

// TestUserStoreRoundtrip tests the infrastructure layer: save via codec encode,
// retrieve via codec decode.
func TestUserStoreRoundtrip(t *testing.T) {
	store := NewUserStore()
	want := routes.UserRecord{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Bob", Email: "bob@example.com"}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := store.Get(want.ID)
	if !ok {
		t.Fatalf("user not found after save")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestUserStoreNotFound verifies Get returns false for missing IDs.
func TestUserStoreNotFound(t *testing.T) {
	store := NewUserStore()
	_, ok := store.Get("no-such-id")
	if ok {
		t.Error("expected not found, got found")
	}
}

// TestCreateUserPipeline exercises the full infrastructure handler:
// the pure functions (L2) + store IO (L3), without an HTTP server.
func TestCreateUserPipeline(t *testing.T) {
	store := NewUserStore()
	handler := MakeCreateUserHandler(store, noopDepositor{})

	req := routes.CreateUserReq{Name: "Alice", Email: "alice@example.com"}
	user, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Name != req.Name {
		t.Errorf("name: got %q, want %q", user.Name, req.Name)
	}
	if user.Email != req.Email {
		t.Errorf("email: got %q, want %q", user.Email, req.Email)
	}

	// Verify the record was persisted via the store's codec-driven encoding.
	record, ok := store.Get(user.ID)
	if !ok {
		t.Fatalf("record not found in store after create")
	}
	if record.Email != user.Email {
		t.Errorf("stored email: got %q, want %q", record.Email, user.Email)
	}
}
