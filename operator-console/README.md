# SmallWorlds Operator Console

This directory contains the first Bootstrap Launcher tracer from the [Operator Console implementation plan](../plans-and-walkthroughs/implementation_plan-operator-console.md). A Go process binds to loopback, embeds the static Svelte 5 client, persists Cluster Profiles and Workflow Runs in SQLite, and exposes a versioned OpenAPI-described interface.

The current fake `VerifyLauncher` Journey Task exercises the complete Inspect → Plan → Approve → Execute → Verify contract without mutating a cluster.

The Setup Journey also includes the first Launcher Vault credential-custody flow. The launcher encrypts credential values in a separate age-encrypted file, stores only references and safe metadata in SQLite, and never returns a submitted value to the browser. It uses the native operating-system credential store for its random wrapping key when available, with a passphrase-unlocked fallback for headless Linux and other hosts where that facility cannot be used.

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

## Launcher Vault custody

- macOS uses Keychain, Windows uses Credential Manager, and Linux/BSD uses a Secret Service provider when one is available.
- If the native credential store is unavailable, choose the passphrase fallback. Keep that passphrase outside the Launcher Host; it is required after every launcher restart and cannot currently be recovered by the product.
- The launcher data directory is restricted to the current operating-system user (`0700`/`0600` on Unix and a protected current-user DACL on Windows).
- Credential read endpoints expose only presence, source, expiry, and rotation status. Replacement is another write-only submission; removal is explicit.
