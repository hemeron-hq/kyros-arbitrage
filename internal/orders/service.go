package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

var ErrUnsupportedProvider = errors.New("order provider unsupported")

type Service struct {
	providers map[exchange.Venue]exchange.OrderPlacer
}

func NewService(providers map[exchange.Venue]exchange.OrderPlacer) *Service {
	copied := make(map[exchange.Venue]exchange.OrderPlacer, len(providers))
	for venue, provider := range providers {
		if provider != nil {
			copied[venue] = provider
		}
	}
	return &Service{providers: copied}
}

func (s *Service) Place(ctx context.Context, request exchange.OrderRequest) (exchange.OrderResult, error) {
	provider := s.providers[request.Venue]
	if provider == nil {
		return exchange.OrderResult{}, fmt.Errorf("%w: %s", ErrUnsupportedProvider, request.Venue)
	}
	return provider.PlaceOrder(ctx, request)
}
