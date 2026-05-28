#!/usr/bin/env bash
# ongrid install.sh - first-install or re-install on top of existing.
# Runs from inside the extracted tarball on the target VPS.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$SCRIPT_DIR"

# ---------- colors (only if TTY) ----------
if [[ -t 1 ]]; then
    C_RED=$'\033[0;31m'
    C_GREEN=$'\033[0;32m'
    C_YELLOW=$'\033[1;33m'
    C_CYAN=$'\033[0;36m'
    C_BOLD=$'\033[1m'
    C_RESET=$'\033[0m'
else
    C_RED=''; C_GREEN=''; C_YELLOW=''; C_CYAN=''; C_BOLD=''; C_RESET=''
fi

log_info()  { printf '%s[INFO]%s %s\n'  "$C_GREEN"  "$C_RESET" "$*"; }
log_warn()  { printf '%s[WARN]%s %s\n'  "$C_YELLOW" "$C_RESET" "$*"; }
log_error() { printf '%s[ERROR]%s %s\n' "$C_RED"    "$C_RESET" "$*" >&2; }

on_error() {
    local exit_code=$?
    log_error "install failed at line $1 (exit $exit_code)"
    log_error "check output above; fix and re-run sudo ./install.sh"
}
trap 'on_error $LINENO' ERR

# ---------- flags ----------
PROFILE_MONITORING=0
NO_SEED=0
FORCE=0
MODE="compose"   # compose (default) | systemd

usage() {
    cat <<EOF
Usage: sudo ./install.sh [OPTIONS]

Options:
  --mode <compose|systemd>
                         Install topology. compose (default) runs the full
                         stack via docker-compose. systemd installs the
                         manager + frontier + deps as native systemd units
                         (no docker; see systemd/install-systemd.sh for the
                         long-form trip report).
  --profile monitoring   compose-only. Also start Prometheus
                         (docker compose --profile monitoring).
  --no-seed              compose-only. Skip the admin-bootstrap user notice.
  --force                compose-only. Reinstall on top of existing
                         (preserves .env / data volume).
  --with-deps            systemd-only. Auto-install mariadb / nginx /
                         grafana via apt/dnf and download pinned
                         prom / loki / tempo / qdrant binaries from
                         upstream releases with sha256 verify.
  -h, --help             Print this help.
EOF
}

# Mode-pre-scan: spot --mode before the main parse loop so we can
# strip it out and pass every remaining flag verbatim to the systemd
# dispatcher. Without this, --with-deps (only known to install-systemd.sh)
# would hit the compose-side parser's "unknown flag" arm and exit.
PASSTHROUGH_ARGS=()
i=0
while [[ $i -lt $# ]]; do
    arg="${@:i+1:1}"
    case "$arg" in
        --mode) MODE="${@:i+2:1}"; i=$((i+2)) ;;
        --mode=*) MODE="${arg#*=}"; i=$((i+1)) ;;
        *) PASSTHROUGH_ARGS+=("$arg"); i=$((i+1)) ;;
    esac
done

case "$MODE" in
    compose) ;;   # fall through to legacy parse + install
    systemd)
        if [[ ! -x "$SCRIPT_DIR/systemd/install-systemd.sh" ]]; then
            log_error "systemd installer missing or not executable: $SCRIPT_DIR/systemd/install-systemd.sh"
            exit 2
        fi
        log_info "dispatching to systemd installer"
        exec bash "$SCRIPT_DIR/systemd/install-systemd.sh" "${PASSTHROUGH_ARGS[@]+"${PASSTHROUGH_ARGS[@]}"}"
        ;;
    *)
        log_error "--mode must be one of: compose, systemd"
        exit 2
        ;;
esac

