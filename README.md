# Kyros Arbitrage

Minimal Go SSR scaffold for the Bitcoin arbitrage challenge. This version proves the server-rendered stack only: Go, templ, Datastar SSE, TemplUI, Tailwind CSS, static assets, and a health endpoint.

## Scope

- No real exchange feeds yet.
- No trading engine yet.
- No persistence yet.
- The UI is shaped for BTC arbitrage so exchange adapters, normalized quotes, opportunity scoring, and paper execution can be added incrementally.

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

- `/` renders the SSR dashboard shell.
- `/stream` opens a Datastar SSE heartbeat stream.
- `/healthz` returns `{"ok":true}`.
- `/assets/` serves CSS and the self-hosted Datastar client.

## Package Layout

- `cmd/server` is the thin process entrypoint.
- `internal/platform/config` owns the minimal strongly typed environment configuration: `PORT` and `ENV`.
- `internal/server` owns HTTP routing, handlers, static assets, and Datastar SSE.
- `internal/view` owns server-rendered templ views and view models.
