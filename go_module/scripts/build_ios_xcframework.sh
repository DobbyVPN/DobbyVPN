#!/usr/bin/env bash
# Build the public Go binding for physical iOS and iOS Simulator.
#
# TrustTunnel's supplied archive is physical-iOS arm64 only.  We build that
# slice with the production bridge, build Simulator slices with the explicit
# `simulator` fallback, then combine the two generated frameworks.  All other
# Go session/runtime/protocol code is identical between the two builds.
set -euo pipefail

readonly output="DobbyVPNRuntime.xcframework"
readonly mobile_version="v0.0.0-20260520154334-0e4426e1883d"
readonly expected_bridge_hash="ff9e5593a5c3218242338aca83db2432dded78d1748195302b363ddfddfd85e8"

gopath="$(go env GOPATH)"
tool_dir="$gopath/bin"
gomobile_bin="${GOMOBILE_BIN:-$tool_dir/gomobile}"
gobind_bin="${GOBIND_BIN:-$tool_dir/gobind}"
if [[ ! -x "$gomobile_bin" || ! -x "$gobind_bin" ]]; then
  echo "pinned gomobile and gobind are required; install both at $mobile_version" >&2
  exit 2
fi
for tool in "$gomobile_bin" "$gobind_bin"; do
  tool_metadata="$(go version -m "$tool" 2>&1)"
  printf '%s\n' "$tool_metadata"
  if ! grep -F 'golang.org/x/mobile' <<<"$tool_metadata" | grep -F "$mobile_version" >/dev/null; then
    echo "tool module closure is not pinned to golang.org/x/mobile@$mobile_version: $tool" >&2
    exit 2
  fi
done
export GOMOBILE="${GOMOBILE:-$gopath/pkg/gomobile}"
mkdir -p "$GOMOBILE"
export PATH="$(dirname "$gomobile_bin"):$(dirname "$gobind_bin"):$PATH"

module_version="$(go list -m -f '{{.Version}}' golang.org/x/mobile)"
if [[ "$module_version" != "$mobile_version" ]]; then
  echo "go.mod resolves golang.org/x/mobile@$module_version; expected $mobile_version" >&2
  exit 2
fi

workdir="$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-ios-xcframework.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT

mkdir -p "$workdir/device" "$workdir/simulator"
device_output="$workdir/device/DobbyVPNRuntime.xcframework"
simulator_output="$workdir/simulator/DobbyVPNRuntime.xcframework"

GO111MODULE=on gomobile bind \
  -tags=static \
  -trimpath \
  -ldflags="-buildid=" \
  -iosversion=15.6 \
  -target=ios/arm64 \
  -o "$device_output" \
  ./ios_exports

GO111MODULE=on gomobile bind \
  -tags='static simulator' \
  -trimpath \
  -ldflags="-buildid=" \
  -iosversion=15.6 \
  -target=iossimulator \
  -o "$simulator_output" \
  ./ios_exports

device_framework="$(find "$device_output" -type d -name DobbyVPNRuntime.framework -print)"
simulator_framework="$(find "$simulator_output" -type d -name DobbyVPNRuntime.framework -print)"

if [[ -z "$device_framework" || -z "$simulator_framework" ]] \
  || [[ "$(printf '%s\n' "$device_framework" | wc -l | tr -d ' ')" -ne 1 ]] \
  || [[ "$(printf '%s\n' "$simulator_framework" | wc -l | tr -d ' ')" -ne 1 ]]; then
  echo "expected exactly one device and one Simulator DobbyVPNRuntime.framework" >&2
  exit 1
fi

module_dir="$(go list -m -f '{{.Dir}}' trusttunnel-go)"
bridge="$module_dir/lib/ios/libdobby_bridge.a"
device_library="$device_framework/DobbyVPNRuntime"

if [[ ! -f "$bridge" || ! -f "$device_library" ]]; then
  echo "missing TrustTunnel bridge or generated device library" >&2
  exit 1
fi

actual_bridge_hash="$(shasum -a 256 "$bridge" | awk '{print $1}')"
if [[ "$actual_bridge_hash" != "$expected_bridge_hash" ]]; then
  echo "TrustTunnel bridge SHA-256 verification failed" >&2
  echo "expected: $expected_bridge_hash" >&2
  echo "actual:   $actual_bridge_hash" >&2
  exit 1
fi

merged_library="$workdir/DobbyVPNRuntime-merged.a"
libtool -static -D -o "$merged_library" "$device_library" "$bridge"
mv "$merged_library" "$device_library"

# The output is ignored/generated. Replacing this exact path makes local and
# CI rebuilds idempotent without touching any source or credential material.
rm -rf "$output"
xcodebuild -create-xcframework \
  -framework "$device_framework" \
  -framework "$simulator_framework" \
  -output "$output"