# -- compose-mode parse loop (only reached when MODE=compose) --
set -- "${PASSTHROUGH_ARGS[@]+"${PASSTHROUGH_ARGS[@]}"}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --profile)
            if [[ "${2:-}" == "monitoring" ]]; then
                PROFILE_MONITORING=1; shift 2
            else
                log_error "only --profile monitoring is supported"; exit 2
            fi
            ;;
        --profile=monitoring) PROFILE_MONITORING=1; shift ;;
        --no-seed) NO_SEED=1; shift ;;
        --force)   FORCE=1;   shift ;;
        -h|--help) usage; exit 0 ;;
        *) log_error "unknown flag: $1"; usage; exit 2 ;;
    esac
done

# ---------- re-exec via sudo if not root ----------
if [[ $EUID -ne 0 ]]; then
    log_warn "not running as root; re-executing via sudo"
    exec sudo -E bash "$0" "$@"
fi

# ---------- preflight ----------
log_info "preflight checks"
command -v docker >/dev/null 2>&1 || { log_error "docker CLI not found; install docker >= 24"; exit 1; }
docker info >/dev/null 2>&1 || { log_error "docker daemon not reachable (permission or not running)"; exit 1; }
docker compose version >/dev/null 2>&1 || { log_error "docker compose v2 not found (need the 'docker compose' subcommand)"; exit 1; }

# ---------- install dir ----------
INSTALL_DIR="${ONGRID_INSTALL_DIR:-/opt/ongrid}"
log_info "install dir: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"

# ---------- copy assets ----------
log_info "copying assets into $INSTALL_DIR"
cp -f "$SCRIPT_DIR/docker-compose.yml" "$INSTALL_DIR/docker-compose.yml"
cp -f "$SCRIPT_DIR/.env.example"       "$INSTALL_DIR/.env.example"
if [[ -f "$SCRIPT_DIR/frontier.yaml" ]]; then
    cp -f "$SCRIPT_DIR/frontier.yaml" "$INSTALL_DIR/frontier.yaml"
fi
if [[ -f "$SCRIPT_DIR/VERSION" ]]; then
    cp -f "$SCRIPT_DIR/VERSION" "$INSTALL_DIR/VERSION"
fi
# ADR-009: cloud-side Prometheus is now a core service. The new compose
# bind-mounts ./prometheus.yml flat into the container at /etc/prometheus/.
# We still copy the legacy prometheus/ subdir if present for backwards
# compatibility with installs that pre-date ADR-009.
if [[ -f "$SCRIPT_DIR/prometheus.yml" ]]; then
    cp -f "$SCRIPT_DIR/prometheus.yml" "$INSTALL_DIR/prometheus.yml"
fi
# ADR-026 self-obs alert rules — bind-mounted alongside prometheus.yml.
if [[ -f "$SCRIPT_DIR/prometheus-rules.yml" ]]; then
    cp -f "$SCRIPT_DIR/prometheus-rules.yml" "$INSTALL_DIR/prometheus-rules.yml"
fi
if [[ -d "$SCRIPT_DIR/prometheus" ]]; then
    mkdir -p "$INSTALL_DIR/prometheus"
    cp -rf "$SCRIPT_DIR/prometheus/." "$INSTALL_DIR/prometheus/"
fi
# Loki config (ADR-012) bind-mounted by docker-compose at ./loki-config.yaml.
# Skipping the copy on a fresh install would crash the loki container — it
# tries to read /etc/loki/local-config.yaml which docker would create as an
# empty directory at the bind-mount source.
if [[ -f "$SCRIPT_DIR/loki-config.yaml" ]]; then
    cp -f "$SCRIPT_DIR/loki-config.yaml" "$INSTALL_DIR/loki-config.yaml"
fi
# Tempo config (ADR-013) — same rationale as loki-config.yaml above.
if [[ -f "$SCRIPT_DIR/tempo-config.yaml" ]]; then
    cp -f "$SCRIPT_DIR/tempo-config.yaml" "$INSTALL_DIR/tempo-config.yaml"
