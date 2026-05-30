package logging

import (
	"io"
	"os"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
	"github.com/rs/zerolog"
)

func New(environment config.Environment) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339

	return zerolog.New(writer(environment)).
		With().
		Timestamp().
		Str("service", "kyros-arbitrage").
		Logger()
}

func writer(environment config.Environment) io.Writer {
	if environment.IsDevelopment() {
		return zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
		}
	}

	return os.Stderr
}
