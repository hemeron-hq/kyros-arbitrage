package main

import (
	"errors"
	"net/http"
	"os"

	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/logging"
	"github.com/hemeron-hq/kyros-arbitrage/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logger := logging.New(config.EnvironmentDevelopment)
		logger.Error().Err(err).Msg("load config")
		os.Exit(1)
	}

	logger := logging.New(cfg.Environment)
	httpServer := server.New(cfg)

	logger.Info().Str("addr", httpServer.Addr).Str("environment", string(cfg.Environment)).Msg("starting kyros arbitrage scaffold")
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error().Err(err).Msg("server stopped")
		os.Exit(1)
	}
}
