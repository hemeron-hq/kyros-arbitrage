-- +goose Up
CREATE TABLE IF NOT EXISTS opportunities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  opportunity_id TEXT NOT NULL UNIQUE,
  observed_at TEXT NOT NULL,
  market TEXT NOT NULL,
  buy_exchange TEXT NOT NULL,
  sell_exchange TEXT NOT NULL,
  base_size TEXT NOT NULL,
  buy_notional TEXT NOT NULL,
  sell_notional TEXT NOT NULL,
  gross_profit TEXT NOT NULL,
  gross_bps TEXT NOT NULL,
  buy_fee TEXT NOT NULL,
  sell_fee TEXT NOT NULL,
  trading_fees TEXT NOT NULL,
  trading_fee_bps TEXT NOT NULL,
  slippage_cost TEXT NOT NULL,
  slippage_bps TEXT NOT NULL,
  latency_penalty TEXT NOT NULL,
  latency_penalty_bps TEXT NOT NULL,
  rebalance_cost TEXT NOT NULL,
  expected_net_profit TEXT NOT NULL,
  expected_net_bps TEXT NOT NULL,
  decision TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  terms_source TEXT NOT NULL,
  partial INTEGER NOT NULL,
  recorded_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_opportunities_observed_at
  ON opportunities(observed_at DESC);

CREATE INDEX IF NOT EXISTS idx_opportunities_decision
  ON opportunities(decision, reason_code);

CREATE TABLE IF NOT EXISTS executions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  opportunity_id TEXT NOT NULL UNIQUE,
  executed_at TEXT NOT NULL,
  market TEXT NOT NULL,
  buy_exchange TEXT NOT NULL,
  sell_exchange TEXT NOT NULL,
  base_size TEXT NOT NULL,
  buy_notional TEXT NOT NULL,
  sell_notional TEXT NOT NULL,
  buy_fee TEXT NOT NULL,
  sell_fee TEXT NOT NULL,
  latency_penalty TEXT NOT NULL,
  rebalance_cost TEXT NOT NULL,
  net_profit TEXT NOT NULL,
  terms_source TEXT NOT NULL,
  recorded_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(opportunity_id) REFERENCES opportunities(opportunity_id)
);

CREATE INDEX IF NOT EXISTS idx_executions_executed_at
  ON executions(executed_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_executions_executed_at;
DROP TABLE IF EXISTS executions;
DROP INDEX IF EXISTS idx_opportunities_decision;
DROP INDEX IF EXISTS idx_opportunities_observed_at;
DROP TABLE IF EXISTS opportunities;
