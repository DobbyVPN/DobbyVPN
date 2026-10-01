#!/usr/bin/env bash
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1

source_root=''
source_sha=''
output=''
test_companion_output=''
source_repository='DobbyVPN/DobbyVPN'

while (($#)); do
  case "$1" in
    --source-root) source_root=${2:?missing --source-root value}; shift 2 ;;
    --source-sha) source_sha=${2:?missing --source-sha value}; shift 2 ;;
    --output) output=${2:?missing --output value}; shift 2 ;;
    --test-companion-output) test_companion_output=${2:?missing --test-companion-output value}; shift 2 ;;
    --source-repository) source_repository=${2:?missing --source-repository value}; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$source_root" && -d "$source_root" ]] || {
  echo '--source-root must be an existing directory' >&2
  exit 2
}
source_root=$(cd -- "$source_root" && pwd -P)
[[ -n "$output" ]] || { echo '--output is required' >&2; exit 2; }
[[ -n "$test_companion_output" ]] || {
  echo '--test-companion-output is required' >&2
  exit 2
}
[[ -z "$source_sha" || "$source_sha" =~ ^[0-9a-f]{40}$ ]] || {
  echo '--source-sha must be a full lowercase Git commit identity' >&2
  exit 2
}

go_bin=${GO_BIN:-$(command -v go || true)}
[[ -n "$go_bin" && -x "$go_bin" ]] || { echo 'Go executable is required' >&2; exit 2; }
export GO_BIN="$go_bin"
export GOPATH="${GOPATH:-$("$go_bin" env GOPATH)}"

sdk_root=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}
[[ -n "$sdk_root" && -d "$sdk_root" ]] || { echo 'Android SDK is required' >&2; exit 2; }
export ANDROID_SDK_ROOT="$sdk_root"
ndk_version=$(python3 "$source_root/.github/scripts/android/android_dependency_provenance.py" --source-root "$source_root" --print-pin android_ndk)
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$sdk_root/ndk/$ndk_version}"
[[ -f "$ANDROID_NDK_HOME/source.properties" ]] || {
  echo "Android NDK $ndk_version is required: $ANDROID_NDK_HOME" >&2
  exit 2
}

driver_args=(
  --source-root "$source_root"
  --output "$output"
  --test-companion-output "$test_companion_output"
  --local
)
if [[ -n "$source_sha" ]]; then
  driver_args+=(--source-sha "$source_sha" --source-repository "$source_repository")
fi
"$source_root/.github/scripts/android/android_build_driver.sh" "${driver_args[@]}"

readelf_bin=${ANDROID_READELF:-}
if [[ -z "$readelf_bin" ]]; then
  mapfile -t ndk_toolchains < <(
    find "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt" -mindepth 1 -maxdepth 1 -type d -print
  )
  [[ "${#ndk_toolchains[@]}" -eq 1 ]] || {
    echo 'Android NDK must contain exactly one host toolchain' >&2
    exit 2
  }
  readelf_bin="${ndk_toolchains[0]}/bin/llvm-readelf"
fi
[[ -x "$readelf_bin" ]] || { echo "Android NDK llvm-readelf is required: $readelf_bin" >&2; exit 2; }
python3 "$source_root/.github/scripts/android/verify_android_native_payloads.py" \
  --apk "$output" --readelf "$readelf_bin"
echo "android_build_check status=passed artifact=$output"
