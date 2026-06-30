#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-50151}"
CONFIG_ARG="${DOBBYVPN_CLI_TEST_CONFIG:-}"
BRANCH_ARG="${DOBBYVPN_ARTIFACT_BRANCH:-}"
REPOSITORY="${DOBBYVPN_GITHUB_REPOSITORY:-DobbyVPN/DobbyVPN}"
WORKFLOW="release.yml"
ARTIFACTS_DIR="$ROOT_DIR/.local-artifacts/macos"
SERVICE_PID=""
CLI_PATH=""

usage() {
  cat <<USAGE
Usage:
  scripts/macos_cli_check.sh --config <url-or-toml-file>

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

require_macos() {
  [[ "$(uname -s)" == "Darwin" ]] || die "This script is intended for macOS"
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

app_artifact() {
  case "$(uname -m)" in
    arm64|aarch64) printf 'dobbyVPN-macos-aarch64.zip' ;;
    x86_64|amd64) printf 'dobbyVPN-macos-amd64.zip' ;;
    *) die "Unsupported CPU architecture: $(uname -m)" ;;
  esac
}

download_artifacts() {
  local repo branch run_id app

  require_gh
  repo="$(github_repo)"
  branch="$(artifact_branch)"
  app="$(app_artifact)"

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
  gh -R "$repo" run download "$run_id" --name "$app" --dir "$ARTIFACTS_DIR/app"
  gh -R "$repo" run download "$run_id" --name macos_grpcvpnserver --dir "$ARTIFACTS_DIR/service"
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
  local zip_file unpacked app

  zip_file="$(find "$ARTIFACTS_DIR/app" -maxdepth 1 -name 'dobbyVPN-macos-*.zip' -type f | head -n 1)"
  [[ -n "$zip_file" ]] || die "Missing downloaded macOS app artifact"

  unpacked="$ARTIFACTS_DIR/app/unpacked"
  rm -rf "$unpacked"
  mkdir -p "$unpacked"
  log "Unpacking downloaded desktop app"
  unzip -q "$zip_file" -d "$unpacked"

  app="$(find "$unpacked" -name '*.app' -type d | head -n 1)"
  [[ -n "$app" ]] || die "Dobby .app bundle was not found"

  CLI_PATH="$app/Contents/MacOS/Dobby Vpn"
  [[ -x "$CLI_PATH" ]] || die "Dobby CLI executable was not found at $CLI_PATH"
}

cleanup() {
  if [[ -n "$SERVICE_PID" ]] && kill -0 "$SERVICE_PID" 2>/dev/null; then
    log "Stopping gRPC VPN service"
    sudo kill "$SERVICE_PID" 2>/dev/null || kill "$SERVICE_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

start_service() {
  local service="$ARTIFACTS_DIR/service/macos_grpcvpnserver"

  [[ -f "$service" ]] || die "Missing downloaded service artifact: $service"
  chmod +x "$service"

  log "Starting gRPC VPN service on port ${PORT}"
  sudo "$service" -port "$PORT" > "$ROOT_DIR/grpcvpnserver.log" 2>&1 &
  SERVICE_PID="$!"

  for _ in {1..30}; do
    if nc -z 127.0.0.1 "$PORT"; then
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

require_macos
download_artifacts
prepare_cli
start_service
run_check
log "Done"
