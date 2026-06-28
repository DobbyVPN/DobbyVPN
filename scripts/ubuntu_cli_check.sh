#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_VERSION="$(tr -d '[:space:]' < "$ROOT_DIR/.go-version")"
PORT="${PORT:-50151}"
CONFIG_ARG="${DOBBYVPN_CLI_TEST_CONFIG:-}"
SKIP_DEPS=0
SKIP_BUILD=0
SERVICE_PID=""

usage() {
  cat <<USAGE
Usage:
  scripts/ubuntu_cli_check.sh --config <url-or-toml-file>

Builds and checks the local VPN path on Ubuntu without Hydraulic Conveyor:
  Go gRPC VPN service -> Gradle desktop CLI -> check-config for every profile.

Options:
  --config <value>   Config URL or local TOML file. Can also be set with DOBBYVPN_CLI_TEST_CONFIG.
  --port <port>      gRPC VPN service port. Default: ${PORT}
  --skip-deps        Do not install system dependencies.
  --skip-build       Reuse existing build outputs.
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

download_file() {
  local url="$1"
  local output="$2"

  curl --fail --location --show-error --progress-bar --retry 3 --connect-timeout 30 "$url" -o "$output"
}

cleanup() {
  if [[ -n "${SERVICE_PID}" ]] && kill -0 "${SERVICE_PID}" 2>/dev/null; then
    log "Stopping gRPC VPN service"
    sudo kill "${SERVICE_PID}" 2>/dev/null || kill "${SERVICE_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      CONFIG_ARG="${2:-}"
      shift 2
      ;;
    --port)
      PORT="${2:-}"
      shift 2
      ;;
    --skip-deps)
      SKIP_DEPS=1
      shift
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

[[ -n "${CONFIG_ARG}" ]] || die "Pass --config <url-or-file> or set DOBBYVPN_CLI_TEST_CONFIG"
[[ -d "$ROOT_DIR/go_module" && -d "$ROOT_DIR/kmp_module" ]] || die "Run this from a cloned DobbyVPN repository"

require_linux() {
  [[ "$(uname -s)" == "Linux" ]] || die "This script is intended for Ubuntu/Linux"
}

linux_go_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    aarch64|arm64) printf 'arm64' ;;
    *) die "Unsupported CPU architecture: $(uname -m)" ;;
  esac
}

need_sudo() {
  if [[ "${EUID}" -eq 0 ]]; then
    return 0
  fi
  command -v sudo >/dev/null 2>&1 || die "sudo is required on a clean Ubuntu install"
}

install_apt_packages() {
  need_sudo
  local packages=(
    ca-certificates
    curl
    unzip
    zip
    git
    build-essential
    gcc
    g++
    pkg-config
    iproute2
    openjdk-17-jdk
  )
  local missing=()

  for package in "${packages[@]}"; do
    if ! dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then
      missing+=("$package")
    fi
  done

  if [[ "${#missing[@]}" -eq 0 ]]; then
    log "APT dependencies already installed"
    return
  fi

  log "Installing APT dependencies: ${missing[*]}"
  sudo apt-get update
  sudo apt-get install -y "${missing[@]}"
}

install_go() {
  if command -v go >/dev/null 2>&1 && go version | grep -q "go${GO_VERSION}"; then
    log "Go ${GO_VERSION} already installed"
    return
  fi

  need_sudo
  local arch
  arch="$(linux_go_arch)"

  local tarball="/tmp/go${GO_VERSION}.linux-${arch}.tar.gz"
  log "Installing Go ${GO_VERSION}"
  download_file "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" "$tarball"
  log "Extracting Go ${GO_VERSION}"
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf "$tarball"
  export PATH="/usr/local/go/bin:$PATH"
}

accept_android_licenses() {
  log "Accepting Android SDK licenses"
  set +o pipefail
  yes | sdkmanager --licenses
  local sdkmanager_status="${PIPESTATUS[1]}"
  set -o pipefail
  return "$sdkmanager_status"
}

