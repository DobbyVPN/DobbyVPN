#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_VERSION="$(tr -d '[:space:]' < "$ROOT_DIR/.go-version")"
PORT="${PORT:-50151}"
CONFIG_ARG="${DOBBYVPN_CLI_TEST_CONFIG:-}"
SKIP_DEPS=0
SKIP_BUILD=0
SERVICE_PID=""
TOOLS_DIR="$ROOT_DIR/.local-tools/macos"

usage() {
  cat <<USAGE
Usage:
  scripts/macos_cli_check.sh --config <url-or-toml-file>

Builds and checks the local VPN path on macOS without Hydraulic Conveyor:
  Go gRPC VPN service -> Gradle desktop CLI -> check-config for every profile.

Options:
  --config <value>   Config URL or local TOML file. Can also be set with DOBBYVPN_CLI_TEST_CONFIG.
  --port <port>      gRPC VPN service port. Default: ${PORT}
  --skip-deps        Do not install local Go/JDK/Android SDK dependencies.
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

require_macos() {
  [[ "$(uname -s)" == "Darwin" ]] || die "This script is intended for macOS"
}

macos_go_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) die "Unsupported CPU architecture: $(uname -m)" ;;
  esac
}

adoptium_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'x64' ;;
    arm64|aarch64) printf 'aarch64' ;;
    *) die "Unsupported CPU architecture: $(uname -m)" ;;
  esac
}

require_command_line_tools() {
  if xcode-select -p >/dev/null 2>&1; then
    log "Xcode Command Line Tools already installed"
    return
  fi

  xcode-select --install >/dev/null 2>&1 || true
  die "Install Xcode Command Line Tools from the opened dialog, then run this script again"
}

install_go() {
  if command -v go >/dev/null 2>&1 && go version | grep -q "go${GO_VERSION}"; then
    log "Go ${GO_VERSION} already available"
    return
  fi

  local arch go_root tarball extract_dir
  arch="$(macos_go_arch)"
  go_root="$TOOLS_DIR/go-${GO_VERSION}"
  tarball="/tmp/go${GO_VERSION}.darwin-${arch}.tar.gz"
  extract_dir="/tmp/dobby-go-${GO_VERSION}"

  if [[ ! -x "$go_root/bin/go" ]]; then
    log "Installing Go ${GO_VERSION} locally"
    rm -rf "$extract_dir" "$go_root"
    mkdir -p "$TOOLS_DIR" "$extract_dir"
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.darwin-${arch}.tar.gz" -o "$tarball"
    tar -xzf "$tarball" -C "$extract_dir"
    mv "$extract_dir/go" "$go_root"
  else
    log "Go ${GO_VERSION} already installed locally"
  fi

  export PATH="$go_root/bin:$PATH"
}

find_java_17() {
  if [[ -n "${JAVA_HOME:-}" && -x "$JAVA_HOME/bin/java" ]] &&
    "$JAVA_HOME/bin/java" -version 2>&1 | grep -q 'version "17'; then
    printf '%s' "$JAVA_HOME"
    return 0
  fi

  if /usr/libexec/java_home -v 17 >/dev/null 2>&1; then
    /usr/libexec/java_home -v 17
    return 0
  fi

  return 1
}

install_jdk() {
  local java_home jdk_root tarball extract_dir
  if java_home="$(find_java_17)"; then
    export JAVA_HOME="$java_home"
    export PATH="$JAVA_HOME/bin:$PATH"
    log "JDK 17 already available"
    return
  fi

  jdk_root="$TOOLS_DIR/jdk-17"
  tarball="/tmp/temurin-17-macos-$(adoptium_arch).tar.gz"
  extract_dir="/tmp/dobby-jdk-17"

  if [[ ! -x "$jdk_root/bin/java" ]]; then
    log "Installing JDK 17 locally"
    rm -rf "$extract_dir" "$jdk_root"
    mkdir -p "$TOOLS_DIR" "$extract_dir"
    curl -LfsS "https://api.adoptium.net/v3/binary/latest/17/ga/mac/$(adoptium_arch)/jdk/hotspot/normal/eclipse" -o "$tarball"
    tar -xzf "$tarball" -C "$extract_dir"
    java_home="$(find "$extract_dir" -path '*/Contents/Home' -type d | head -n 1)"
    [[ -n "$java_home" ]] || die "Downloaded JDK 17 archive did not contain Contents/Home"
    mv "$java_home" "$jdk_root"
  else
    log "JDK 17 already installed locally"
  fi

  export JAVA_HOME="$jdk_root"
  export PATH="$JAVA_HOME/bin:$PATH"
}

install_android_sdk() {
  local sdk_root tools_zip tools_dir
  sdk_root="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
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

  tools_zip="/tmp/android-commandlinetools-mac.zip"
  tools_dir="$sdk_root/cmdline-tools"
  log "Installing Android SDK command line tools"
  mkdir -p "$tools_dir"
  curl -fsSL "https://dl.google.com/android/repository/commandlinetools-mac-11076708_latest.zip" -o "$tools_zip"
  rm -rf "$tools_dir/latest" "$tools_dir/cmdline-tools"
  unzip -q "$tools_zip" -d "$tools_dir"
  mv "$tools_dir/cmdline-tools" "$tools_dir/latest"

  yes | sdkmanager --licenses >/dev/null
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
  local service="$ROOT_DIR/go_module/macos_grpcvpnserver"
  local arch
  arch="$(macos_go_arch)"

  if [[ "$SKIP_BUILD" -eq 1 && -x "$service" ]]; then
    log "Reusing existing gRPC VPN service"
  else
    log "Building macOS gRPC VPN service"
    (
      cd "$ROOT_DIR/go_module"
      CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags="-buildid=" -o macos_grpcvpnserver ./desktop_exports/
    )
  fi

  mkdir -p "$ROOT_DIR/kmp_module/services"
  cp "$service" "$ROOT_DIR/kmp_module/services/macos_grpcvpnserver"
  chmod +x "$ROOT_DIR/kmp_module/services/macos_grpcvpnserver"
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
  sudo "$ROOT_DIR/kmp_module/services/macos_grpcvpnserver" -port "$PORT" > "$ROOT_DIR/grpcvpnserver.log" 2>&1 &
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

run_cli_check() {
  log "Running CLI config check"
  (
    cd "$ROOT_DIR/kmp_module"
    PORT="$PORT" ./gradlew --quiet :app:run --args="check-config ${CONFIG_ARG}"
  )
}

main() {
  require_macos

  if [[ "$SKIP_DEPS" -eq 0 ]]; then
    require_command_line_tools
    install_go
    install_jdk
    install_android_sdk
  else
    log "Skipping dependency installation"
  fi

  prepare_cloak_internal
  build_service
  build_desktop_cli
  start_service
  run_cli_check
  log "Done"
}

main
