package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	db "github.com/hemeron-hq/kyros-arbitrage/gen/db"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const migrationsDir = "sql/migrations"

type Database struct {
	url     string
	path    string
	sqlDB   *sql.DB
	queries *db.Queries
}

func Open(ctx context.Context, databaseURL string) (*Database, error) {
	dsn, displayPath, err := parseSQLiteURL(databaseURL)
	if err != nil {
		return nil, err
	}
	dsn = withSQLitePragmas(dsn)
	if err := ensureParentDir(displayPath); err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	database := &Database{
		url:     databaseURL,
		path:    displayPath,
		sqlDB:   sqlDB,
		queries: db.New(sqlDB),
	}
	if err := database.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return database, nil
}

func (d *Database) Close() error {
	if d == nil || d.sqlDB == nil {
		return nil
	}
	return d.sqlDB.Close()
}

func (d *Database) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.sqlDB.BeginTx(ctx, opts)
}

func (d *Database) Queries() *db.Queries {
	return d.queries
}

func (d *Database) Path() string {
	return d.path
}

func (d *Database) URL() string {
	return d.url
}

func (d *Database) migrate(ctx context.Context) error {
	if _, err := d.sqlDB.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := d.sqlDB.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set sqlite busy timeout: %w", err)
	}

	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("configure database migrations: %w", err)
	}
	if err := goose.UpContext(ctx, d.sqlDB, migrationsDir); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}

func parseSQLiteURL(raw string) (dsn string, displayPath string, err error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", errors.New("DATABASE_URL is required")
	}
	if raw == ":memory:" {
		return raw, raw, nil
	}

	parsed, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", "", fmt.Errorf("parse DATABASE_URL: %w", parseErr)
	}
	if parsed.Scheme != "" && parsed.Scheme != "file" {
		return "", "", fmt.Errorf("unsupported DATABASE_URL scheme %q: only sqlite file URLs are supported", parsed.Scheme)
	}

	if parsed.Scheme == "file" {
		path := parsed.Path
		if parsed.Opaque != "" {
			path = parsed.Opaque
		}
		if parsed.Host != "" && parsed.Host != "localhost" {
			path = filepath.Join(string(filepath.Separator), parsed.Host, parsed.Path)
		}
		if path == "" {
			return "", "", errors.New("DATABASE_URL file path is required")
		}
		return raw, path, nil
	}

	return raw, raw, nil
}

func withSQLitePragmas(dsn string) string {
	const pragmas = "_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
	if strings.Contains(dsn, "?") {
		return dsn + "&" + pragmas
	}
	return dsn + "?" + pragmas
}

func ensureParentDir(path string) error {
	if path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return nil
}