install_android_sdk() {
  local sdk_root="${ANDROID_HOME:-$HOME/Android/Sdk}"
  export ANDROID_HOME="$sdk_root"
  export ANDROID_SDK_ROOT="$sdk_root"
  export PATH="$sdk_root/cmdline-tools/latest/bin:$sdk_root/platform-tools:$PATH"

  if [[ -x "$sdk_root/cmdline-tools/latest/bin/sdkmanager" ]] &&
    [[ -d "$sdk_root/platforms/android-35" ]] &&
    [[ -d "$sdk_root/platforms/android-36" ]] &&
    [[ -d "$sdk_root/build-tools/36.0.0" ]]; then
    log "Android SDK already installed"
    return
  fi

  local tools_zip="/tmp/android-commandlinetools-linux.zip"
  local tools_dir="$sdk_root/cmdline-tools"
  log "Installing Android SDK command line tools"
  mkdir -p "$tools_dir"
  download_file "https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip" "$tools_zip"
  log "Extracting Android SDK command line tools"
  rm -rf "$tools_dir/latest" "$tools_dir/cmdline-tools"
  unzip -q "$tools_zip" -d "$tools_dir"
  mv "$tools_dir/cmdline-tools" "$tools_dir/latest"

  accept_android_licenses
  log "Installing Android SDK packages"
  sdkmanager "platforms;android-35" "platforms;android-36" "build-tools;36.0.0" "platform-tools"
}

prepare_cloak_internal() {
  local source_dir="$ROOT_DIR/Cloak/internal"
  local target_dir="$ROOT_DIR/go_module/modules/Cloak/internal"

  if [[ ! -d "$source_dir" ]]; then
    log "Initializing git submodules"
    git -C "$ROOT_DIR" submodule update --init --recursive
  fi
  [[ -d "$source_dir" ]] || die "Missing $source_dir after submodule initialization"

  if [[ -d "$target_dir" ]]; then
    log "Cloak/internal already vendored"
    return
  fi

  log "Vendoring Cloak/internal into go_module/modules/Cloak"
  cp -R "$source_dir" "$target_dir"
}

build_service() {
  local service="$ROOT_DIR/go_module/ubuntu_grpcvpnserver"
  local arch
  arch="$(linux_go_arch)"

  if [[ "$SKIP_BUILD" -eq 1 && -x "$service" ]]; then
    log "Reusing existing gRPC VPN service"
  else
    log "Building Linux gRPC VPN service"
    (
      cd "$ROOT_DIR/go_module"
      CGO_ENABLED=1 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-buildid=" -o ubuntu_grpcvpnserver ./desktop_exports/
    )
  fi

  mkdir -p "$ROOT_DIR/kmp_module/services"
  cp "$service" "$ROOT_DIR/kmp_module/services/ubuntu_grpcvpnserver"
  chmod +x "$ROOT_DIR/kmp_module/services/ubuntu_grpcvpnserver"
}

build_desktop_cli() {
  if [[ "$SKIP_BUILD" -eq 1 ]]; then
    log "Skipping Gradle build"
    return
  fi

  log "Building desktop JVM app"
  (
    cd "$ROOT_DIR/kmp_module"
    ./gradlew --build-cache --parallel :app:jvmJar
  )
}

start_service() {
  log "Starting gRPC VPN service on port ${PORT}"
  sudo "$ROOT_DIR/kmp_module/services/ubuntu_grpcvpnserver" -port "$PORT" > "$ROOT_DIR/grpcvpnserver.log" 2>&1 &
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

run_cli_check() {
  log "Running CLI config check"
  (
    cd "$ROOT_DIR/kmp_module"
    PORT="$PORT" ./gradlew --quiet :app:run --args="check-config ${CONFIG_ARG}"
  )
}

main() {
  require_linux

  if [[ "$SKIP_DEPS" -eq 0 ]]; then
    install_apt_packages
    install_go
    install_android_sdk
  else
    log "Skipping dependency installation"
    export PATH="/usr/local/go/bin:${ANDROID_HOME:-$HOME/Android/Sdk}/cmdline-tools/latest/bin:${ANDROID_HOME:-$HOME/Android/Sdk}/platform-tools:$PATH"
  fi

  prepare_cloak_internal
  build_service
  build_desktop_cli
  start_service
  run_cli_check
  log "Done"
}

main
