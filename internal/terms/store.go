package terms

import (
	"sort"
	"sync"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

type Store struct {
	mu        sync.RWMutex
	snapshots map[exchange.Key]Snapshot
}

func NewStore(now time.Time) *Store {
	store := &Store{snapshots: make(map[exchange.Key]Snapshot)}
	for _, binding := range exchange.DefaultBindings() {
		store.Apply(FallbackSnapshot(binding.Exchange, binding.Market, now, "public fallback terms"))
	}
	return store
}

func (s *Store) Apply(snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[snapshot.Key()] = snapshot.Clone()
}

func (s *Store) Snapshot(key exchange.Key) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, ok := s.snapshots[key]
	if !ok {
		return Snapshot{}, false
	}
	return snapshot.Clone(), true
}

func (s *Store) All() []Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]Snapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		snapshots = append(snapshots, snapshot.Clone())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Exchange == snapshots[j].Exchange {
			return snapshots[i].Market.ID() < snapshots[j].Market.ID()
		}
		return snapshots[i].Exchange < snapshots[j].Exchange
	})
	return snapshots
}

func (s *Store) Health(now time.Time) []Health {
	snapshots := s.All()
	health := make([]Health, 0, len(snapshots))
	for _, snapshot := range snapshots {
		health = append(health, Health{
			Exchange:  snapshot.Exchange,
			Market:    snapshot.Market,
			Source:    snapshot.Source,
			Fresh:     snapshot.IsFresh(now),
			UpdatedAt: snapshot.UpdatedAt,
			ExpiresAt: snapshot.ExpiresAt,
			Message:   snapshot.Message,
		})
	}
	return health
}
