package core

import (
	"context"
	"sync"

	"github.com/lignting/lignting-agents/internal/domain"
)

var bridgeStates sync.Map // *fakeStore -> map[string]*domain.Bridge

func bridgeKey(fromType, fromID, toType, toID string) string {
	return fromType + "|" + fromID + "|" + toType + "|" + toID
}

func (s *fakeStore) bridgeMap() map[string]*domain.Bridge {
	if v, ok := bridgeStates.Load(s); ok {
		return v.(map[string]*domain.Bridge)
	}
	m := map[string]*domain.Bridge{}
	actual, _ := bridgeStates.LoadOrStore(s, m)
	return actual.(map[string]*domain.Bridge)
}

func setFakeBridge(store *fakeStore, b domain.Bridge) {
	store.mu.Lock()
	store.bridgeMap()[bridgeKey(b.FromType, b.FromID, b.ToType, b.ToID)] = &b
	store.mu.Unlock()
}

func (s *fakeStore) GetActiveBridge(_ context.Context, fromType, fromID, toType, toID string) (*domain.Bridge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bridgeMap()[bridgeKey(fromType, fromID, toType, toID)], nil
}
