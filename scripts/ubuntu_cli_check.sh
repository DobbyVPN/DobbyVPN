#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-50151}"
CONFIG_ARG="${DOBBYVPN_CLI_TEST_CONFIG:-}"
BRANCH_ARG="${DOBBYVPN_ARTIFACT_BRANCH:-}"
REPOSITORY="${DOBBYVPN_GITHUB_REPOSITORY:-DobbyVPN/DobbyVPN}"
WORKFLOW="release.yml"
ARTIFACTS_DIR="$ROOT_DIR/.local-artifacts/ubuntu"
SERVICE_PID=""
CLI_PATH=""

usage() {
  cat <<USAGE
Usage:
  scripts/ubuntu_cli_check.sh --config <url-or-toml-file>

Options:
  --config <value>   Config URL, local TOML file, or inline TOML.
  --port <port>      gRPC VPN service port. Default: ${PORT}
  --branch <branch>  Artifact branch. Default: current git branch, or main.
  -h, --help         Show this help.
USAGE
}

log() {
  printf '[+] %s\n' "$*"
}

die() {
  printf '[!] %s\n' "$*" >&2
  exit 1
}

require_linux() {
  [[ "$(uname -s)" == "Linux" ]] || die "This script is intended for Ubuntu/Linux"
}

require_gh() {
  command -v gh >/dev/null 2>&1 || die "GitHub CLI (gh) is required to download artifacts"
}

github_repo() {
  local remote

  remote="$(git -C "$ROOT_DIR" remote get-url origin 2>/dev/null || true)"
  remote="${remote%.git}"
  case "$remote" in
    git@github.com:*) printf '%s' "${remote#git@github.com:}"; return ;;
    https://github.com/*) printf '%s' "${remote#https://github.com/}"; return ;;
    http://github.com/*) printf '%s' "${remote#http://github.com/}"; return ;;
  esac

  printf '%s' "$REPOSITORY"
}

artifact_branch() {
  local branch

  if [[ -n "$BRANCH_ARG" ]]; then
    printf '%s' "$BRANCH_ARG"
    return
  fi

  branch="$(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [[ -n "$branch" && "$branch" != "HEAD" ]]; then
    printf '%s' "$branch"
    return
  fi

  printf 'main'
}

download_artifacts() {
  local repo branch run_id

  require_gh
  repo="$(github_repo)"
  branch="$(artifact_branch)"

  log "Finding latest successful ${WORKFLOW} run for ${repo}:${branch}"
  run_id="$(gh -R "$repo" run list \
    --workflow "$WORKFLOW" \
    --branch "$branch" \
    --status success \
    --limit 1 \
    --json databaseId \
    --jq '.[0].databaseId // empty')"
  if [[ -z "$run_id" && "$branch" != "main" ]]; then
    log "No successful ${WORKFLOW} run for ${branch}; falling back to main"
    branch="main"
    run_id="$(gh -R "$repo" run list \
      --workflow "$WORKFLOW" \
      --branch "$branch" \
      --status success \
      --limit 1 \
      --json databaseId \
      --jq '.[0].databaseId // empty')"
  fi
  [[ -n "$run_id" ]] || die "No successful ${WORKFLOW} run found for ${repo}:${branch}"

  rm -rf "$ARTIFACTS_DIR"
  mkdir -p "$ARTIFACTS_DIR/app" "$ARTIFACTS_DIR/service"
  gh -R "$repo" run download "$run_id" --name dobbyVPN-linux.deb --dir "$ARTIFACTS_DIR/app"
  gh -R "$repo" run download "$run_id" --name ubuntu_grpcvpnserver --dir "$ARTIFACTS_DIR/service"
}

config_arg() {
  if [[ "$CONFIG_ARG" =~ ^https?:// || -f "$CONFIG_ARG" ]]; then
    printf '%s' "$CONFIG_ARG"
    return
  fi

  printf '%s' "$CONFIG_ARG" > "$ARTIFACTS_DIR/cli-test-config.toml"
  printf '%s' "$ARTIFACTS_DIR/cli-test-config.toml"
}

prepare_cli() {
  local deb="$ARTIFACTS_DIR/app/dobbyVPN-linux.deb"

  [[ -f "$deb" ]] || die "Missing downloaded app artifact: $deb"
  log "Installing downloaded desktop app"
  sudo apt-get install -y "$deb"

  CLI_PATH="$(command -v dobby-vpn || true)"
  if [[ -z "$CLI_PATH" ]]; then
    CLI_PATH="$(
      find /opt /usr/local/bin /usr/bin \
        -type f \
        \( -name 'dobby-vpn' -o -name 'Dobby Vpn' \) \
        -perm -111 2>/dev/null | head -n 1
    )"
  fi
  [[ -n "$CLI_PATH" ]] || die "Dobby CLI executable was not found"
}

cleanup() {
  if [[ -n "$SERVICE_PID" ]] && kill -0 "$SERVICE_PID" 2>/dev/null; then
    log "Stopping gRPC VPN service"
    sudo kill "$SERVICE_PID" 2>/dev/null || kill "$SERVICE_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

start_service() {
  local service="$ARTIFACTS_DIR/service/ubuntu_grpcvpnserver"

  [[ -f "$service" ]] || die "Missing downloaded service artifact: $service"
  chmod +x "$service"

  log "Starting gRPC VPN service on port ${PORT}"
  sudo "$service" -port "$PORT" > "$ROOT_DIR/grpcvpnserver.log" 2>&1 &
  SERVICE_PID="$!"

  for _ in {1..30}; do
    if bash -c ":</dev/tcp/127.0.0.1/${PORT}" 2>/dev/null; then
      log "gRPC VPN service is ready"
      return
    fi
    sleep 1
  done

  cat "$ROOT_DIR/grpcvpnserver.log" >&2 || true
  die "gRPC VPN service did not start on port ${PORT}"
}

run_check() {
  [[ -x "$CLI_PATH" ]] || die "Dobby CLI executable is not ready"
  log "Running CLI config check"
  PORT="$PORT" "$CLI_PATH" check-config "$(config_arg)"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) CONFIG_ARG="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --branch) BRANCH_ARG="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "Unknown argument: $1" ;;
  esac
done

[[ -n "$CONFIG_ARG" ]] || die "Pass --config <url-or-file> or set DOBBYVPN_CLI_TEST_CONFIG"

require_linux
download_artifacts
prepare_cli
start_service
run_check
log "Done"
