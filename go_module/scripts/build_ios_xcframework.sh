#!/usr/bin/env bash
# Build the public Go binding for physical iOS and iOS Simulator.
#
# TrustTunnel's supplied archive is physical-iOS arm64 only. With no argument,
# build that slice with the production bridge and a universal Simulator slice,
# then combine both generated frameworks. A local Simulator-only invocation
# selects one native architecture and skips the physical build. All other Go
# session/runtime/protocol code is identical between the builds.
set -euo pipefail

readonly output="DobbyVPNRuntime.xcframework"
readonly mobile_version="v0.0.0-20260520154334-0e4426e1883d"

simulator_architecture=""
if [[ "$#" -gt 0 ]]; then
  if [[ "$#" -ne 2 || "$1" != "--simulator-architecture" ]]; then
    echo "usage: $0 [--simulator-architecture arm64|amd64]" >&2
    exit 2
  fi
  simulator_architecture="$2"
  if [[ "$simulator_architecture" != "arm64" && "$simulator_architecture" != "amd64" ]]; then
    echo "unsupported Simulator architecture: $simulator_architecture (expected arm64 or amd64)" >&2
    exit 2
  fi
fi

gopath="$(go env GOPATH | tee /dev/stderr)"
tool_dir="$gopath/bin"
gomobile_bin="${GOMOBILE_BIN:-$tool_dir/gomobile}"
gobind_bin="${GOBIND_BIN:-$tool_dir/gobind}"
if [[ ! -x "$gomobile_bin" || ! -x "$gobind_bin" ]]; then
  echo "pinned gomobile and gobind are required; install both at $mobile_version" >&2
  exit 2
fi
for tool in "$gomobile_bin" "$gobind_bin"; do
  # Keep the complete tool report visible while retaining it for the module
  # pin check.  A command substitution alone would hide successful stdout
  # from the invoking build log.
  tool_metadata="$(go version -m "$tool" | tee /dev/stderr)"
  if [[ "$tool_metadata" != *"golang.org/x/mobile"*"$mobile_version"* ]]; then
    echo "tool module closure is not pinned to golang.org/x/mobile@$mobile_version: $tool" >&2
    exit 2
  fi
done
export GOMOBILE="${GOMOBILE:-$gopath/pkg/gomobile}"
mkdir -p "$GOMOBILE"
export PATH="${gomobile_bin%/*}:${gobind_bin%/*}:$PATH"

module_version="$(go list -m -f '{{.Version}}' golang.org/x/mobile | tee /dev/stderr)"
if [[ "$module_version" != "$mobile_version" ]]; then
  echo "go.mod resolves golang.org/x/mobile@$module_version; expected $mobile_version" >&2
  exit 2
fi

workdir="$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-ios-xcframework.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT

mkdir -p "$workdir/device" "$workdir/simulator"
device_output="$workdir/device/DobbyVPNRuntime.xcframework"
simulator_output="$workdir/simulator/DobbyVPNRuntime.xcframework"

if [[ -z "$simulator_architecture" ]]; then
  GO111MODULE=on gomobile bind \
    -tags=static \
    -trimpath \
    -ldflags="-buildid=" \
    -iosversion=15.6 \
    -target=ios/arm64 \
    -o "$device_output" \
    ./ios_exports
fi

simulator_target="iossimulator"
if [[ -n "$simulator_architecture" ]]; then
  simulator_target="iossimulator/$simulator_architecture"
fi
GO111MODULE=on gomobile bind \
  -tags='static simulator' \
  -trimpath \
  -ldflags="-buildid=" \
  -iosversion=15.6 \
  -target="$simulator_target" \
  -o "$simulator_output" \
  ./ios_exports

simulator_framework_output="$(
  find "$simulator_output" -type d -name DobbyVPNRuntime.framework -print | tee /dev/stderr
)"
if [[ -z "$simulator_framework_output" ]]; then
  echo "expected exactly one generated Simulator DobbyVPNRuntime.framework" >&2
  exit 1
