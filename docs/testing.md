# Testing strategy

Prexel v0.1 ships a focused test suite that exercises the critical paths without inflating coverage
for its own sake. Anything that requires a live Docker engine, real SSH host, or production Caddy
admin API is left to a manual smoke pass at release time.

## What's covered by `go test`

| Package                           | Test file(s)                                       | Notes                                                                                  |
|-----------------------------------|----------------------------------------------------|----------------------------------------------------------------------------------------|
| `internal/crypto`                 | `aes_test.go`                                      | AES-256-GCM round trip, nonce uniqueness, tamper detection, wrong-key rejection.       |
| `internal/auth`                   | `jwt_test.go`, `refresh_test.go`, `password_validation_test.go`, `testutil_test.go` | JWT issue/parse, expired/wrong-alg rejection; refresh-token rotation + reuse-detection + family invalidation; strong-password policy. |
| `internal/setup`                  | `setup_test.go`                                    | MarkComplete idempotency under concurrent callers, EnsureInstanceID idempotency.       |
| `internal/domain`                 | `validation_test.go`                               | Hostname regex/normalization, `FindAvailablePort` range scan.                          |
| `internal/app`                    | `validation_test.go`                               | Name regex, port/limits parsing, env-var key regex, health-check defaults + ranges.    |
| `internal/secret`                 | `service_test.go`                                  | Upsert encrypts at rest, List masks values, Resolve* filters runtime vs build-time.    |
| `internal/ssh`                    | `keygen_test.go`                                   | Ed25519 generation round-trip; ParseKey accepts Ed25519 + RSA; nil/empty input rejected. |
| `internal/eventbus`               | `bus_test.go`                                      | Publish/Subscribe, replay with Last-Event-ID, ring-buffer eviction, multi-subscriber.  |
| `internal/api/apimiddleware`      | `middleware_test.go`                               | Auth (Bearer required, JWT validated), SetupGate (pre/post-setup behaviour), LoginRateLimit (5-then-429), RequestSize cap. |
| `internal/api/handler`            | `auth_test.go`, `app_test.go`                      | Setup wizard happy path, login + refresh + token reuse, app CRUD + secrets PUT/GET masked/DELETE. |
| `internal/cli/command`            | `render_test.go`                                   | Golden tests for `server list`, `app list`, `version` outputs.                         |

Run with:

```bash
go test -race -cover ./...
```

Approximate coverage on the tested packages: `eventbus` 95%, `secret` 85%, `crypto` 85%, `setup` 75%,
`apimiddleware` 71%, `auth` 58%, `ssh` 30%, `domain` 8%, `app` 14%, `handler` 18%. The low-percentage
packages have shallow public APIs with deep dependencies on DB/network — what's tested is the
exposed correctness contract, not internal repository plumbing.

## Frontend

`web/` ships Vitest:

```bash
cd web && npm test
```

Covers:

- `src/stores/auth.test.ts` — initial state, `setToken`/`clear`, `initialize` swallows errors and is
  idempotent, refresh hydration.
- `src/composables/useApi.test.ts` — `useApi` singleton behaviour and interceptor registration,
  `apiErrorMessage` helper for axios errors.
- `src/components/StatusBadge.test.ts` — tone class mapping for every documented status, pulse
  behaviour for in-flight states.

## Adding a new test

- **Backend service package**: put the test file next to the code
  (`internal/foo/foo_test.go`). For anything that touches the DB, use
  `modernc.org/sqlite` opened on `:memory:` and apply migrations via the
  embed.FS (same pattern as `internal/auth/refresh_test.go`). Reuse fixtures
  from `internal/auth/testutil_test.go` for user/JWT helpers.
- **HTTP handler**: spin up the handler inside a `httptest.NewServer` with a
  real router subset (see `internal/api/handler/auth_test.go`,
  `app_test.go`). Stub Docker / Caddy / SSH via interface seams — handlers
  receive `*service.Service` or a small dep struct, never the concrete
  Docker client.
