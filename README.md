# Prexel

**Deploy without limits.**

Prexel is a self-hosted deploy platform — Heroku/Railway-style ergonomics on your own VPS. A single
Go binary embeds the API, CLI, and Vue 3 web UI; Docker manages the workloads; Caddy handles TLS via
Let's Encrypt.

> **Status:** MVP v0.1.

## Quick start

```bash
# On a fresh Linux VPS (Debian/Ubuntu):
curl -fsSL https://get.prexel.dev | sudo sh

# Then browse to https://<your-server-ip>:3000 to run the setup wizard.
# Once configured, install the same binary on your laptop and connect:
prexel login --url https://prexel.example.com
prexel server list
prexel deploy my-app --watch
```

The install script:

- detects OS/arch and downloads the matching binary release,
- generates `PREXEL_SECRET_KEY` and `PREXEL_JWT_SECRET` via `openssl rand -hex 32`,
- writes `/etc/prexel/prexel.env` (chmod 600) and `/var/lib/prexel/` (chmod 700),
- installs and enables the `prexel.service` systemd unit,
- is idempotent — re-running upgrades the binary without touching data or secrets.

## Principles

- **One binary** — `prexel serve` runs the server; the other subcommands are the CLI client. The Vue
  frontend is embedded via `go:embed web/dist`.
- **Self-hosted, no vendor lock-in** — your SQLite DB and `/etc/prexel/` are the entire state. Move
  hosts by tarring `/var/lib/prexel/` and `/etc/prexel/`.
- **Predictable** — zero-downtime deploys with health checks, automatic rollback on failure,
  per-app SSL via Caddy, token-family refresh rotation.
- **Two surfaces, same actions** — anything you can do in the web UI you can do from the CLI, and
  vice versa.

## Repo layout

```
prexel/
├── main.go                # only `func main()` in the repo (~30 lines)
├── internal/              # backend services + CLI subcommands
├── web/                   # Vue 3 frontend (embedded into the prod binary)
├── migrations/            # golang-migrate SQL files
├── deployments/           # docker-compose for development
├── build/package/         # Dockerfiles + goreleaser config
├── configs/               # env file + Caddy templates
├── init/                  # systemd unit
└── scripts/               # install.sh, dev-up.sh
```

## Development

Dev is 100% Docker — no local Go or Node required.

```bash
make up                       # boot the dev stack (Go + air + Vue + Caddy)
make logs                     # tail logs
make ctl ARGS="version"       # run a CLI subcommand
make test                     # go test ./...
make lint                     # golangci-lint
make build-prod               # smoke-build the production image
```

Editing any `.go` file under `internal/` triggers `air` to rebuild and re-run `prexel serve`. The
Vite dev server hot-reloads the Vue UI separately on `:5173`.

### Frontend tests

```bash
cd web && npm test            # vitest stores/components/composables
```

### Backend tests

```bash
go test -race -cover ./...
```

See [`docs/testing.md`](docs/testing.md) for what is covered by automated tests versus what needs
manual smoke validation.

## Production

`scripts/install.sh` (used by the quick-start above) installs the binary at
`/usr/local/bin/prexel`, drops the systemd unit at `/etc/systemd/system/prexel.service`, generates
secrets in `/etc/prexel/prexel.env`, and starts the service. See
[`docs/release.md`](docs/release.md) for the release workflow.

## Documentation

Operational docs in this repo:

- [`docs/testing.md`](docs/testing.md) — what's covered by `go test`, how to
  add new tests, manual smoke procedure for releases.
- [`docs/release.md`](docs/release.md) — pre-flight checklist, tag → GoReleaser
  flow, Homebrew tap, hotfix path.
- [`docs/security.md`](docs/security.md) — threat model, mitigations and
  hardening recommendations for the operator.
- [`docs/architecture.md`](docs/architecture.md) — component diagram and
  sequence/state flows for deploy, auth and setup.
- [`docs/qa-guide.md`](docs/qa-guide.md) — long-form manual QA roteiro.
- [`docs/development.md`](docs/development.md) — dev environment, conventions,
  GitHub App registration.

Product specs (data models, API reference, UI guidelines, milestone scopes) are
in Confluence — start at the
[Prexel space overview](https://hotsed.atlassian.net/wiki/spaces/Prexel/overview).
