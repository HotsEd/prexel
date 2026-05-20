# Release process

Prexel releases a single binary for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
Same binary runs as the server (`prexel serve`) on a VPS or as the CLI client on a developer's
laptop. The Vue 3 frontend is embedded via `go:embed web/dist/` so there's nothing extra to ship.

## Pre-flight checklist

Before tagging a release, all of these must be green:

- [ ] `make test` passes locally (or CI green on `main`).
- [ ] `make lint` passes (`golangci-lint run`).
- [ ] `cd web && npm test && npm run build` succeed.
- [ ] Manual smoke pass from `docs/testing.md §Manual smoke procedure (release)`
      against a throwaway VPS.
- [ ] `CHANGELOG.md` updated with the new version + dated entry (added/changed/
      fixed/removed). If a CHANGELOG file doesn't exist yet, the GitHub Release
      notes act as canonical changelog for v0.1.
- [ ] Version bump verified — Prexel doesn't keep the version in source. The
      tag itself (`v0.1.0`) is baked into the binary at link time via
      `-X github.com/prexel/prexel/internal/cli/command.Version={{.Version}}`
      (see `build/package/goreleaser.yaml`). The default fallback string is in
      `internal/cli/command/version.go` (`var Version = "0.1.0-dev"`) — only
      bump it when the dev-build identifier should advance (e.g. moving from
      `0.1.0-dev` to `0.2.0-dev` after cutting v0.1.0).

## One-time setup

- GitHub repo: `prexel/prexel`.
- Homebrew tap repo: `prexel/homebrew-tap` (referenced by `build/package/goreleaser.yaml`). The
  brew formula is generated but `skip_upload: true` until the tap repo exists — flip the flag in
  goreleaser.yaml to start publishing.
- `get.prexel.dev` — static redirect or page that fetches `scripts/install.sh` from the latest tag
  (you can serve it from Cloudflare R2 or GitHub Pages).

## Cut a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers `.github/workflows/release.yml`:

1. checks out the repo,
2. installs Go 1.25 and Node 20,
3. runs `npm ci && npm run build` in `web/` (populates `web/dist/`),
4. invokes `goreleaser/goreleaser-action@v6` against `build/package/goreleaser.yaml`,
5. GoReleaser builds the four (`linux,darwin` × `amd64,arm64`) binaries with the version baked in
   via `-X github.com/prexel/prexel/internal/cli/command.Version={{.Version}}`,
6. archives each as `prexel_<version>_<os>_<arch>.tar.gz` containing the binary plus
   `README.md`, `LICENSE`, `init/prexel.service`, `configs/prexel.env.example`, `scripts/install.sh`,
7. uploads a draft GitHub Release with the archives + `checksums.txt`.

Review the draft release on GitHub, edit the changelog if needed, and publish.

## Verifying the production image locally

Before tagging, smoke-build the production Docker image to confirm the multi-stage build still
produces a small image:

```bash
cd web && npm run build
docker build -f build/package/Dockerfile -t prexel-prod:latest .
docker images prexel-prod:latest
# REPOSITORY    TAG     IMAGE ID       CREATED         SIZE
# prexel-prod   latest  …              <now>           ~73MB
```

Then smoke-run it (no Docker socket — `prexel serve` happily boots without one until you try to
deploy):

```bash
mkdir -p /tmp/test-prexel
docker run --rm \
  -e PREXEL_SECRET_KEY="$(openssl rand -hex 32)" \
  -e PREXEL_JWT_SECRET="$(openssl rand -hex 32)" \
  -e PREXEL_DATA_DIR=/var/lib/prexel \
  -e PREXEL_PORT=3000 \
  -v /tmp/test-prexel:/var/lib/prexel \
  -p 3001:3000 \
  prexel-prod:latest serve

# Verify:
curl -k https://localhost:3001/api/v1/healthz   # {"status":"ok"}
curl -k https://localhost:3001/                 # <!DOCTYPE html>... (SPA)
curl -k https://localhost:3001/setup            # same SPA HTML (history fallback)
```

Image expected size is well under 100MB because the runtime stage is `distroless/base-debian12:nonroot`.

## Distribution paths

| Channel       | How it works                                                      |
|---------------|-------------------------------------------------------------------|
| Install script| `curl -fsSL https://get.prexel.dev \| sh` — `scripts/install.sh`. |
| Direct binary | Download `prexel_<version>_<os>_<arch>.tar.gz` from GitHub Releases. |
| Homebrew      | `brew install prexel/tap/prexel` once `prexel/homebrew-tap` is published. |

## Upgrade behaviour

`install.sh` is idempotent. Re-running on an existing install:

- preserves `/etc/prexel/prexel.env` (secrets stay put),
- preserves `/var/lib/prexel/` (DB, encrypted secrets, deploy logs),
- replaces `/usr/local/bin/prexel` with the new version,
- replaces the systemd unit if a newer one ships in the tarball,
- runs `systemctl restart prexel`.

## Rollback

If a release misbehaves, install a previous version explicitly:

```bash
PREXEL_VERSION=v0.0.9 curl -fsSL https://get.prexel.dev | sudo sh
```

DB migrations only move forward in v0.1; if a future release ships a `down.sql` the install script
will need a `--no-migrate` escape hatch.

## Homebrew tap

The Homebrew formula is generated by GoReleaser but **not uploaded yet** —
`brews:` em `build/package/goreleaser.yaml` está com `skip_upload: true` até o
repo `prexel/homebrew-tap` existir. Para ligar:

1. Crie `github.com/prexel/homebrew-tap` (público, vazio).
2. Gere um PAT com `repo` scope (ou use o GitHub App da org com Contents:
   write nesse repo) e exporte como `HOMEBREW_TAP_GITHUB_TOKEN` no env do job
   `release` em `.github/workflows/release.yml`.
3. Flip `skip_upload: false` em `goreleaser.yaml`.
4. A próxima tag publica `Formula/prexel.rb` automaticamente. Usuários instalam
   com `brew install prexel/tap/prexel`.

## Anúncio

Template curto para release notes / Discord / Twitter:

```
prexel v0.1.0 — <one-line summary>

Highlights:
- <feature 1>
- <feature 2>
- <breaking change ou nota de upgrade, se houver>

Install: curl -fsSL https://get.prexel.dev | sudo sh
Changelog: https://github.com/prexel/prexel/releases/tag/v0.1.0
```

## Hotfix path

Bug crítico em produção que precisa sair fora do ciclo normal:

1. Branch de `main` (ou da tag em apuro): `git checkout -b hotfix/v0.1.1 v0.1.0`.
2. Aplique o fix mínimo. Não inclua features novas — patch é só bug.
3. Adicione teste de regressão (mesmo que rápido) e atualize `CHANGELOG.md`.
4. Rode o pre-flight (smoke incluído) — pular smoke num hotfix é como o bug
   chegou em prod em primeiro lugar.
5. Merge no `main` (PR pequeno + review).
6. Tag patch: `git tag v0.1.1 && git push origin v0.1.1` → GoReleaser publica.
7. Verifique o release no GitHub e teste o one-liner num VPS limpo.
8. Avisa quem precisa fazer `PREXEL_VERSION=v0.1.1 curl ... | sudo sh`.

Se o hotfix afetar dados (migration nova, formato de secret), documente o
upgrade path no anúncio — não é hotfix se quebra rollback.
