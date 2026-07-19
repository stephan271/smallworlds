# SmallWorlds Operator Console

This directory contains the first Bootstrap Launcher tracer from the [Operator Console implementation plan](../plans-and-walkthroughs/implementation_plan-operator-console.md). A Go process binds to loopback, embeds the static Svelte 5 client, persists Cluster Profiles and Workflow Runs in SQLite, and exposes a versioned OpenAPI-described interface.

The current fake `VerifyLauncher` Journey Task exercises the complete Inspect → Plan → Approve → Execute → Verify contract without mutating a cluster.

## Build

```bash
cd operator-console/web
npm ci
npm run generate:api
npm run check
npm run build

cd ..
go test ./...
go build ./cmd/smallworlds-admin
```

The web build is written into `internal/webui/dist` and embedded by the Go build. Generated web assets are intentionally not committed.

## Run

```bash
go run ./cmd/smallworlds-admin
```

The launcher selects a random `127.0.0.1` port and opens a one-time authenticated browser URL. Running the command again reopens the existing per-user launcher instead of starting a competing process.

For a controlled development run:

```bash
go run ./cmd/smallworlds-admin \
  --port 4174 \
  --data-dir .tmp/development \
  --token development-token \
  --no-browser
```

## Verify

```bash
go test ./...

cd web
npm run check
npm run build
npm run test:e2e
```

The Playwright journey covers English and German, keyboard submission, automated axe checks, workflow progress, and browser reload recovery.

## Public interface

The contract is defined in [`api/openapi.json`](api/openapi.json). Regenerate the browser types after changing it:

```bash
cd web
npm run generate:api
```

The browser never receives provisioning authority or secret values. Go owns sessions, persistence, plans, approvals, execution, event streaming, and verification.
