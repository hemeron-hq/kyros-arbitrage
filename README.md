# Kyros Arbitrage

Go SSR arbitrage dashboard with live public market feeds for the Bitcoin arbitrage challenge. The backend connects to Binance and Kraken BTC/USDT order books over WebSockets, keeps REST polling as fallback, normalizes the top 10 order-book levels, scores net-profitable paper routes, and streams projections to a Datastar dashboard.

## Scope

- Live Binance and Kraken BTC/USDT L2 top 10 feeds.
- WebSocket-first ingestion with polling fallback and stale-feed tracking.
- Backend-owned market state; the UI only renders projections from Go services.
- Net profitability after trading fees, book-depth slippage, latency penalty, and paper wallet limits.
- SQLite-backed persistent history for detected opportunities, simulated fills, and accumulated P&L.
- Per-exchange authenticated trading terms when read-only keys are present, with per-exchange-market fallback when keys are absent or rejected.
- Autonomous paper execution only. There is no real order placement or real withdrawal execution.

## Requirements

- Go 1.26.1
- `sqlc`
- `templ`
- `task`
- Tailwind CSS standalone CLI

## Commands

```bash
task sqlc
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
DATABASE_URL=file:./app.db
BINANCE_API_KEY=
BINANCE_API_SECRET=
KRAKEN_API_KEY=
KRAKEN_API_SECRET=
```

When valid exchange keys are present, Kyros refreshes account-specific trading fees and balances in the background. When an exchange has no valid keys or lacks the required read permission, only that exchange market falls back to code-owned demo terms and wallet profiles. Fallback state is visible in the dashboard terms-source health panel and recorded with simulated decisions, so demo results do not claim authenticated terms for exchanges using fallback data.

The `Connections` tab is read-only. It visualizes the environment-configured exchange credential state and the execution terms currently feeding the paper engine: source, freshness, fee rates, market constraints, balances, and transfer-fee inputs. It does not accept API keys in the browser and does not place real orders.

For local development with watchers:

```bash
task dev
```

## Endpoints

- `/` renders the SSR dashboard with feed status, net opportunities, paper wallets, terms-source health, execution-term details, and session P&L.
- `/stream` opens a Datastar SSE stream with coalesced dashboard patches.
- `/risk/mode` updates the persisted risk mode from the dashboard.
- `/healthz` returns process and feed-health status.
- `/api/history` returns persisted opportunity, simulated execution, and P&L history.
- `/api/metrics` returns feed and decision-loop speed evidence for diagnostics.
- `/assets/` serves CSS and the self-hosted Datastar client.

## Package Layout

- `cmd/server` is the thin process entrypoint.
- `internal/exchange` owns exchange-neutral client contracts, account/fee/constraint types, and shared market/order data types.
- `internal/exchange/{binance,kraken}` owns exchange-specific feed parsing, signed account/terms fetchers, wallet snapshots, and future order gateway behavior.
- `internal/terms` owns trading terms aggregation, fallback profiles, freshness, source health, and background refresh orchestration.
- `internal/market` owns market-data store, projections, and service lifecycle over exchange interfaces.
- `internal/arbitrage` owns direct arbitrage depth walking, net profitability, latency penalties, ranking, and autonomous decisions.
- `internal/history` owns persistent opportunity and execution history recording/reporting.
- `internal/orders` is reserved for future order placement, cancellation, fills, and paper/real gateways.
- `internal/portfolio` defines portfolio contracts; `internal/portfolio/paper` owns simulated exchange balances, fills, and session P&L.
- `internal/platform/config` owns strongly typed environment configuration.
- `internal/platform/database` owns application database opening, goose migration execution, and generated query access.
- `internal/server` owns process-level HTTP routing, health/history diagnostics, and static assets.
- `internal/ui` owns server-rendered templ views, dashboard route registration, Datastar SSE wiring, and dashboard view models split by UI domain.
- `sql/` owns migrations, queries, and the sqlc config. `gen/db` contains sqlc-generated Go code and should not be edited by hand.

## Market Data Defaults

The first runtime defaults are code-owned: Binance and Kraken `BTC/USDT`, L2 top 10, `3s` stale threshold, `3s` polling fallback interval, `15m` terms refresh interval, `30m` authenticated terms TTL, `5m` latency-model window, fallback fee profiles, fallback paper wallet seeds, and proactive WebSocket recycling before 24 hours. Exchange binding defaults live with the shared exchange contracts, while service timing stays in code so runtime controls can be added deliberately later instead of expanding environment configuration early.

The scoring hot path reads in-memory snapshots only. REST calls for authenticated fees, balances, withdrawal-fee metadata, and market constraints run in the background terms service and are never made while ranking a market-data update.

Every simulated route subtracts trading fees, slippage, latency penalty, and a rebalance cost derived from exchange transfer-fee terms. Authenticated routes are skipped when required transfer-fee inputs are unavailable, while fallback demo terms use visible code-owned transfer-fee assumptions.

## Wallet Model

Kyros models one wallet per exchange, with independent balances per asset. A direct cross-exchange paper trade buys BTC on the cheaper exchange using that exchange's USDT wallet and sells BTC on the richer exchange using that other exchange's BTC wallet. The engine never assumes instant transfers between exchanges. If one side lacks balance, the route is either partially sized down or rejected.

The current `internal/portfolio/paper` implementation is in-memory and seeded from authenticated balances when available, otherwise from visible fallback balances. Future real account implementations should satisfy the `internal/portfolio.Store` contract and live beside `paper` rather than replacing the arbitrage logic.

## Persistent History

Kyros writes each decision pass to the application SQLite database through sqlc-generated queries. The default local database URL is `file:./app.db`; override it with `DATABASE_URL`. Relative file URLs are resolved from the process working directory, so the repo Taskfile commands create `/app.db` at the repository root, not under `cmd/server`. The dashboard shows recent detected opportunities, simulated fills, stored counts, and accumulated historical P&L, while `/api/history` exposes the same data as JSON for demos and diagnostics.

Database migrations live in `sql/migrations`, use goose annotations, and are applied by `internal/platform/database` on startup. Runtime commands should be started from the repository root so the migration directory is available at `sql/migrations`; the Docker image copies that directory into `/app/sql/migrations`. Query code is generated from `sql/queries` into `gen/db`.

The application database is intentionally separate from the in-memory paper wallet. Restarting the app preserves persisted history and accumulated P&L reports, while the paper wallet still starts from authenticated or fallback balances.

## Docker and Deployment

Build and run the production image locally:

```bash
task docker-build
task docker-run
```

The image listens on `PORT` and stores the application database at `/var/lib/app/app.db` by default. Mount `/var/lib/app` as a persistent volume in production:

```bash
docker run --rm -p 8090:8090 -v database:/var/lib/app -e ENV=production kyros-arbitrage
```

`compose.yaml` runs the same container locally with a named volume mounted at `/var/lib/app`.
