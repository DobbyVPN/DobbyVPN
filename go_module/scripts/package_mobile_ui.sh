#!/usr/bin/env bash
# Convenience wrapper for Go/Fyne mobile iteration. Release Android artifacts
# come from android_module, which embeds the native VpnService. iOS packaging
# is delegated to package_ios_app.sh so the NetworkExtension shell is embedded
# as well. Android VpnService and the iOS NetworkExtension remain the native
# native lifecycle boundaries; this helper's Android branch remains renderer-only by
# design and is not a second VPN runtime or a release artifact.
set -euo pipefail

target=${1:-}
output=${2:-}
if [[ -z "$target" || -z "$output" ]]; then
  echo "usage: $0 android/arm64|android/amd64|ios|iossimulator OUTPUT" >&2
  exit 2
fi
script_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
module_root="$script_root/go_module"
case "$target" in
  android|android/arm|android/arm64|android/amd64|android/386) ;;
  ios|iossimulator)
    exec "$module_root/scripts/package_ios_app.sh" "$target" "$output"
    ;;
  *) echo "unsupported mobile target: $target" >&2; exit 2 ;;
esac

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
# Fyne's mobile packager checks for this directory but does not need the
# gomobile-generated OpenAL cache for the renderer-only Android convenience
# artifact. Do not run gomobile init or install an unpinned toolchain.
mkdir -p -- "$($go_bin env GOPATH)/pkg/gomobile"

pushd "$module_root/cmd/dobbyui" >/dev/null
"$go_bin" tool fyne package \
  --os "$target" \
  --name "Dobby Vpn" \
  --app-id com.dobby.vpn \
  --icon "$script_root/assets/logo.png" \
  --app-version "$version" \
  --app-build "$build" \
  --tags accessibility
popd >/dev/null

case "$target" in
  android*) artifact="$module_root/cmd/dobbyui/Dobby_Vpn.apk" ;;
  *) artifact="$module_root/cmd/dobbyui/Dobby-Vpn.app" ;
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
