#!/usr/bin/env bash
# Prexel installer — production one-liner.
#
# Usage:
#   curl -fsSL https://get.prexel.dev | sh
#
# Environment overrides:
#   PREXEL_VERSION   release tag to install (default: latest)
#   PREXEL_REPO      GitHub repo slug (default: prexel/prexel)
#   PREXEL_PORT      HTTPS port (default: 3000)
#
# Behaviour:
#   - Detects OS (linux/darwin) and arch (amd64/arm64).
#   - Verifies deps: curl, tar, systemctl (linux), docker (warn if absent).
#   - Creates the dedicated system user `prexel` (UID < 1000, no shell).
#   - On fresh installs:
#       * Creates /etc/prexel (0750, root:prexel) with prexel.env (0640).
#       * Generates PREXEL_SECRET_KEY and PREXEL_JWT_SECRET via openssl rand -hex 32.
#       * Creates /var/lib/prexel (0700, prexel:prexel) and /var/log/prexel (0700).
#       * Downloads /usr/local/bin/prexel from the release tarball.
#       * SHA256-verifies the tarball against the release `checksums.txt`.
#       * Installs and starts the systemd unit.
#   - On existing installs: preserves DB + secrets, replaces the binary, restarts.
#       * The systemd unit, if previously installed, is backed up before being
#         overwritten so local customisations are recoverable.
#   - Idempotent: re-running does not destroy state, and re-applies the canonical
#     permissions on /etc/prexel, /var/lib/prexel, /var/log/prexel and prexel.env.

set -euo pipefail

PREXEL_VERSION="${PREXEL_VERSION:-latest}"
PREXEL_REPO="${PREXEL_REPO:-prexel/prexel}"
PREXEL_BIN="/usr/local/bin/prexel"
PREXEL_ETC="/etc/prexel"
PREXEL_DATA="/var/lib/prexel"
PREXEL_LOG="/var/log/prexel"
PREXEL_UNIT="/etc/systemd/system/prexel.service"
PREXEL_ENV_FILE="${PREXEL_ETC}/prexel.env"
PREXEL_USER="prexel"
PREXEL_GROUP="prexel"

# Populated by download_binary; cleaned up by the EXIT trap installed in main().
TMPDIR_INSTALL=""

err() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarn:\033[0m %s\n' "$*" >&2; }

cleanup() {
    if [[ -n "$TMPDIR_INSTALL" && -d "$TMPDIR_INSTALL" ]]; then
        rm -rf "$TMPDIR_INSTALL"
    fi
}

require_root() {
    if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        err "must run as root (try: sudo $0)"
    fi
}

detect_os() {
    case "$(uname -s)" in
        Linux)   echo linux ;;
        Darwin)  echo darwin ;;
        *) err "unsupported OS: $(uname -s)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo amd64 ;;
        aarch64|arm64)  echo arm64 ;;
        *) err "unsupported architecture: $(uname -m)" ;;
    esac
}

# detect_sha_tool prints the command we should use to verify SHA256 sums.
# Linux ships `sha256sum` via coreutils; macOS only has `shasum -a 256`.
detect_sha_tool() {
    if command -v sha256sum >/dev/null 2>&1; then
        echo "sha256sum"
    elif command -v shasum >/dev/null 2>&1; then
        echo "shasum"
    else
        echo ""
    fi
}

check_deps() {
    for bin in curl tar openssl; do
        command -v "$bin" >/dev/null 2>&1 || err "missing dependency: $bin"
    done
    if [[ -z "$(detect_sha_tool)" ]]; then
        err "missing dependency: need sha256sum or shasum to verify the release"
    fi
    if [[ "$(detect_os)" == "linux" ]]; then
        command -v systemctl >/dev/null 2>&1 \
            || err "systemd is required (systemctl not found)"
        command -v useradd >/dev/null 2>&1 \
            || err "useradd is required to create the prexel system user"
    fi
    if ! command -v docker >/dev/null 2>&1; then
        warn "docker not detected — Prexel requires Docker to deploy apps."
        warn "Install Docker first: https://docs.docker.com/engine/install/"
    fi
}

