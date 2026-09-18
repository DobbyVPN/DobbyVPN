#!/usr/bin/env bash
# Build the shared Go/Fyne mobile UI package.
#
# This deliberately packages only the Go UI. Android VpnService and the iOS
# NetworkExtension remain native lifecycle boundaries and are integrated by
# their platform projects; this command is the reversible UI migration
# artifact, not a second VPN runtime.
set -euo pipefail

target=${1:-}
output=${2:-}
if [[ -z "$target" || -z "$output" ]]; then
  echo "usage: $0 android/arm64|android/amd64|ios|iossimulator OUTPUT" >&2
  exit 2
fi
case "$target" in
  android|android/arm|android/arm64|android/amd64|android/386|ios|iossimulator) ;;
  *) echo "unsupported mobile target: $target" >&2; exit 2 ;;
esac

script_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
module_root="$script_root/go_module"
version=${VERSION_NAME:-$(tr -d '[:space:]' < "$script_root/VERSION")}
build=${APP_BUILD:-1005001}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "VERSION_NAME must be numeric x.y.z" >&2
  exit 2
}
[[ "$build" =~ ^[1-9][0-9]*$ ]] || {
  echo "APP_BUILD must be a positive integer" >&2
  exit 2
}

go_bin=${GO_BIN:-go}
command -v "$go_bin" >/dev/null || { echo "Go is unavailable: $go_bin" >&2; exit 2; }
mkdir -p -- "$(dirname -- "$output")"

pushd "$module_root" >/dev/null
"$go_bin" tool fyne package \
  --os "$target" \
  --src ./cmd/dobbyui \
  --name "Dobby Vpn" \
  --app-id com.dobby.vpn \
  --app-version "$version" \
  --app-build "$build" \
  --tags accessibility
popd >/dev/null

case "$target" in
  android*) artifact="$module_root/Dobby_Vpn.apk" ;;
  *) artifact="$module_root/Dobby-Vpn.app" ;;
esac
[[ -e "$artifact" ]] || {
  echo "Fyne did not produce the expected mobile artifact: $artifact" >&2
  exit 1
}
rm -rf -- "$output"
if [[ -d "$artifact" ]]; then
  cp -R -- "$artifact" "$output"
else
  cp -- "$artifact" "$output"
fi
