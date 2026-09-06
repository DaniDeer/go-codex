// Package handlers implements the SERVER-side business logic and security
// enforcement for every route declared in routes/ — the "implement" phase.
// chiserver/ and nethttpserver/ both import this package unchanged and
// attach its functions via .WithHandler/.HandleMW, proving the business
// logic itself is adapter-agnostic.
package handlers

import (
	"fmt"
	"sync"

	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// UserStore is a mock database that operates via routes.UserRecordCodec —
// analogous to a real SQL table, using the codec to encode/decode rows.
type UserStore struct {
	mu   sync.RWMutex
	rows map[string]map[string]any
}

// NewUserStore returns an empty, ready-to-use mock store.
func NewUserStore() *UserStore {
	return &UserStore{rows: make(map[string]map[string]any)}
}

// Save encodes the UserRecord using routes.UserRecordCodec (analogous to
// SQL INSERT/UPDATE).
func (s *UserStore) Save(r routes.UserRecord) error {
	encoded, err := routes.UserRecordCodec.Encode(r)
	if err != nil {
		return err
	}
	row, ok := encoded.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected encoded type %T", encoded)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[r.ID] = row
	return nil
}

// Get decodes a UserRecord from a stored row using routes.UserRecordCodec.
func (s *UserStore) Get(id string) (routes.UserRecord, bool) {
	s.mu.RLock()
	row, ok := s.rows[id]
	s.mu.RUnlock()
	if !ok {
		return routes.UserRecord{}, false
	}
	record, err := routes.UserRecordCodec.Decode(row)
	if err != nil {
		return routes.UserRecord{}, false
	}
	return record, true
}