# ensure_prexel_user creates the unprivileged `prexel` system user that the
# daemon runs as. Adding it to the docker group is what gives the daemon access
# to /var/run/docker.sock without needing root.
ensure_prexel_user() {
    if [[ "$(detect_os)" != "linux" ]]; then
        return
    fi
    if ! getent group "$PREXEL_GROUP" >/dev/null 2>&1; then
        log "creating system group $PREXEL_GROUP"
        groupadd --system "$PREXEL_GROUP"
    fi
    if ! id -u "$PREXEL_USER" >/dev/null 2>&1; then
        log "creating system user $PREXEL_USER"
        useradd --system --no-create-home \
                --home-dir "$PREXEL_DATA" \
                --shell /usr/sbin/nologin \
                --gid "$PREXEL_GROUP" \
                "$PREXEL_USER"
    fi
    if getent group docker >/dev/null 2>&1; then
        if ! id -nG "$PREXEL_USER" 2>/dev/null | tr ' ' '\n' | grep -qx docker; then
            log "adding $PREXEL_USER to the docker group (for /var/run/docker.sock)"
            usermod -aG docker "$PREXEL_USER"
        fi
    else
        warn "docker group not present — install Docker, then re-run this script"
        warn "so the prexel user can be added to the docker group."
    fi
}

# resolve_release_tag prints the actual tag we will fetch. "latest" hits the
# GitHub API redirect; explicit versions are returned verbatim.
resolve_release_tag() {
    if [[ "$PREXEL_VERSION" != "latest" ]]; then
        echo "$PREXEL_VERSION"
        return
    fi
    # Use the redirect from /releases/latest to get the tag without needing jq.
    local url
    url=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
        "https://github.com/${PREXEL_REPO}/releases/latest" 2>/dev/null \
        || true)
    if [[ -z "$url" ]]; then
        err "could not resolve latest release for ${PREXEL_REPO}"
    fi
    echo "${url##*/}"
}

# verify_checksum validates that the given filename in tmp matches the SHA256
# entry from checksums.txt. checksums.txt MUST already be present in tmp.
verify_checksum() {
    local tmp="$1" file="$2"
    local tool
    tool=$(detect_sha_tool)
    case "$tool" in
        sha256sum)
            ( cd "$tmp" && sha256sum --check --ignore-missing checksums.txt ) \
                || err "checksum verification failed for ${file}"
            ;;
        shasum)
            ( cd "$tmp" && shasum -a 256 -c --ignore-missing checksums.txt ) \
                || err "checksum verification failed for ${file}"
            ;;
        *)
            err "no SHA256 tool available to verify ${file}"
            ;;
    esac
}