fi
if [[ -d "$SCRIPT_DIR/grafana" ]]; then
    mkdir -p "$INSTALL_DIR/grafana"
    cp -rf "$SCRIPT_DIR/grafana/." "$INSTALL_DIR/grafana/"
fi
# SearXNG settings (default backend for the web_search skill). The compose
# bind-mounts ./searxng into /etc/searxng inside the container.
if [[ -d "$SCRIPT_DIR/searxng" ]]; then
    mkdir -p "$INSTALL_DIR/searxng"
    cp -rf "$SCRIPT_DIR/searxng/." "$INSTALL_DIR/searxng/"
fi
if [[ -d "$SCRIPT_DIR/edge" ]]; then
    mkdir -p "$INSTALL_DIR/edge"
    cp -rf "$SCRIPT_DIR/edge/." "$INSTALL_DIR/edge/"
    find "$INSTALL_DIR/edge" -maxdepth 1 -name '*.sh' -exec chmod 755 {} \;
    # Rebuild the ADR-024 one-button upgrade bundle from the loose edge
    # binaries we just staged. The release tarball no longer double-packs a
    # pre-built copy (it duplicated these same binaries at ~120 MB of
    # incompressible payload); nginx serves the reassembled file from /edge/
    # unchanged. Best-effort: a failure here only disables one-button edge
    # upgrade until the next install, so warn and continue.
    _edge_ver=$(tr -d '[:space:]' < "$SCRIPT_DIR/VERSION" 2>/dev/null || true)
    if [[ -x "$INSTALL_DIR/edge/build-edge-bundle.sh" && -n "$_edge_ver" ]]; then
        "$INSTALL_DIR/edge/build-edge-bundle.sh" "$INSTALL_DIR/edge" "$_edge_ver" linux-amd64 \
            || log_warn "edge upgrade bundle rebuild failed; one-button edge upgrade disabled until next install"
    fi
fi

# ---------- host data dirs (bind-mount targets) ----------
# All stateful services bind-mount to host paths instead of docker named
# volumes. Operators can back up / inspect / replace files without docker
# gymnastics, and the storage can be redirected at a customer filesystem
# (NFS / iSCSI / NVMe) by overriding ONGRID_DATA_DIR and ONGRID_LOG_DIR.
# We chown each subdir to the uid the container image runs as — missing
# this on first boot makes prom/loki/tempo/grafana crash with "permission
# denied on /<datadir>".
ONGRID_DATA_DIR="${ONGRID_DATA_DIR:-/var/lib/ongrid}"
ONGRID_LOG_DIR="${ONGRID_LOG_DIR:-/var/log/ongrid}"
log_info "data dir: $ONGRID_DATA_DIR  (override via ONGRID_DATA_DIR)"
log_info "log dir:  $ONGRID_LOG_DIR  (override via ONGRID_LOG_DIR)"

# Warn the operator if legacy docker named volumes from pre-bind-mount
# installs are still around — they're orphaned now and contain the live
# data until they run the migration in README.md "数据卷迁移".
LEGACY_VOLS=()
# Cover both the bare names AND the docker-compose project-prefixed
# variants — see comment in upgrade.sh.
for v in \
    ongrid_ongrid_mysql_data ongrid_mysql_data mysql_data \
    ongrid_ongrid_logs ongrid_logs \
    ongrid_prometheus_data prometheus_data \
    ongrid_grafana_data grafana_data \
    ongrid_loki_data loki_data \
    ongrid_tempo_data tempo_data \
    ongrid_qdrant_data qdrant_data; do
    if docker volume inspect "$v" >/dev/null 2>&1; then
        LEGACY_VOLS+=("$v")
    fi
