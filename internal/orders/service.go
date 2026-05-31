package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

var ErrUnsupportedProvider = errors.New("order provider unsupported")

type Service struct {
	providers map[exchange.ID]exchange.OrderPlacer
}

func NewService(providers map[exchange.ID]exchange.OrderPlacer) *Service {
	copied := make(map[exchange.ID]exchange.OrderPlacer, len(providers))
	for exchangeID, provider := range providers {
		if provider != nil {
			copied[exchangeID] = provider
		}
	}
	return &Service{providers: copied}
}

func (s *Service) Place(ctx context.Context, request exchange.OrderRequest) (exchange.OrderResult, error) {
	provider := s.providers[request.Exchange]
	if provider == nil {
		return exchange.OrderResult{}, fmt.Errorf("%w: %s", ErrUnsupportedProvider, request.Exchange)
	}
	return provider.PlaceOrder(ctx, request)
}
