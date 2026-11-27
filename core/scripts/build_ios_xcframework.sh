#!/usr/bin/env bash
# Build the public Go binding for physical iOS and iOS Simulator.
#
# TrustTunnel ships a physical-iOS arm64 archive and a universal Simulator
# archive. With no argument, build both frameworks. A local Simulator-only
# invocation selects one native architecture and skips the physical build.
set -euo pipefail

configuration=${DOBBY_BUILD_CONFIGURATION:-Release}
go_debug_flags=(-trimpath)
case "$configuration" in
  Release) ;;
  Debug) go_debug_flags+=(-gcflags="all=-N -l"); export CGO_CFLAGS="-O0 -g"; export CGO_CXXFLAGS="-O0 -g" ;;
  *) echo "Unknown build configuration: $configuration" >&2; exit 2 ;;
esac

version=$(cat ../VERSION)
commit=${SOURCE_COMMIT:-${GITHUB_SHA:-}}
if [[ -z "$commit" ]]; then commit=$(git rev-parse HEAD); fi
go_identity="-buildid= -X core/buildinfo.Version=$version -X core/buildinfo.Commit=$commit -X core/buildinfo.Configuration=$configuration"

readonly output="DobbyVPNRuntime.xcframework"

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
gopath="${gopath%%:*}"
tool_dir="$(go env GOBIN | tee /dev/stderr)"
tool_dir="${tool_dir:-$gopath/bin}"
gomobile_bin="${GOMOBILE_BIN:-$tool_dir/gomobile}"
gobind_bin="${GOBIND_BIN:-$tool_dir/gobind}"
export GOMOBILE="${GOMOBILE:-$gopath/pkg/gomobile}"
mkdir -p "$GOMOBILE"

workdir="$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-ios-xcframework.XXXXXX")"
cleanup_workdir() {
  local status=$? cleanup_status=0
  rm -rf "$workdir" || cleanup_status=$?
  if (( cleanup_status != 0 )); then
    echo "failed to remove iOS build work directory $workdir (exit $cleanup_status)" >&2
    if (( status == 0 )); then status=$cleanup_status; fi
  fi
  exit "$status"
}
trap cleanup_workdir EXIT

python3 ../.github/scripts/ios/mobile_tools.py stage "$workdir/bin" "$gomobile_bin" "$gobind_bin"
export PATH="$workdir/bin:$PATH"

mkdir -p "$workdir/device" "$workdir/simulator"
device_output="$workdir/device/DobbyVPNRuntime.xcframework"
simulator_output="$workdir/simulator/DobbyVPNRuntime.xcframework"

run_bind() {
  local stage=$1 target=$2 tags=$3 bind_output=$4
  shift 4
  local arch query_tags="${tags// /,},ios" bind_tmp="$workdir/$stage-tmp" status=0 cleanup_status=0
  for arch in "$@"; do
    echo "iOS $stage module query: GO111MODULE=on GOOS=ios GOARCH=$arch go list -m -json -tags=$query_tags all" >&2
    GO111MODULE=on GOOS=ios GOARCH="$arch" go list -m -json "-tags=$query_tags" all
  done
  mkdir -p "$bind_tmp"
  echo "iOS $stage gomobile bind: target=$target work=$bind_tmp" >&2
  if TMPDIR="$bind_tmp" GOTMPDIR="$bind_tmp" GO111MODULE=on gomobile bind \
    -x -work -tags="$tags" -ldflags="$go_identity" "${go_debug_flags[@]}" \
    -iosversion=15.6 -target="$target" -o "$bind_output" ./ffi/ios; then
    :
  else
    status=$?
    echo "iOS $stage gomobile bind failed (exit $status); generated go.mod files:" >&2
    find "$bind_tmp" -type f -name go.mod -exec sh -c '
      for file do
        printf "generated go.mod: %s\n" "$file" >&2
        cat "$file" >&2 || echo "failed to read generated go.mod: $file" >&2
      done
    ' sh {} + || echo "failed to find generated go.mod files in $bind_tmp" >&2
  fi
  rm -rf "$bind_tmp" || cleanup_status=$?
  if (( cleanup_status != 0 )); then
    echo "failed to remove iOS $stage temporary directory $bind_tmp (exit $cleanup_status)" >&2
    if (( status == 0 )); then status=$cleanup_status; fi
  fi
  return "$status"
}

if [[ -z "$simulator_architecture" ]]; then
  run_bind device ios/arm64 static "$device_output" arm64
fi

simulator_target="iossimulator"
simulator_go_arches=(arm64 amd64)
if [[ -n "$simulator_architecture" ]]; then
  simulator_target="iossimulator/$simulator_architecture"
  simulator_go_arches=("$simulator_architecture")
fi
run_bind simulator "$simulator_target" 'static simulator' "$simulator_output" "${simulator_go_arches[@]}"

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
provenance="$module_dir/lib/static-libraries.provenance.json"
if [[ ! -f "$simulator_bridge" || ! -f "$provenance" ]]; then
  echo "missing iOS Simulator TrustTunnel bridge or provenance" >&2
  exit 1
fi
simulator_arches="$(xcrun lipo -archs "$simulator_bridge" | tee /dev/stderr)"
# lipo uses x86_64 for Go's amd64.
simulator_native_arches=("${simulator_go_arches[@]/amd64/x86_64}")
for simulator_arch in "${simulator_native_arches[@]}"; do
  if [[ " $simulator_arches " != *" $simulator_arch "* ]]; then
    echo "TrustTunnel Simulator bridge is missing architecture $simulator_arch" >&2
    exit 1
  fi
done
python3 ../.github/scripts/ios/verify_bridge.py "$provenance" ios_simulator "$simulator_bridge"
simulator_library="$simulator_framework/DobbyVPNRuntime"
if [[ ! -f "$simulator_library" ]]; then
  echo "missing generated Simulator DobbyVPNRuntime binary" >&2
  exit 1
fi
merged_simulator_slices=()
for simulator_arch in "${simulator_native_arches[@]}"; do
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

  python3 ../.github/scripts/ios/verify_bridge.py "$provenance" ios "$bridge"

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
