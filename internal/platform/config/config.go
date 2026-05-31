package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentProduction  Environment = "production"
)

func (e Environment) IsDevelopment() bool {
	return e != EnvironmentProduction
}

type Config struct {
	Environment      Environment `env:"ENV" envDefault:"development"`
	Port             uint16      `env:"PORT" envDefault:"8090"`
	DatabaseURL      string      `env:"DATABASE_URL" envDefault:"file:./app.db"`
	BinanceAPIKey    string      `env:"BINANCE_API_KEY"`
	BinanceAPISecret string      `env:"BINANCE_API_SECRET"`
	KrakenAPIKey     string      `env:"KRAKEN_API_KEY"`
	KrakenAPISecret  string      `env:"KRAKEN_API_SECRET"`
}

func (c Config) Addr() string {
	return ":" + strconv.Itoa(int(c.Port))
}

func (c Config) Validate() error {
	switch c.Environment {
	case EnvironmentDevelopment, EnvironmentProduction:
	default:
		return fmt.Errorf("ENV must be one of development or production")
	}

	return nil
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
