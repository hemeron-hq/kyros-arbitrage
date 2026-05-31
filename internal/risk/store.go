package risk

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	db "github.com/hemeron-hq/kyros-arbitrage/gen/db"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/database"
)

type Store struct {
	queries *db.Queries
}

func NewStore(database *database.Database) *Store {
	return &Store{queries: database.Queries()}
}

func (s *Store) LoadMode(ctx context.Context) (Mode, error) {
	value, err := s.queries.GetRiskMode(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ModeBalanced, nil
		}
		return "", fmt.Errorf("load risk mode: %w", err)
	}
	mode, err := ParseMode(value)
	if err != nil {
		return "", fmt.Errorf("load risk mode %q: %w", value, err)
	}
	return mode, nil
}

func (s *Store) SaveMode(ctx context.Context, mode Mode) error {
	if err := s.queries.UpsertRiskMode(ctx, string(mode)); err != nil {
		return fmt.Errorf("save risk mode: %w", err)
	}
	return nil
}
