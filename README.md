# Kyros Arbitrage

Go SSR arbitrage dashboard with live public market feeds for the Bitcoin arbitrage challenge. The backend connects to Binance and Kraken BTC/USDT order books over WebSockets, keeps REST polling as fallback, normalizes the top 10 order-book levels, scores net-profitable paper routes, and streams projections to a Datastar dashboard.

## Scope

- Live Binance and Kraken BTC/USDT L2 top 10 feeds.
- WebSocket-first ingestion with polling fallback and stale-feed tracking.
- Backend-owned market state; the UI only renders projections from Go services.
- Net profitability after trading fees, book-depth slippage, latency penalty, and paper wallet limits.
- Per-exchange authenticated trading terms when read-only keys are present, with per-exchange-market fallback when keys are absent or rejected.
- Autonomous paper execution only. There is no real order placement or real withdrawal execution.

## Requirements

- Go 1.26.1
- `templ`
- `task`
- Tailwind CSS standalone CLI

## Commands

```bash
task generate
task css
task check
task run
```

The app listens on `:8090` by default. Override with `PORT=8080 task run`.

Optional local environment files are supported through `.env`. Existing shell environment values take precedence.
Use `.env.example` as the starting point for local overrides.

The only behavior-changing runtime configuration is:

```bash
ENV=development
PORT=8090
BINANCE_API_KEY=
BINANCE_API_SECRET=
KRAKEN_API_KEY=
KRAKEN_API_SECRET=
```

When valid exchange keys are present, Kyros refreshes account-specific trading fees and balances in the background. When an exchange has no valid keys or lacks the required read permission, only that exchange market falls back to code-owned demo terms and wallet profiles. Fallback state is visible in the dashboard terms-source health panel and recorded with simulated decisions, so demo results do not claim authenticated terms for exchanges using fallback data.

For local development with watchers:

```bash
task dev
```

## Endpoints

- `/` renders the SSR dashboard with feed status, net opportunities, paper wallets, terms-source health, and session P&L.
- `/stream` opens a Datastar SSE stream with coalesced dashboard patches.
- `/healthz` returns process and feed-health status.
- `/assets/` serves CSS and the self-hosted Datastar client.

## Package Layout

- `cmd/server` is the thin process entrypoint.
- `internal/exchange` owns exchange-neutral client contracts, account/fee/constraint types, and shared market/order data types.
- `internal/exchange/{binance,kraken}` owns exchange-specific feed parsing, signed account/terms fetchers, wallet snapshots, and future order gateway behavior.
- `internal/terms` owns trading terms aggregation, fallback profiles, freshness, source health, and background refresh orchestration.
- `internal/market` owns market-data store, projections, and service lifecycle over exchange interfaces.
- `internal/arbitrage` owns direct arbitrage depth walking, net profitability, latency penalties, ranking, and autonomous decisions.
- `internal/orders` is reserved for future order placement, cancellation, fills, and paper/real gateways.
- `internal/portfolio` defines portfolio contracts; `internal/portfolio/paper` owns simulated exchange balances, fills, and session P&L.
- `internal/platform/config` owns strongly typed environment configuration.
- `internal/server` owns HTTP routing, handlers, static assets, and Datastar SSE projection wiring.
- `internal/view` owns server-rendered templ views and view models.

## Market Data Defaults

The first runtime defaults are code-owned: Binance and Kraken `BTC/USDT`, L2 top 10, `3s` stale threshold, `3s` polling fallback interval, `15m` terms refresh interval, `30m` authenticated terms TTL, `5m` latency-model window, fallback fee profiles, fallback paper wallet seeds, and proactive WebSocket recycling before 24 hours. Exchange binding defaults live with the shared exchange contracts, while service timing stays in code so runtime controls can be added deliberately later instead of expanding environment configuration early.

The scoring hot path reads in-memory snapshots only. REST calls for authenticated fees, balances, withdrawal-fee metadata, and market constraints run in the background terms service and are never made while ranking a market-data update.

## Wallet Model

Kyros models one wallet per exchange, with independent balances per asset. A direct cross-exchange paper trade buys BTC on the cheaper exchange using that exchange's USDT wallet and sells BTC on the richer exchange using that other exchange's BTC wallet. The engine never assumes instant transfers between exchanges. If one side lacks balance, the route is either partially sized down or rejected.

The current `internal/portfolio/paper` implementation is in-memory and seeded from authenticated balances when available, otherwise from visible fallback balances. Future real account implementations should satisfy the `internal/portfolio.Store` contract and live beside `paper` rather than replacing the arbitrage logic.