done
if (( ${#LEGACY_VOLS[@]} > 0 )); then
    log_warn "legacy docker volumes detected (data NOT auto-migrated): ${LEGACY_VOLS[*]}"
    log_warn "see README.md '数据卷迁移' to copy data into $ONGRID_DATA_DIR before bringing the stack up"
fi

mkdir -p \
    "$ONGRID_DATA_DIR/mysql" \
    "$ONGRID_DATA_DIR/prometheus" \
    "$ONGRID_DATA_DIR/loki" \
    "$ONGRID_DATA_DIR/tempo" \
    "$ONGRID_DATA_DIR/qdrant" \
    "$ONGRID_DATA_DIR/grafana" \
    "$ONGRID_DATA_DIR/embeddings" \
    "$ONGRID_LOG_DIR"

# Stage the bundled fastembed model (ADR-027 Phase-2 offline RAG).
# Skip if operator already has files in there (e.g. a custom model).
if [[ -d "$SCRIPT_DIR/embeddings/fast-bge-small-zh-v1.5" ]]; then
    target="$ONGRID_DATA_DIR/embeddings/fast-bge-small-zh-v1.5"
    if [[ -f "$target/model_optimized.onnx" ]]; then
        log_info "embedding model already staged ($target)"
    else
        log_info "staging bundled embedding model → $target"
        mkdir -p "$target"
        cp -rf "$SCRIPT_DIR/embeddings/fast-bge-small-zh-v1.5/." "$target/"
    fi
fi
# Manager runs as uid 65532 (nonroot in the image); the embedding
# cache must be readable by that uid AND writable so fastembed-go can
# write its own progress / lock files on first load.
chmod -R 0755 "$ONGRID_DATA_DIR/embeddings" 2>/dev/null || true
chown -R 65532:65532 "$ONGRID_DATA_DIR/embeddings" 2>/dev/null || true

# Image uids — pinned to what the upstream images run as. Bumping the
# image tag in docker-compose.yml without updating these here will fail
# on first boot (chown to the wrong uid → service can't write).
chown -R 999:999       "$ONGRID_DATA_DIR/mysql"      2>/dev/null || true   # mysql:8.0
chown -R 65534:65534   "$ONGRID_DATA_DIR/prometheus" 2>/dev/null || true   # prom/prometheus runs as nobody
chown -R 10001:10001   "$ONGRID_DATA_DIR/loki"       2>/dev/null || true   # grafana/loki
chown -R 10001:10001   "$ONGRID_DATA_DIR/tempo"      2>/dev/null || true   # grafana/tempo
chown -R 472:472       "$ONGRID_DATA_DIR/grafana"    2>/dev/null || true   # grafana/grafana-oss
# qdrant runs as root inside the container — no chown needed.
# manager log dir: container's ongrid user writes here.
chmod 755 "$ONGRID_DATA_DIR" "$ONGRID_LOG_DIR"

# Export so the docker compose subprocess inherits — compose substitutes
# ${ONGRID_DATA_DIR:-...} into the bind paths at up time.
export ONGRID_DATA_DIR ONGRID_LOG_DIR

# ---------- nginx config + TLS certs (ADR-008) ----------
# nginx.conf is bind-mounted into the nginx container; certs/ holds the
# TLS material. install.sh always refreshes nginx.conf from the tarball
# but never overwrites operator-provided certs.
if [[ -f "$SCRIPT_DIR/nginx.conf" ]]; then
    cp -f "$SCRIPT_DIR/nginx.conf" "$INSTALL_DIR/nginx.conf"
fi

mkdir -p "$INSTALL_DIR/certs"
chmod 700 "$INSTALL_DIR/certs"
if [[ ! -f "$INSTALL_DIR/certs/tls.crt" || ! -f "$INSTALL_DIR/certs/tls.key" ]]; then
    log_info "generating self-signed TLS cert (valid 365d, CN=ongrid)"
    command -v openssl >/dev/null 2>&1 || {
        log_error "openssl not found; cannot generate self-signed cert"
        log_error "install openssl, or drop tls.crt + tls.key into $INSTALL_DIR/certs/ and re-run"
        exit 1
    }
    openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
        -subj "/CN=ongrid" \
        -keyout "$INSTALL_DIR/certs/tls.key" \
        -out    "$INSTALL_DIR/certs/tls.crt" \
        -addext "subjectAltName = DNS:ongrid,DNS:localhost,IP:127.0.0.1" \
        2>/dev/null
    chmod 600 "$INSTALL_DIR/certs/tls.key"
    chmod 644 "$INSTALL_DIR/certs/tls.crt"
    log_warn "self-signed cert: browsers will warn — replace with a real cert in $INSTALL_DIR/certs/ later"
fi

# ---------- load docker images ----------
# Both ongrid (manager) and frontier (broker) images ship in the tarball.
# Docker Hub pull is unreliable in some networks, so installers should not
# depend on it. ADR-007 explains the upstream-frontier-shipped-locally choice.
if [[ -f "$SCRIPT_DIR/images/ongrid.tar" ]]; then
    log_info "loading ongrid image (docker load)"
    docker load -i "$SCRIPT_DIR/images/ongrid.tar"
else
    log_warn "images/ongrid.tar not found; assuming image already present"
fi
if [[ -f "$SCRIPT_DIR/images/frontier.tar" ]]; then
    log_info "loading frontier broker image (docker load)"
    docker load -i "$SCRIPT_DIR/images/frontier.tar"
else
    log_warn "images/frontier.tar not found; assuming image already present"
fi
if [[ -f "$SCRIPT_DIR/images/ongrid-web.tar" ]]; then
    log_info "loading ongrid-web (frontend + nginx) image (docker load)"
    docker load -i "$SCRIPT_DIR/images/ongrid-web.tar"
else
    log_warn "images/ongrid-web.tar not found; assuming ongrid-web image already present"
fi

# Resolve VERSION from VERSION file or .env.example (fallback)
VERSION_FROM_FILE=""
if [[ -f "$INSTALL_DIR/VERSION" ]]; then
    VERSION_FROM_FILE=$(tr -d '[:space:]' < "$INSTALL_DIR/VERSION" || true)
fi
if [[ -z "$VERSION_FROM_FILE" ]]; then
    VERSION_FROM_FILE=$(grep -E '^ONGRID_VERSION=' "$SCRIPT_DIR/.env.example" | cut -d= -f2- | tr -d '[:space:]' || true)
fi
[[ -n "$VERSION_FROM_FILE" ]] || { log_error "cannot determine ongrid version"; exit 1; }

if ! docker image inspect "ongrid:${VERSION_FROM_FILE}" >/dev/null 2>&1; then
    log_error "docker image ongrid:${VERSION_FROM_FILE} not found after load"
    exit 1
fi
log_info "ongrid:${VERSION_FROM_FILE} image ready"

# ---------- secret generator ----------
gen_secret() {
    local len="${1:-24}"
    local out=""
    if command -v openssl >/dev/null 2>&1; then
        out=$(openssl rand -base64 48 | tr -d '=+/\n' | cut -c1-"$len" || true)
    fi
    if [[ -z "$out" ]]; then
        out=$(head -c 64 /dev/urandom | base64 | tr -d '=+/\n' | cut -c1-"$len" || true)
    fi
    if [[ -z "$out" ]]; then
        out=$(hexdump -n 32 -e '"%02x"' /dev/urandom | cut -c1-"$len")
    fi
    printf '%s' "$out"
}

# ---------- .env: create or reuse ----------
GENERATED_ADMIN_PASSWORD=""
ADMIN_PASSWORD_NEWLY_GENERATED=0

if [[ -f "$INSTALL_DIR/.env" && $FORCE -eq 0 ]]; then
    log_info ".env exists; reusing (use --force to re-copy template if needed)"
elif [[ -f "$INSTALL_DIR/.env" && $FORCE -eq 1 ]]; then
    log_info ".env exists; preserving operator customizations (--force does not overwrite .env)"
else
    log_info "creating $INSTALL_DIR/.env from template"
    cp "$SCRIPT_DIR/.env.example" "$INSTALL_DIR/.env"
fi

ENV_FILE="$INSTALL_DIR/.env"
chmod 600 "$ENV_FILE"

# Fill blanks in-place (portable sed: use .bak suffix then rm).
fill_blank() {
    local key="$1" value="$2"
    # Escape replacement for sed (|, \, &)
    local esc
    esc=$(printf '%s' "$value" | sed -e 's/[\\|&]/\\&/g')
    sed -i.bak -E "s|^${key}=[[:space:]]*$|${key}=${esc}|" "$ENV_FILE"
    rm -f "${ENV_FILE}.bak"
}

is_blank() {
    local key="$1"
    grep -E "^${key}=[[:space:]]*$" "$ENV_FILE" >/dev/null 2>&1
}

# MYSQL_ROOT_PASSWORD
if is_blank MYSQL_ROOT_PASSWORD; then
    fill_blank MYSQL_ROOT_PASSWORD "$(gen_secret 24)"
    log_info "generated MYSQL_ROOT_PASSWORD"
fi

# MYSQL_PASSWORD
if is_blank MYSQL_PASSWORD; then
    fill_blank MYSQL_PASSWORD "$(gen_secret 24)"
    log_info "generated MYSQL_PASSWORD"
fi

# ONGRID_JWT_SECRET (64 chars)
if is_blank ONGRID_JWT_SECRET; then
    fill_blank ONGRID_JWT_SECRET "$(gen_secret 64)"
    log_info "generated ONGRID_JWT_SECRET"
fi

# ONGRID_ADMIN_PASSWORD (record it for the final banner)
if is_blank ONGRID_ADMIN_PASSWORD; then
    GENERATED_ADMIN_PASSWORD=$(gen_secret 20)
    fill_blank ONGRID_ADMIN_PASSWORD "$GENERATED_ADMIN_PASSWORD"
    ADMIN_PASSWORD_NEWLY_GENERATED=1
    log_info "generated ONGRID_ADMIN_PASSWORD (printed once in final banner)"
fi

# GRAFANA_ADMIN_PASSWORD (manager bootstraps SA token with this)
if is_blank GRAFANA_ADMIN_PASSWORD; then
    fill_blank GRAFANA_ADMIN_PASSWORD "$(gen_secret 20)"
    log_info "generated GRAFANA_ADMIN_PASSWORD"
fi

# ONGRID_PUBLIC_URL — manager hands this URL to edges as the data-plane
# endpoint for plugin telemetry (logs / traces push). Empty disables
# plugin endpoints, which silently breaks "edge can push logs to Loki"
# on first install. We auto-fill with the operator's host + HTTPS port
# so the common case works out of the box; operators behind a proper
# domain edit .env post-install.
if is_blank ONGRID_PUBLIC_URL; then
    # Detect best host string for the public URL. hostname -f on stock
    # cloud images often returns 'localhost.localdomain' which makes
    # promtail / otelcol push to a black hole. Prefer in order:
    #   1. First non-loopback IPv4 from `hostname -I`
    #   2. `ip route get 8.8.8.8` source IP
    #   3. hostname -f only if it's not localhost*
    #   4. Literal 'localhost' (only as last resort)
    HOST_FOR_URL=""
    if h=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -v '^127\.' | grep -v '^$' | head -1); then
        HOST_FOR_URL="$h"
    fi
    if [[ -z "$HOST_FOR_URL" ]]; then
        HOST_FOR_URL=$(ip route get 8.8.8.8 2>/dev/null | awk '/src/{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' || true)
    fi
    if [[ -z "$HOST_FOR_URL" ]]; then
        fqdn=$(hostname -f 2>/dev/null || true)
        if [[ -n "$fqdn" && "$fqdn" != "localhost.localdomain" && "$fqdn" != "localhost" ]]; then
            HOST_FOR_URL="$fqdn"
        fi
    fi
    [[ -z "$HOST_FOR_URL" ]] && HOST_FOR_URL=localhost

    PORT_FOR_URL=$(grep -E '^ONGRID_HTTP_PORT=' "$ENV_FILE" | cut -d= -f2- || echo 443)
    : "${PORT_FOR_URL:=443}"
    if [[ "$PORT_FOR_URL" == "443" ]]; then
        fill_blank ONGRID_PUBLIC_URL "https://${HOST_FOR_URL}"
    else
        fill_blank ONGRID_PUBLIC_URL "https://${HOST_FOR_URL}:${PORT_FOR_URL}"
    fi
    log_info "auto-set ONGRID_PUBLIC_URL=https://${HOST_FOR_URL}:${PORT_FOR_URL} (edit .env to override)"
fi

# Bump ONGRID_VERSION to match VERSION file.
sed -i.bak -E "s|^ONGRID_VERSION=.*|ONGRID_VERSION=${VERSION_FROM_FILE}|" "$ENV_FILE"
rm -f "${ENV_FILE}.bak"
chmod 600 "$ENV_FILE"

# Load env for later banner use (read-only subset; don't export secrets).
ONGRID_HTTP_PORT=$(grep -E '^ONGRID_HTTP_PORT=' "$ENV_FILE" | cut -d= -f2- || true)
ONGRID_HTTP_REDIRECT_PORT=$(grep -E '^ONGRID_HTTP_REDIRECT_PORT=' "$ENV_FILE" | cut -d= -f2- || true)
ONGRID_TUNNEL_PORT=$(grep -E '^ONGRID_TUNNEL_PORT=' "$ENV_FILE" | cut -d= -f2- || true)
PROM_PORT=$(grep -E '^PROM_PORT=' "$ENV_FILE" | cut -d= -f2- || true)
ADMIN_EMAIL=$(grep -E '^ONGRID_ADMIN_EMAIL=' "$ENV_FILE" | cut -d= -f2- || true)
: "${ONGRID_HTTP_PORT:=443}"
: "${ONGRID_HTTP_REDIRECT_PORT:=80}"
: "${ONGRID_TUNNEL_PORT:=40012}"
: "${PROM_PORT:=9090}"

# ---------- compose up ----------
# No explicit -f so docker-compose.override.yml (if present) auto-loads.
# Naming -f docker-compose.yml disables compose's default override
# discovery; the 2026-05-19 regression silently dropped operator env
# overrides (ONGRID_INVESTIGATOR_ENABLED, custom feature flags).
COMPOSE_ARGS=(--env-file "$ENV_FILE")
if [[ $PROFILE_MONITORING -eq 1 ]]; then
    COMPOSE_ARGS+=(--profile monitoring)
fi

log_info "starting stack: docker compose ${COMPOSE_ARGS[*]} up -d"
(
    cd "$INSTALL_DIR"
    docker compose "${COMPOSE_ARGS[@]}" up -d
)

# ---------- wait for /healthz ----------
# nginx terminates TLS on host port ${ONGRID_HTTP_PORT} (443 by default) and
# proxies /healthz to the manager. -k tolerates the self-signed cert.
log_info "waiting for /healthz on https://localhost:${ONGRID_HTTP_PORT} (up to 60s)"
HEALTH_OK=0
for i in $(seq 1 30); do
    if curl -fsSk "https://localhost:${ONGRID_HTTP_PORT}/healthz" >/dev/null 2>&1; then
        HEALTH_OK=1
        log_info "ongrid is healthy (took ~$((i*2))s)"
        break
    fi
    printf '.'
    sleep 2
done
printf '\n'
if [[ $HEALTH_OK -eq 0 ]]; then
    log_warn "ongrid did not become healthy within 60s"
    log_warn "check logs: docker compose -f $INSTALL_DIR/docker-compose.yml logs ongrid"
    log_warn "             docker compose -f $INSTALL_DIR/docker-compose.yml logs nginx"
fi

# ---------- detect host address for banner ----------
HOST_HINT="$(hostname -f 2>/dev/null || hostname 2>/dev/null || echo localhost)"

# ---------- banner ----------
echo ""
echo "${C_BOLD}${C_CYAN}===============================================================${C_RESET}"
echo "${C_BOLD}${C_GREEN}  ongrid installation complete${C_RESET}"
echo "${C_BOLD}${C_CYAN}===============================================================${C_RESET}"
echo ""
# Compose URLs. Default https:443 case prints clean https://host/; otherwise
# include the explicit :port. Same logic for the optional :80 redirect.
if [[ "$ONGRID_HTTP_PORT" == "443" ]]; then
    WEB_URL="https://${HOST_HINT}/"
    API_URL="https://${HOST_HINT}/api/v1"
else
    WEB_URL="https://${HOST_HINT}:${ONGRID_HTTP_PORT}/"
    API_URL="https://${HOST_HINT}:${ONGRID_HTTP_PORT}/api/v1"
fi

echo "${C_BOLD}Install dir:${C_RESET}     $INSTALL_DIR"
echo "${C_BOLD}Version:${C_RESET}         ${VERSION_FROM_FILE}"
echo ""
echo "${C_BOLD}Web UI:${C_RESET}          ${WEB_URL}"
echo "${C_BOLD}API URL:${C_RESET}         ${API_URL}"
echo "${C_BOLD}Tunnel endpoint:${C_RESET} ${HOST_HINT}:${ONGRID_TUNNEL_PORT}   (for edges)"
if [[ $PROFILE_MONITORING -eq 1 ]]; then
    echo "${C_BOLD}Prometheus:${C_RESET}      http://${HOST_HINT}:${PROM_PORT}"
fi
echo ""
echo "${C_YELLOW}TLS:${C_RESET} self-signed cert in ${INSTALL_DIR}/certs/ — browsers will warn"
echo "      on first visit. Replace tls.crt + tls.key with a real cert and"
echo "      'docker compose -f ${INSTALL_DIR}/docker-compose.yml restart nginx'."
echo ""

if [[ $NO_SEED -eq 0 ]]; then
    echo "${C_BOLD}${C_YELLOW}---------------- bootstrap admin ----------------${C_RESET}"
    echo "${C_BOLD}email:${C_RESET}    ${ADMIN_EMAIL}"
    if [[ $ADMIN_PASSWORD_NEWLY_GENERATED -eq 1 ]]; then
        echo "${C_BOLD}${C_YELLOW}password:${C_RESET} ${C_BOLD}${GENERATED_ADMIN_PASSWORD}${C_RESET}"
        echo ""
        echo "${C_YELLOW}>> Record this password NOW. It will not be shown again.${C_RESET}"
        echo "${C_YELLOW}>> It is stored in ${ENV_FILE} (chmod 600) and seeded on first start.${C_RESET}"
    else
        echo "${C_BOLD}password:${C_RESET} (unchanged; see ${ENV_FILE})"
    fi
    echo "${C_BOLD}${C_YELLOW}-------------------------------------------------${C_RESET}"
    echo ""
fi

echo "${C_BOLD}Next steps:${C_RESET}"
echo "  1. Login test (the -k flag tolerates the self-signed cert):"
echo "       curl -sk -X POST ${API_URL}/auth/login \\"
echo "            -H 'Content-Type: application/json' \\"
echo "            -d '{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"<paste-above>\"}'"
echo "  2. Install edge on a target host:"
echo "       scp -r $INSTALL_DIR/edge user@target:~/ongrid-edge && ssh user@target 'sudo ~/ongrid-edge/install-edge.sh'"
echo "  3. Service management:"
echo "       sudo docker compose -f $INSTALL_DIR/docker-compose.yml logs -f ongrid"
echo "       sudo docker compose -f $INSTALL_DIR/docker-compose.yml restart ongrid"
echo ""
echo "${C_BOLD}${C_CYAN}===============================================================${C_RESET}"
