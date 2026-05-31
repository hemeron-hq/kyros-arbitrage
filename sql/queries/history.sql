-- name: InsertOpportunity :exec
INSERT OR IGNORE INTO opportunities (
  opportunity_id,
  observed_at,
  market,
  buy_exchange,
  sell_exchange,
  buy_liquidity,
  sell_liquidity,
  base_size,
  buy_notional,
  sell_notional,
  gross_profit,
  gross_bps,
  buy_fee,
  sell_fee,
  trading_fees,
  trading_fee_bps,
  slippage_cost,
  slippage_bps,
  latency_penalty,
  latency_penalty_bps,
  rebalance_cost,
  rebalance_exposure,
  fee_hurdle_bps,
  edge_after_fees_bps,
  missing_bps,
  expected_net_profit,
  expected_net_bps,
  decision,
  reason_code,
  terms_source,
  partial
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertExecution :exec
INSERT OR IGNORE INTO executions (
  opportunity_id,
  executed_at,
  market,
  buy_exchange,
  sell_exchange,
  buy_liquidity,
  sell_liquidity,
  base_size,
  buy_notional,
  sell_notional,
  buy_fee,
  sell_fee,
  latency_penalty,
  rebalance_cost,
  rebalance_exposure,
  net_profit,
  terms_source
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListRecentOpportunities :many
SELECT
  opportunity_id,
  observed_at,
  market,
  buy_exchange,
  sell_exchange,
  buy_liquidity,
  sell_liquidity,
  base_size,
  buy_notional,
  sell_notional,
  gross_profit,
  gross_bps,
  buy_fee,
  sell_fee,
  trading_fees,
  trading_fee_bps,
  slippage_cost,
  slippage_bps,
  latency_penalty,
  latency_penalty_bps,
  rebalance_cost,
  rebalance_exposure,
  fee_hurdle_bps,
  edge_after_fees_bps,
  missing_bps,
  expected_net_profit,
  expected_net_bps,
  decision,
  reason_code,
  terms_source,
  partial
FROM opportunities
ORDER BY observed_at DESC, id DESC
LIMIT ?;

-- name: ListOpportunitiesPage :many
SELECT
  opportunity_id,
  observed_at,
  market,
  buy_exchange,
  sell_exchange,
  buy_liquidity,
  sell_liquidity,
  base_size,
  buy_notional,
  sell_notional,
  gross_profit,
  gross_bps,
  buy_fee,
  sell_fee,
  trading_fees,
  trading_fee_bps,
  slippage_cost,
  slippage_bps,
  latency_penalty,
  latency_penalty_bps,
  rebalance_cost,
  rebalance_exposure,
  fee_hurdle_bps,
  edge_after_fees_bps,
  missing_bps,
  expected_net_profit,
  expected_net_bps,
  decision,
  reason_code,
  terms_source,
  partial
FROM opportunities
ORDER BY observed_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListRecentExecutions :many
SELECT
  opportunity_id,
  executed_at,
  market,
  buy_exchange,
  sell_exchange,
  buy_liquidity,
  sell_liquidity,
  base_size,
  buy_notional,
  sell_notional,
  buy_fee,
  sell_fee,
  latency_penalty,
  rebalance_cost,
  rebalance_exposure,
  net_profit,
  terms_source
FROM executions
ORDER BY executed_at DESC, id DESC
LIMIT ?;

-- name: ListExecutionsPage :many
SELECT
  opportunity_id,
  executed_at,
  market,
  buy_exchange,
  sell_exchange,
  buy_liquidity,
  sell_liquidity,
  base_size,
  buy_notional,
  sell_notional,
  buy_fee,
  sell_fee,
  latency_penalty,
  rebalance_cost,
  rebalance_exposure,
  net_profit,
  terms_source
FROM executions
ORDER BY executed_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: CountOpportunities :one
SELECT count(*) FROM opportunities;

-- name: CountExecutions :one
SELECT count(*) FROM executions;

-- name: SumExecutionNetProfit :one
SELECT COALESCE(SUM(CAST(net_profit AS REAL)), 0) FROM executions;
