-- +goose Up
CREATE TABLE IF NOT EXISTS risk_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  mode TEXT NOT NULL CHECK (mode IN ('conservative', 'balanced', 'aggressive')),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO risk_settings (id, mode) VALUES (1, 'balanced');

-- +goose Down
DROP TABLE IF EXISTS risk_settings;
