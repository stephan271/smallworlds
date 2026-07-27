# SmallWorlds Operator Console

This directory contains the first Bootstrap Launcher tracer from the [Operator Console implementation plan](../plans-and-walkthroughs/implementation_plan-operator-console.md). A Go process binds to loopback, embeds the static Svelte 5 client, persists Cluster Profiles and Workflow Runs in SQLite, and exposes a versioned OpenAPI-described interface.

The current fake `VerifyLauncher` Journey Task exercises the complete Inspect → Plan → Approve → Execute → Verify contract without mutating a cluster.

The Setup Journey also includes the first Launcher Vault credential-custody flow. The launcher encrypts credential values in a separate age-encrypted file, stores only references and safe metadata in SQLite, and never returns a submitted value to the browser. It uses the native operating-system credential store for its random wrapping key when available, with a passphrase-unlocked fallback for headless Linux and other hosts where that facility cannot be used.

## Interface shape

The client is organised the way [`gettingStarted.md`](../gettingStarted.md) describes the journey, and that document is the specification for it. Three tabs sit under the installation's name:

- **Setting up** — the ordered stages of the Setup Journey: choose what the community gets → establish the settings repository → prepare the machine → install → make administration private → protect the data → finish. The order is load-bearing: the repository must exist and hold the configuration before any machine is touched, because installing *is* the act of pointing a new cluster at it. Every stage is reachable at any time; one that cannot be worked on yet says why rather than disappearing, and each carries the same Back/Continue pair.
- **What happened** — two things. *What the cluster is doing* reads the Cluster Node directly (`GET /api/v1/cluster-detail`): node readiness, every Argo CD Application's sync and health, and the workloads that have not settled together with the reason the cluster itself gives — `secret "keycloak-admin-creds" not found`, and so on. It refreshes itself while anything is still moving. Below it, *what this console did* is the Activity record for the whole installation, replayed from the event stream rather than only what the open browser session watched. The two answer different questions: a run parked at `awaiting-convergence` reports that something is unfinished and nothing whatever about what.
- **This installation** — details, what the Launcher Vault holds, Recovery Bundle export and import, the `VerifyLauncher` rehearsal, and shutting the installation down. Decommissioning is offered only once there is something out there to shut down.

One input is deliberately not optional and not hidden: the Cluster Secrets manifest on the install stage. Keycloak, Garage, Grafana and the Argo CD repository credential each mount a Secret it is the only source of, and without it the install succeeds, Argo CD reports `Degraded`, and those pods sit in `CreateContainerConfigError` for as long as anyone is willing to wait. Both the stage and `POST /api/v1/local-bootstrap/plan` refuse (`cluster_secrets_required`) unless a manifest is supplied or one is already custodied in the vault.

Two rules shape what a stage shows. Anything the launcher can settle without a decision it settles silently and reports — the vault opens with the operating system's own credential store, bootstrap assets and the Hetzner toolchain are fetched by the stage that needs them, and the private network gets a default name. Anything with a sensible default sits behind that stage's *Advanced settings*. Each answer is asked for exactly once, in the stage it belongs to: the domain, release, environment extension and administration address are collected on the first stage and read from there by every later one.

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

## Native releases

Release archives are available for Linux x86-64/ARM64, macOS Intel/Apple
Silicon, and Windows x86-64. They embed this client and require no developer or
infrastructure tooling on the Launcher Host. See [native release packaging](../docs/releases/bootstrap-launcher-packaging.md) for artifact names, checksum/signature verification, platform behavior, and English/German failure guidance.

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

The Local internet-exposed stable-release check must run against a real router
and public IPv4. It verifies trusted public member endpoints, public Headscale
coordination, private Console access from an enrolled Launcher Host, and denial
of a forged operator Host header on the public IP. Its HTML report includes a
timestamped `local-public-stable-release-evidence.json` attachment:

```bash
cd web
SMALLWORLDS_RELEASE=v1.2.27 \
SMALLWORLDS_PUBLIC_DOMAIN=community.example \
SMALLWORLDS_PUBLIC_IPV4=203.0.113.10 \
SMALLWORLDS_PRIVATE_CONSOLE_URL=https://console.operator.internal \
npm run test:e2e:local-public
```

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
