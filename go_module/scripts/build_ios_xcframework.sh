#!/usr/bin/env bash
# Build the public Go binding for physical iOS and iOS Simulator.
#
# TrustTunnel ships a physical-iOS arm64 archive and a universal Simulator
# archive. With no argument, build both frameworks. A local Simulator-only
# invocation selects one native architecture and skips the physical build.
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
simulator_framework_count="$(printf '%s\n' "$simulator_framework_output" | grep -c .)"
if [[ "$simulator_framework_count" -ne 1 ]]; then
  echo "expected exactly one generated Simulator DobbyVPNRuntime.framework" >&2
  exit 1
fi
simulator_framework="$simulator_framework_output"

module_dir="$(go list -m -f '{{.Dir}}' trusttunnel-go | tee /dev/stderr)"
simulator_bridge="$module_dir/lib/ios-simulator/libdobby_bridge.a"
simulator_provenance="$module_dir/lib/static-libraries.provenance.json"
if [[ ! -f "$simulator_bridge" || ! -f "$simulator_provenance" ]]; then
  echo "missing iOS Simulator TrustTunnel bridge or provenance" >&2
  exit 1
fi
simulator_arches="$(xcrun lipo -archs "$simulator_bridge" | tee /dev/stderr)"
required_simulator_arches=(arm64 x86_64)
if [[ -n "$simulator_architecture" ]]; then
  selected_simulator_arch=arm64
  if [[ "$simulator_architecture" == amd64 ]]; then selected_simulator_arch=x86_64; fi
  required_simulator_arches=("$selected_simulator_arch")
fi
for simulator_arch in "${required_simulator_arches[@]}"; do
  if [[ " $simulator_arches " != *" $simulator_arch "* ]]; then
    echo "TrustTunnel Simulator bridge is missing architecture $simulator_arch" >&2
    exit 1
  fi
done
simulator_expected_hash="$(python3 - "$simulator_provenance" <<'PY' | tee /dev/stderr
import json
import re
import sys

path = sys.argv[1]
try:
    with open(path, encoding="utf-8") as source:
        document = json.load(source)
except (OSError, json.JSONDecodeError) as error:
    print(f"cannot read TrustTunnel Simulator bridge provenance {path}: {error}", file=sys.stderr)
    raise SystemExit(1)

value = document.get("artifacts", {}).get("ios_simulator", {}).get("sha256")
if document.get("schema") != 2 or not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}", value) is None:
    print(f"invalid TrustTunnel Simulator bridge provenance {path}", file=sys.stderr)
    raise SystemExit(1)
print(value)
PY
)"
simulator_actual_hash="$(shasum -a 256 "$simulator_bridge" | tee /dev/stderr | awk '{print $1}')"
if [[ "$simulator_actual_hash" != "$simulator_expected_hash" ]]; then
  echo "TrustTunnel Simulator bridge SHA-256 verification failed" >&2
  echo "expected: $simulator_expected_hash" >&2
  echo "actual:   $simulator_actual_hash" >&2
  exit 1
fi
simulator_library="$simulator_framework/DobbyVPNRuntime"
if [[ ! -f "$simulator_library" ]]; then
  echo "missing generated Simulator DobbyVPNRuntime binary" >&2
  exit 1
fi
simulator_merge_arches=(arm64 x86_64)
if [[ -n "$simulator_architecture" ]]; then
  selected_simulator_arch=arm64
  if [[ "$simulator_architecture" == amd64 ]]; then selected_simulator_arch=x86_64; fi
  simulator_merge_arches=("$selected_simulator_arch")
fi
merged_simulator_slices=()
for simulator_arch in "${simulator_merge_arches[@]}"; do
  runtime_slice="$workdir/DobbyVPNRuntime-Simulator-$simulator_arch.a"
  bridge_slice="$workdir/libdobby_bridge-Simulator-$simulator_arch.a"
  merged_slice="$workdir/DobbyVPNRuntime-Simulator-$simulator_arch-merged.a"
  xcrun lipo -thin "$simulator_arch" "$simulator_library" -output "$runtime_slice"
  xcrun lipo -thin "$simulator_arch" "$simulator_bridge" -output "$bridge_slice"
  libtool -static -D -o "$merged_slice" "$runtime_slice" "$bridge_slice"
  merged_simulator_slices+=("$merged_slice")
done
if [[ "${#merged_simulator_slices[@]}" -eq 1 ]]; then
  mv "${merged_simulator_slices[0]}" "$simulator_library"
else
  merged_simulator_library="$workdir/DobbyVPNRuntime-Simulator-merged.a"
  xcrun lipo -create "${merged_simulator_slices[@]}" -output "$merged_simulator_library"
  mv "$merged_simulator_library" "$simulator_library"
fi

if [[ -z "$simulator_architecture" ]]; then
  device_framework="$device_output/ios-arm64/DobbyVPNRuntime.framework"
  if [[ ! -d "$device_framework" ]]; then
    echo "missing generated device DobbyVPNRuntime.framework" >&2
    exit 1
  fi

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