- **CLI command**: golden tests. Build the cobra command, run with stubbed
  client (`internal/cli/client/http.go` can be swapped via the
  `clientFactory` hook in tests), capture stdout, diff against a `.golden`
  file (`internal/cli/command/render_test.go` shows the pattern). Update
  goldens with `go test ./internal/cli/command -update`.
- **Frontend**: colocate `.test.ts` with the component / store / composable.
  Vitest + jsdom is already configured; avoid mounting the whole router —
  test stores / composables in isolation, and components with a thin
  wrapper.

## Cobertura com flag

```bash
go test -cover ./...                                # imprime % por package
go test -coverprofile=/tmp/cov.out ./internal/...   # gera profile
go tool cover -html=/tmp/cov.out                    # explora no browser
```

Sob Docker (recomendado — mesma toolchain do CI):

```bash
docker compose -f deployments/docker-compose.yml exec -T prexel \
  sh -c "cd /workspace && go test -cover ./..."
```

## What's **not** covered (manual smoke)

These packages drive Docker/Caddy/SSH/Git directly. Mocking them out for unit tests would replace
real bugs with mock bugs; instead, validate them at release time on the smoke VPS.

- `internal/build` — `docker build` over local socket and `ssh://` URLs.
- `internal/deploy` — zero-downtime swap, healthcheck loop, rollback, reconciliation.
- `internal/docker` — local + remote providers, prexel-net management.
- `internal/caddy` — admin API client (UpsertRoute / EnableTLS / cert polling).
- `internal/git` — clone via GitHub App, SSH key, PAT.
- `internal/server` connection test against a real SSH endpoint.

### Manual smoke procedure (release)

Lista enxuta para correr antes de cada tag (`docs/qa-guide.md` cobre o roteiro
completo de 50-75min; este aqui é o mínimo viável para um release):

1. **Install**: provision a fresh Linux VPS, point A-record, run
   `curl -fsSL https://get.prexel.dev | sudo sh`.
2. **Setup wizard**: open `https://<ip>:3000`, create admin (senha forte),
   selecione TLS Let's Encrypt + hostname real.
3. **Deploy**: create a `docker_image` app (`nginx:alpine`, port 80), assign
   domain, click Deploy. Aguarde container `running`.
4. **SSL real**: confirme que `curl https://<hostname>` devolve nginx welcome
   com cert válido (não self-signed).
5. **Rollback manual**: faça um 2º deploy, depois `prexel rollback <app>` →
   versão anterior volta em < 30s.
6. **Rollback automático**: crie app com porta errada (health vai falhar) →
   deploy deve abortar e marcar `status=error` sem deixar container
   pendente.
7. **Upgrade idempotente**: rode o install one-liner de novo na mesma VPS →
   binário substitui, dados ficam.

### Manual smoke procedure (dev)

The CI pipeline builds the production Docker image; for local validation:

```bash
cd web && npm run build
docker build -f build/package/Dockerfile -t prexel-prod:latest .

docker run --rm \
  -e PREXEL_SECRET_KEY="$(openssl rand -hex 32)" \
  -e PREXEL_JWT_SECRET="$(openssl rand -hex 32)" \
  -e PREXEL_DATA_DIR=/var/lib/prexel \
  -e PREXEL_PORT=3000 \
  -v /tmp/test-prexel:/var/lib/prexel \
  -p 3001:3000 \
  prexel-prod:latest serve

# In another terminal:
curl -k https://localhost:3001/api/v1/healthz    # {"status":"ok"}
curl -k https://localhost:3001/                  # Vue SPA index.html
curl -k https://localhost:3001/setup             # SPA, served by go:embed
```

## CI

`.github/workflows/ci.yml` runs on every push/PR:

- `go-lint` — `golangci-lint run`
- `go-test` — `go test -race -cover ./...`
- `web-build` — `npm ci && npm run build`
- `web-test` — `npm run test`
- `docker-build` — `docker build -f build/package/Dockerfile .`

`.github/workflows/release.yml` runs on `v*` tags via GoReleaser, publishing
`prexel_<version>_<os>_<arch>.tar.gz` archives plus checksums to GitHub Releases.
