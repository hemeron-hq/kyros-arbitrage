# Kyros Arbitrage

Go SSR arbitrage dashboard with live public market feeds for the Bitcoin arbitrage challenge. The backend connects to Binance and Kraken BTC/USDT order books over WebSockets, keeps REST polling as fallback, normalizes the top 10 order-book levels, scores depth-aware gross routes, and streams read-only projections to a simple Datastar dashboard.

## Scope

- Live Binance and Kraken BTC/USDT L2 top 10 feeds.
- WebSocket-first ingestion with polling fallback and stale-feed tracking.
- Backend-owned market state; the UI only renders projections from the market store.
- No real trading keys, order placement, withdrawals, wallet mutation, or persistence in this phase.

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

For local development with watchers:

```bash
task dev
```

## Endpoints

- `/` renders the SSR dashboard with feed status and gross spread rows.
- `/stream` opens a Datastar SSE stream with coalesced dashboard patches.
- `/healthz` returns process and feed-health status.
- `/assets/` serves CSS and the self-hosted Datastar client.

## Package Layout

- `cmd/server` is the thin process entrypoint.
- `internal/exchange` owns exchange-neutral contracts and data types shared by market data and future order flows.
- `internal/exchange/{binance,kraken}` owns venue-specific parsing and connection behavior.
- `internal/market` owns market-data store, projections, and service lifecycle over exchange interfaces.
- `internal/strategy` owns route and opportunity calculations from normalized books.
- `internal/orders` is reserved for future order placement, cancellation, fills, and paper/real gateways.
- `internal/portfolio` is reserved for future venue balances, inventory, reservations, and account state.
- `internal/platform/config` owns strongly typed environment configuration.
- `internal/server` owns HTTP routing, handlers, static assets, and Datastar SSE projection wiring.
- `internal/view` owns server-rendered templ views and view models.

## Market Data Defaults

The first runtime defaults are code-owned: Binance and Kraken `BTC/USDT`, L2 top 10, `3s` stale threshold, `3s` polling fallback interval, and proactive WebSocket recycling before 24 hours. Exchange binding defaults live with the shared exchange contracts, while market service timing stays in `internal/market` so runtime controls can be added deliberately later instead of expanding environment configuration early.