download_binary() {
    local os arch tag tarball
    os=$(detect_os)
    arch=$(detect_arch)
    tag=$(resolve_release_tag)
    log "installing prexel ${tag} for ${os}/${arch}"

    # Release tarball layout produced by goreleaser:
    #   prexel_<version-without-v>_<os>_<arch>.tar.gz
    local ver_no_v="${tag#v}"
    tarball="prexel_${ver_no_v}_${os}_${arch}.tar.gz"
    local url="https://github.com/${PREXEL_REPO}/releases/download/${tag}/${tarball}"
    local checksum_url="https://github.com/${PREXEL_REPO}/releases/download/${tag}/checksums.txt"

    TMPDIR_INSTALL=$(mktemp -d)
    local tmp="$TMPDIR_INSTALL"

    log "downloading ${url}"
    if ! curl -fsSL -o "${tmp}/${tarball}" "$url"; then
        err "download failed: $url"
    fi

    log "downloading ${checksum_url}"
    if ! curl -fsSL -o "${tmp}/checksums.txt" "$checksum_url"; then
        err "could not download checksums.txt for ${tag} — refusing to install an unverified binary"
    fi

    verify_checksum "$tmp" "$tarball"
    log "checksum verified for ${tarball}"

    tar -xzf "${tmp}/${tarball}" -C "$tmp"
    if [[ ! -x "${tmp}/prexel" ]]; then
        err "tarball did not contain a prexel binary"
    fi
    install -m 0755 "${tmp}/prexel" "$PREXEL_BIN"

    # If the tarball includes the systemd unit, prefer it over an older one,
    # but back up any pre-existing unit so admin customisations are recoverable.
    local src_unit=""
    if [[ -f "${tmp}/init/prexel.service" ]]; then
        src_unit="${tmp}/init/prexel.service"
    elif [[ -f "${tmp}/prexel.service" ]]; then
        src_unit="${tmp}/prexel.service"
    fi
    if [[ -n "$src_unit" ]]; then
        if [[ -f "$PREXEL_UNIT" ]]; then
            local ts backup
            ts=$(date -u +%Y%m%dT%H%M%SZ)
            backup="${PREXEL_UNIT}.bak.${ts}"
            log "backing up existing systemd unit to ${backup}"
            cp -p "$PREXEL_UNIT" "$backup"
        fi
        install -m 0644 "$src_unit" "$PREXEL_UNIT"
    fi
}

# normalize_perms enforces our canonical ownership/mode on the given path.
# It logs a single line whenever it had to change something, so re-runs over a
# correctly-permissioned tree stay quiet.
normalize_perms() {
    local path="$1" owner="$2" mode="$3"
    local current_owner current_mode
    if [[ ! -e "$path" ]]; then
        return
    fi
    current_owner=$(stat -c '%U:%G' "$path" 2>/dev/null || stat -f '%Su:%Sg' "$path" 2>/dev/null || true)
    current_mode=$(stat -c '%a' "$path" 2>/dev/null || stat -f '%Lp' "$path" 2>/dev/null || true)

    if [[ "$current_owner" != "$owner" ]]; then
        log "fixing ownership on $path ($current_owner -> $owner)"
        chown "$owner" "$path"
    fi
    if [[ "$current_mode" != "$mode" ]]; then
        log "fixing mode on $path ($current_mode -> $mode)"
        chmod "$mode" "$path"
    fi
}

# user_group_pair returns "user:group" using the prexel user when available
# (after ensure_prexel_user) and otherwise falls back to root.
user_group_pair() {
    if id -u "$PREXEL_USER" >/dev/null 2>&1; then
        echo "${PREXEL_USER}:${PREXEL_GROUP}"
    else
        echo "root:root"
    fi
}

write_env_file() {
    install -d "$PREXEL_ETC"
    if [[ -f "$PREXEL_ENV_FILE" ]]; then
        log "preserving existing $PREXEL_ENV_FILE"
    else
        log "generating $PREXEL_ENV_FILE"
        local secret jwt
        secret=$(openssl rand -hex 32)
        jwt=$(openssl rand -hex 32)
        umask 0177
        cat > "$PREXEL_ENV_FILE" <<EOF
# Prexel configuration. Regenerated only on first install.
PREXEL_ENV=production
PREXEL_PORT=${PREXEL_PORT:-3000}
PREXEL_DATA_DIR=${PREXEL_DATA}
PREXEL_SECRET_KEY=${secret}
PREXEL_JWT_SECRET=${jwt}

# Optional GitHub App integration (leave empty to use SSH key or PAT auth).
PREXEL_GITHUB_APP_ID=
PREXEL_GITHUB_APP_PRIVATE_KEY=
PREXEL_GITHUB_APP_SLUG=
EOF
    fi

    # /etc/prexel must be readable by the daemon's group so it can pull in the
    # EnvironmentFile, but unreadable to everyone else.
    local etc_owner="root:root"
    local env_owner="root:root"
    if id -u "$PREXEL_USER" >/dev/null 2>&1; then
        etc_owner="root:${PREXEL_GROUP}"
        env_owner="root:${PREXEL_GROUP}"
    fi
    normalize_perms "$PREXEL_ETC" "$etc_owner" "750"
    normalize_perms "$PREXEL_ENV_FILE" "$env_owner" "640"
}

