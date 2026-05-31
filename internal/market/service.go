package market

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

type ServiceConfig struct {
	BookDepth       int
	PollInterval    time.Duration
	StaleAfter      time.Duration
	WebSocketMaxAge time.Duration
}

func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		BookDepth:       10,
		PollInterval:    3 * time.Second,
		StaleAfter:      3 * time.Second,
		WebSocketMaxAge: 23 * time.Hour,
	}
}

type Service struct {
	store     *Store
	providers map[exchange.ID]exchange.MarketDataProvider
	bindings  []exchange.Binding
	cfg       ServiceConfig
}

func NewService(store *Store, providers map[exchange.ID]exchange.MarketDataProvider, bindings []exchange.Binding, cfg ServiceConfig) *Service {
	return &Service{
		store:     store,
		providers: providers,
		bindings:  enabledBindings(bindings),
		cfg:       cfg.withDefaults(),
	}
}

func (s *Service) Start(ctx context.Context) {
	for _, binding := range s.bindings {
		s.store.Register(binding)

		provider := s.providers[binding.Exchange]
		if provider == nil {
			s.store.SetStatus(binding, exchange.StatusError, exchange.TransportNone, "provider not registered")
			continue
		}

		go s.runWebSocket(ctx, provider, binding)
		go s.runPoller(ctx, provider, binding)
	}

	go s.runStaleMarker(ctx)
}

func (s *Service) runWebSocket(ctx context.Context, provider exchange.MarketDataProvider, binding exchange.Binding) {
	retry := backoff.NewExponentialBackOff()
	retry.InitialInterval = 500 * time.Millisecond
	retry.MaxInterval = 15 * time.Second
	retry.RandomizationFactor = 0.25
	retry.Reset()

	for ctx.Err() == nil {
		s.store.SetStatusIfNotFresh(binding, exchange.StatusConnecting, exchange.TransportWebSocket, "connecting websocket", time.Now())

		streamCtx, cancel := context.WithTimeout(ctx, s.cfg.WebSocketMaxAge)
		err := s.streamOnce(streamCtx, provider, binding)
		cancel()

		if ctx.Err() != nil {
			return
		}

		if errors.Is(err, context.DeadlineExceeded) {
			s.store.SetStatusIfNotFresh(binding, exchange.StatusConnecting, exchange.TransportWebSocket, "recycling websocket connection", time.Now())
			retry.Reset()
			continue
		}
		if err != nil {
			s.store.SetErrorIfNotFresh(binding, exchange.TransportWebSocket, fmt.Sprintf("websocket error: %v", err), time.Now())
		}

		delay := retry.NextBackOff()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) streamOnce(ctx context.Context, provider exchange.MarketDataProvider, binding exchange.Binding) error {
	out := make(chan exchange.OrderBookSnapshot, 32)
	errs := make(chan error, 1)

	go func() {
		errs <- provider.Stream(ctx, binding, s.cfg.BookDepth, out)
		close(out)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snapshot, ok := <-out:
			if !ok {
				out = nil
				continue
			}
			s.store.Apply(snapshot)
		case err := <-errs:
			if err == nil {
				err = errors.New("websocket stream ended")
			}
			return err
		}
	}
}

func (s *Service) runPoller(ctx context.Context, provider exchange.MarketDataProvider, binding exchange.Binding) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			now := time.Now()
			if !s.store.IsFreshWebSocket(binding.Key(), now) {
				pollCtx, cancel := context.WithTimeout(ctx, min(5*time.Second, s.cfg.PollInterval))
				snapshot, err := provider.Poll(pollCtx, binding, s.cfg.BookDepth)
				cancel()
				if err != nil {
					s.store.SetErrorIfNotFresh(binding, exchange.TransportPolling, fmt.Sprintf("polling error: %v", err), time.Now())
				} else {
					s.store.Apply(snapshot)
				}
			}
			timer.Reset(s.cfg.PollInterval)
		}
	}
}

func (s *Service) runStaleMarker(ctx context.Context) {
	interval := max(s.cfg.StaleAfter/2, 250*time.Millisecond)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.store.MarkStale(now)
		}
	}
}

func (cfg ServiceConfig) withDefaults() ServiceConfig {
	defaults := DefaultServiceConfig()
	if cfg.BookDepth <= 0 {
		cfg.BookDepth = defaults.BookDepth
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = defaults.StaleAfter
	}
	if cfg.WebSocketMaxAge <= 0 {
		cfg.WebSocketMaxAge = defaults.WebSocketMaxAge
	}

	return cfg
}

func enabledBindings(bindings []exchange.Binding) []exchange.Binding {
	enabled := make([]exchange.Binding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Enabled {
			enabled = append(enabled, binding)
		}
	}
	return enabled
}
