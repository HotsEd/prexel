# Development guide

Working notes for anyone (including Claude Code) hacking on this repo. The user-facing project
spec lives in Confluence — the top-level `CLAUDE.MD` lists the index pages.

## Layout in one paragraph

Single Go binary `prexel`. `main.go` at the repo root is ~5 lines and only injects two
`embed.FS` (migrations, web/dist) into `internal/cli.Execute`. The whole Cobra tree lives in
`internal/cli/command/`. `prexel serve` is the server; everything else is client. The frontend
is Vue 3 under `web/`, embedded via `go:embed web/dist` for production. Dev environment is
entirely Docker (`deployments/docker-compose.yml`).

## How to run things

You do not need a local Go toolchain. Everything goes through Docker Compose.

```bash
make up           # start prexel (air hot reload) + Vite + Caddy
make logs         # tail logs
make ctl ARGS="version"   # invoke a CLI subcommand
make down         # stop
make test
make lint
make tidy
make migrate-up
```

The Go service inside the `prexel` container runs `air -c .air.toml`, which watches
`*.go`/`*.sql`/`*.tmpl` and rebuilds + reruns `go run . serve` on change.

When you add Go dependencies, run `make tidy` so go.sum stays consistent.

## Where things live

- `internal/config/` — Viper, validates `PREXEL_SECRET_KEY` / `PREXEL_JWT_SECRET` ≥ 32 chars.
- `internal/crypto/` — AES-256-GCM with HKDF key derivation. Used by anything storing SSH
  keys or secrets.
- `internal/db/` — opens SQLite via pure-Go `modernc.org/sqlite`, applies migrations from an
  `fs.FS` using golang-migrate.
- `internal/api/router.go` — Chi router with middleware stack.
- `internal/api/apimiddleware/` — custom middlewares. Named `apimiddleware` to avoid clashing
  with `github.com/go-chi/chi/v5/middleware`.
- `internal/api/handler/` — HTTP handlers. Currently mostly `NotImplemented` stubs (A2+).
- `internal/tlsutil/` — generates a self-signed cert on first boot under
  `${PREXEL_DATA_DIR}/tls/`.
- `internal/cli/command/` — Cobra commands. `serve.go` is the only non-trivial one right now.
- `migrations/` — SQL files for golang-migrate. Add new pairs with
  `make migrate-new NAME=...`. The full v0.1 schema is in `001_initial_schema.{up,down}.sql`.

## Conventions

- Public packages document themselves with a package comment.
- Errors wrap context: `return fmt.Errorf("open: %w", err)`.
- Logging uses `log/slog` with the default text handler.
- Background loops (status, DNS, SSL — added in later milestones) live in their own packages
  and are started by `serve.go` after migrations apply.
- New endpoints go under `/api/v1`. Health is unauthenticated; everything else under the
  authenticated group requires `Authorization: Bearer <JWT HS256>`.

## What to NOT do here (yet)

- No tests in the codebase yet — Milestone D1 will add them. Don't fabricate test stubs.
- No real auth handlers, setup wizard, deploy engine, etc. Most `/api/v1/*` routes still
  return 501. Land them in their respective milestones (A2 through A8).
- Don't add a separate `prexelctl` binary. There is only `prexel` with subcommands.
- Don't move `main.go` under `cmd/` — the project deliberately follows the single-binary
  style (caddy/consul/nomad).

## GitHub App registration (Milestone A5)

The `git_sources` of type `github_app` need a registered GitHub App. The agent ships
with the integration *implemented* but *not provisioned* — env vars are empty by
default, in which case `POST /git-sources {type:"github_app"}` returns
`github_app_not_configured`. The other two source types (`ssh_key`, `token`) work
out of the box.

To wire it up:

1. Browse to https://github.com/settings/apps/new (for a personal account) or
   `https://github.com/organizations/<org>/settings/apps/new` (for an org).
2. **Homepage URL:** `https://your-prexel.example.com`.
3. **Callback URL:** `https://your-prexel.example.com/api/v1/git-sources/github/callback`
   (this is the public, no-auth endpoint that GitHub redirects browsers to).
4. **Webhook:** uncheck *Active* for v0.1 (we wire webhooks in v0.2 push-to-deploy).
5. **Permissions → Repository:** `Contents: Read-only`, `Metadata: Read-only`.
6. **Subscribe to events:** none for v0.1.
7. **Where can this GitHub App be installed?** Any account (or only this account,
   your call).
8. After creation: copy the **App ID** and the URL slug (the bit after
   `https://github.com/apps/`). Then **Generate a private key** — GitHub downloads a
   `.pem` file. Paste its full contents into the env var.

Then export:

```bash
PREXEL_GITHUB_APP_ID=123456
PREXEL_GITHUB_APP_SLUG=my-prexel-app
PREXEL_GITHUB_APP_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----"
```

In docker-compose: pass the same vars through `environment:` on the `prexel`
service. Restart with `make down && make up`.

### Install flow

1. User visits the Prexel UI and clicks *Add GitHub source*.
2. SPA calls `GET /api/v1/git-sources/github/install-url` (auth required) →
   `{ "url": "https://github.com/apps/<slug>/installations/new" }`.
3. SPA opens that URL in a popup. User picks repos to grant Prexel access to.
4. GitHub redirects the popup to `/api/v1/git-sources/github/callback?installation_id=…`.
   That route is public and renders a tiny HTML page that posts the
   `installation_id` back to `window.opener` via `postMessage`, then closes.
5. SPA receives the message and does the authenticated
   `POST /api/v1/git-sources {type:"github_app", name, installation_id}` to persist.

The server caches installation access tokens in memory (per `installation_id`)
and refreshes when within five minutes of expiry — GitHub installation tokens
live for one hour.

## Useful URLs in dev

- Health: `https://localhost:3443/api/v1/healthz`
- Vite dev server: `http://localhost:5173`
- Caddy admin: `http://localhost:2019/config/`
