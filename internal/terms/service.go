package terms

import (
	"context"
	"fmt"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

type ServiceConfig struct {
	RefreshInterval time.Duration
	RequestTimeout  time.Duration
}

func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		RefreshInterval: 15 * time.Minute,
		RequestTimeout:  5 * time.Second,
	}
}

type Service struct {
	store    *Store
	clients  map[exchange.ID]exchange.TermsClient
	bindings []exchange.Binding
	cfg      ServiceConfig
	now      func() time.Time
}

func NewService(store *Store, clients map[exchange.ID]exchange.TermsClient, bindings []exchange.Binding, cfg ServiceConfig) *Service {
	return &Service{
		store:    store,
		clients:  clients,
		bindings: enabledBindings(bindings),
		cfg:      cfg.withDefaults(),
		now:      time.Now,
	}
}

func (s *Service) Start(ctx context.Context) {
	for _, binding := range s.bindings {
		client := s.clients[binding.Exchange]
		if client == nil {
			s.store.Apply(FallbackSnapshot(binding.Exchange, binding.Market, s.now(), "terms client not registered"))
			continue
		}
		if client.TermsUnavailableMessage() == "" {
			pending := FallbackSnapshot(binding.Exchange, binding.Market, s.now(), "authenticated terms refresh pending")
			pending.ExpiresAt = pending.UpdatedAt
			s.store.Apply(pending)
		}
		go s.refreshLoop(ctx, client, binding)
	}
}

func (s *Service) refreshLoop(ctx context.Context, client exchange.TermsClient, binding exchange.Binding) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.refreshOnce(ctx, client, binding)
			timer.Reset(s.cfg.RefreshInterval)
		}
	}
}

func (s *Service) refreshOnce(ctx context.Context, client exchange.TermsClient, binding exchange.Binding) {
	now := s.now()
	if message := client.TermsUnavailableMessage(); message != "" {
		s.store.Apply(FallbackSnapshot(binding.Exchange, binding.Market, now, message))
		return
	}

	requestCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()

	fees, err := client.FetchFeeSchedule(requestCtx, binding, now)
	if err != nil {
		s.applyFetchFailure(binding, now, fmt.Errorf("fee schedule: %w", err))
		return
	}
	account, err := client.FetchAccount(requestCtx, now)
	if err != nil {
		s.applyFetchFailure(binding, now, fmt.Errorf("account: %w", err))
		return
	}
	source := SourceAuthenticated
	constraints, err := client.FetchMarketConstraints(requestCtx, binding)
	if err != nil {
		constraints = FallbackConstraints()
		source = SourceMixed
	}
	transferFees, _ := client.FetchTransferFees(requestCtx, []string{binding.Market.Base, binding.Market.Quote}, now)

	s.store.Apply(Snapshot{
		Exchange:     binding.Exchange,
		Market:       binding.Market,
		Source:       source,
		Fees:         fees,
		Constraints:  constraints,
		Balances:     account.Balances,
		TransferFees: transferFees,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(DefaultTTL),
		Message:      "authenticated trading terms",
	})
}

func (s *Service) applyFetchFailure(binding exchange.Binding, now time.Time, err error) {
	message := fmt.Sprintf("terms fetch failed: %v", err)
	s.store.Apply(FallbackSnapshot(binding.Exchange, binding.Market, now, message))
}

func (cfg ServiceConfig) withDefaults() ServiceConfig {
	defaults := DefaultServiceConfig()
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = defaults.RefreshInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaults.RequestTimeout
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
