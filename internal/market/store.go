package market

import (
	"sort"
	"sync"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

type Store struct {
	mu          sync.RWMutex
	staleAfter  time.Duration
	snapshots   map[exchange.Key]exchange.OrderBookSnapshot
	subscribers map[int]chan struct{}
	nextSubID   int
	observer    Observer
}

type Observer interface {
	ObserveMarket(snapshot exchange.OrderBookSnapshot, now time.Time)
}

func NewStore(staleAfter time.Duration) *Store {
	return &Store{
		staleAfter:  staleAfter,
		snapshots:   make(map[exchange.Key]exchange.OrderBookSnapshot),
		subscribers: make(map[int]chan struct{}),
	}
}

func (s *Store) SetObserver(observer Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
}

func (s *Store) Register(binding exchange.Binding) {
	s.SetStatus(binding, exchange.StatusStarting, exchange.TransportNone, "waiting for first market data")
}

func (s *Store) Apply(snapshot exchange.OrderBookSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot = snapshot.Clone()
	if snapshot.ReceivedAt.IsZero() {
		snapshot.ReceivedAt = time.Now()
	}
	if snapshot.Transport == "" {
		snapshot.Transport = exchange.TransportWebSocket
	}
	if snapshot.Status == "" {
		snapshot.Status = exchange.StatusLive
	}
	if snapshot.Message == "" {
		snapshot.Message = "feed live"
	}

	s.snapshots[snapshot.Key()] = snapshot
	if s.observer != nil {
		s.observer.ObserveMarket(snapshot.Clone(), time.Now())
	}
	s.notifyLocked()
}

func (s *Store) SetStatus(binding exchange.Binding, status exchange.FeedStatus, transport exchange.Transport, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := binding.Key()
	snapshot := s.snapshots[key]
	snapshot.Exchange = binding.Exchange
	snapshot.Market = binding.Market
	snapshot.Status = status
	snapshot.Transport = transport
	snapshot.Message = message

	s.snapshots[key] = snapshot
	s.notifyLocked()
}

func (s *Store) SetErrorIfNotFresh(binding exchange.Binding, transport exchange.Transport, message string, now time.Time) {
	s.SetStatusIfNotFresh(binding, exchange.StatusError, transport, message, now)
}

func (s *Store) SetStatusIfNotFresh(binding exchange.Binding, status exchange.FeedStatus, transport exchange.Transport, message string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := binding.Key()
	snapshot := s.snapshots[key]
	if snapshot.Status == exchange.StatusLive && !snapshot.ReceivedAt.IsZero() && now.Sub(snapshot.ReceivedAt) <= s.staleAfter {
		return
	}

	snapshot.Exchange = binding.Exchange
	snapshot.Market = binding.Market
	snapshot.Status = status
	snapshot.Transport = transport
	snapshot.Message = message
	s.snapshots[key] = snapshot
	s.notifyLocked()
}

func (s *Store) MarkStale(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for key, snapshot := range s.snapshots {
		if snapshot.Status != exchange.StatusLive || snapshot.ReceivedAt.IsZero() {
			continue
		}
		if now.Sub(snapshot.ReceivedAt) <= s.staleAfter {
			continue
		}

		snapshot.Status = exchange.StatusStale
		snapshot.Message = "market data is stale"
		s.snapshots[key] = snapshot
		changed = true
	}

	if changed {
		s.notifyLocked()
	}
}

func (s *Store) IsFreshWebSocket(key exchange.Key, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, ok := s.snapshots[key]
	if !ok {
		return false
	}

	return snapshot.Status == exchange.StatusLive &&
		snapshot.Transport == exchange.TransportWebSocket &&
		!snapshot.ReceivedAt.IsZero() &&
		now.Sub(snapshot.ReceivedAt) <= s.staleAfter
}

func (s *Store) Snapshot() []exchange.OrderBookSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]exchange.OrderBookSnapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		snapshots = append(snapshots, snapshot.Clone())
	}

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Market.ID() == snapshots[j].Market.ID() {
			return snapshots[i].Exchange < snapshots[j].Exchange
		}
		return snapshots[i].Market.ID() < snapshots[j].Market.ID()
	})

	return snapshots
}

func (s *Store) Subscribe(done <-chan struct{}) <-chan struct{} {
	events := make(chan struct{}, 1)

	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	s.subscribers[id] = events
	s.mu.Unlock()

	go func() {
		<-done
		s.mu.Lock()
		delete(s.subscribers, id)
		close(events)
		s.mu.Unlock()
	}()

	return events
}

func (s *Store) notifyLocked() {
	for _, subscriber := range s.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}