fi
mapfile -t simulator_frameworks <<<"$simulator_framework_output"
if [[ "${#simulator_frameworks[@]}" -ne 1 ]]; then
  echo "expected exactly one generated Simulator DobbyVPNRuntime.framework" >&2
  exit 1
fi
simulator_framework="${simulator_frameworks[0]}"

if [[ -z "$simulator_architecture" ]]; then
  device_framework="$device_output/ios-arm64/DobbyVPNRuntime.framework"
  if [[ ! -d "$device_framework" ]]; then
    echo "missing generated device DobbyVPNRuntime.framework" >&2
    exit 1
  fi

  module_dir="$(go list -m -f '{{.Dir}}' trusttunnel-go | tee /dev/stderr)"
  bridge="$module_dir/lib/ios/libdobby_bridge.a"
  device_library="$device_framework/DobbyVPNRuntime"

  if [[ ! -f "$bridge" || ! -f "$device_library" ]]; then
    echo "missing TrustTunnel bridge or generated device library" >&2
    exit 1
  fi

  provenance="$module_dir/lib/static-libraries.provenance.json"
  if [[ ! -f "$provenance" ]]; then
    echo "missing TrustTunnel static-library provenance: $provenance" >&2
    exit 1
  fi
  parser_status=0
  expected_bridge_output="$(python3 - "$provenance" <<'PY' | tee /dev/stderr
import json
import re
import sys

path = sys.argv[1]
try:
    with open(path, encoding="utf-8") as source:
        document = json.load(source)
except OSError as error:
    print(f"cannot read TrustTunnel static-library provenance {path}: {error}", file=sys.stderr)
    raise SystemExit(1)
except json.JSONDecodeError as error:
    print(f"invalid TrustTunnel static-library provenance JSON {path}: {error}", file=sys.stderr)
    raise SystemExit(1)

try:
    value = document["artifacts"]["ios"]["sha256"]
except (KeyError, TypeError):
    print(f"TrustTunnel static-library provenance has no artifacts.ios.sha256: {path}", file=sys.stderr)
    raise SystemExit(1)

if not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}", value) is None:
    print(
        f"invalid iOS bridge SHA-256 in TrustTunnel static-library provenance {path}: "
        "expected exactly 64 lowercase hexadecimal characters",
        file=sys.stderr,
    )
    raise SystemExit(1)

print(value)
PY
)" || parser_status=$?
  # Stdout is forwarded by tee before it is parsed; stderr remains inherited
  # directly from Python. A successful substitution therefore hides neither.
  if [[ "$parser_status" -ne 0 ]]; then
    exit "$parser_status"
  fi
  if [[ ! "$expected_bridge_output" =~ ^[0-9a-f]{64}$ ]]; then
    echo "TrustTunnel bridge provenance parser returned an invalid hash" >&2
    exit 1
  fi
  expected_bridge_hash="$expected_bridge_output"
  bridge_hash_output="$(shasum -a 256 "$bridge" | tee /dev/stderr)"
  if [[ ! "$bridge_hash_output" =~ ^([0-9a-f]{64})[[:space:]] ]]; then
    echo "TrustTunnel bridge SHA-256 command returned an invalid record" >&2
    exit 1
  fi
  actual_bridge_hash="${BASH_REMATCH[1]}"
  if [[ "$actual_bridge_hash" != "$expected_bridge_hash" ]]; then
    echo "TrustTunnel bridge SHA-256 verification failed" >&2
    echo "expected: $expected_bridge_hash" >&2
    echo "actual:   $actual_bridge_hash" >&2
    exit 1
  fi

  merged_library="$workdir/DobbyVPNRuntime-merged.a"
  libtool -static -D -o "$merged_library" "$device_library" "$bridge"
  mv "$merged_library" "$device_library"
fi

final_output="$workdir/$output"
if [[ -n "$simulator_architecture" ]]; then
  xcodebuild -create-xcframework \
    -framework "$simulator_framework" \
    -output "$final_output"
else
  xcodebuild -create-xcframework \
    -framework "$device_framework" \
    -framework "$simulator_framework" \
    -output "$final_output"
fi
rm -rf "$output"
mv "$final_output" "$output"