ensure_data_dir() {
    if [[ ! -d "$PREXEL_DATA" ]]; then
        log "creating $PREXEL_DATA"
        install -d -m 0700 "$PREXEL_DATA"
    fi
    normalize_perms "$PREXEL_DATA" "$(user_group_pair)" "700"
}

ensure_log_dir() {
    if [[ ! -d "$PREXEL_LOG" ]]; then
        log "creating $PREXEL_LOG"
        install -d -m 0700 "$PREXEL_LOG"
    fi
    normalize_perms "$PREXEL_LOG" "$(user_group_pair)" "700"
}

ensure_unit() {
    if [[ ! -f "$PREXEL_UNIT" ]]; then
        # Fallback: drop a minimal unit if the tarball didn't ship one.
        log "writing fallback systemd unit at $PREXEL_UNIT"
        cat > "$PREXEL_UNIT" <<'UNIT'
[Unit]
Description=Prexel Server
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
EnvironmentFile=/etc/prexel/prexel.env
ExecStart=/usr/local/bin/prexel serve
Restart=always
RestartSec=5
User=prexel
Group=prexel
SupplementaryGroups=docker
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/prexel /var/log/prexel
WorkingDirectory=/var/lib/prexel

[Install]
WantedBy=multi-user.target
UNIT
        chmod 0644 "$PREXEL_UNIT"
    fi
}

enable_service() {
    if [[ "$(detect_os)" != "linux" ]]; then
        warn "skipping systemd setup on $(detect_os) — start the service manually"
        return
    fi
    log "enabling and starting prexel.service"
    systemctl daemon-reload
    if systemctl is-active --quiet prexel; then
        systemctl restart prexel
    else
        systemctl enable --now prexel
    fi
}

verify_running() {
    if [[ "$(detect_os)" != "linux" ]]; then
        return
    fi
    log "verifying Prexel is responding"
    local port="${PREXEL_PORT:-3000}"
    local cert="${PREXEL_DATA}/tls/server.crt"
    local cert_args=()
    if [[ -f "$cert" ]]; then
        # Pin the certificate and force the connection to loopback so we don't
        # depend on DNS or the cert's SAN matching "127.0.0.1".
        cert_args=(--cacert "$cert" --resolve "localhost:${port}:127.0.0.1")
    else
        warn "TLS cert not found at $cert — falling back to -k (skipping verification)"
        cert_args=(-k --resolve "localhost:${port}:127.0.0.1")
    fi
    local target
    if [[ -f "$cert" ]]; then
        target="https://localhost:${port}/api/v1/healthz"
    else
        target="https://127.0.0.1:${port}/api/v1/healthz"
    fi
    local i
    for i in $(seq 1 10); do
        if curl -fsS "${cert_args[@]}" "$target" >/dev/null 2>&1; then
            log "Prexel is up on port ${port}"
            return
        fi
        sleep 1
    done
    warn "health check did not respond within 10s — inspect logs with:"
    warn "    journalctl -u prexel -f"
}

print_next_steps() {
    local host_ip
    host_ip=$(hostname -I 2>/dev/null | tr ' ' '\n' \
        | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -1)
    [[ -z "$host_ip" ]] && host_ip="IP_PUBLICO"
    cat <<EOF

✓ Prexel installed.
  Acesse https://${host_ip}:${PREXEL_PORT:-3000}
  para completar o setup wizard.

  • Configuração: $PREXEL_ENV_FILE
  • Binário: $PREXEL_BIN
  • Dados: $PREXEL_DATA
  • Logs: journalctl -u prexel -f
EOF
}

main() {
    trap cleanup EXIT
    require_root
    check_deps
    ensure_prexel_user
    write_env_file
    ensure_data_dir
    ensure_log_dir
    download_binary
    ensure_unit
    enable_service
    verify_running
    print_next_steps
}

main "$@"
