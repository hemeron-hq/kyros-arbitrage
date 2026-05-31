-- +goose Up
ALTER TABLE opportunities ADD COLUMN buy_liquidity TEXT NOT NULL DEFAULT 'taker';
ALTER TABLE opportunities ADD COLUMN sell_liquidity TEXT NOT NULL DEFAULT 'taker';
ALTER TABLE opportunities ADD COLUMN rebalance_exposure TEXT NOT NULL DEFAULT '0';
ALTER TABLE opportunities ADD COLUMN fee_hurdle_bps TEXT NOT NULL DEFAULT '0';
ALTER TABLE opportunities ADD COLUMN edge_after_fees_bps TEXT NOT NULL DEFAULT '0';
ALTER TABLE opportunities ADD COLUMN missing_bps TEXT NOT NULL DEFAULT '0';

ALTER TABLE executions ADD COLUMN buy_liquidity TEXT NOT NULL DEFAULT 'taker';
ALTER TABLE executions ADD COLUMN sell_liquidity TEXT NOT NULL DEFAULT 'taker';
ALTER TABLE executions ADD COLUMN rebalance_exposure TEXT NOT NULL DEFAULT '0';

-- +goose Down
ALTER TABLE executions DROP COLUMN rebalance_exposure;
ALTER TABLE executions DROP COLUMN sell_liquidity;
ALTER TABLE executions DROP COLUMN buy_liquidity;

ALTER TABLE opportunities DROP COLUMN missing_bps;
ALTER TABLE opportunities DROP COLUMN edge_after_fees_bps;
ALTER TABLE opportunities DROP COLUMN fee_hurdle_bps;
ALTER TABLE opportunities DROP COLUMN rebalance_exposure;
ALTER TABLE opportunities DROP COLUMN sell_liquidity;
ALTER TABLE opportunities DROP COLUMN buy_liquidity;
