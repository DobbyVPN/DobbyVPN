#!/usr/bin/env bash
set -euo pipefail

fdroiddata_dir=""
fdroidserver_dir=""
reference_apk=""
source_sha=""
version_name=""
version_code=""

while (($# > 0)); do
  case "$1" in
    --fdroiddata)
      fdroiddata_dir="$2"
      shift 2
      ;;
    --fdroidserver)
      fdroidserver_dir="$2"
      shift 2
      ;;
    --reference-apk)
      reference_apk="$2"
      shift 2
      ;;
    --source-sha)
      source_sha="$2"
      shift 2
      ;;
    --version-name)
      version_name="$2"
      shift 2
      ;;
    --version-code)
      version_code="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$fdroiddata_dir" || -z "$fdroidserver_dir" || -z "$reference_apk" \
  || -z "$source_sha" || -z "$version_name" || -z "$version_code" ]]; then
  echo "all F-Droid check arguments are required" >&2
  exit 2
fi
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$version_name" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
[[ "$version_code" =~ ^[1-9][0-9]*$ ]]

metadata_path="$fdroiddata_dir/metadata/com.dobby.vpn.yml"
metadata_helper="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/fdroid_release_metadata.py"
reference_apk="$(readlink -f "$reference_apk")"
reference_dir="$(dirname "$reference_apk")"
baseline_path="$(mktemp)"
fdroid_home="/home/vagrant"
reference_cert="$reference_dir/fdroid-reference.crt"
reference_key="$reference_dir/fdroid-reference.key"
reference_ca_bundle="$reference_dir/fdroid-reference-ca-bundle.crt"
https_log="$reference_dir/fdroid-https.log"
reference_url="https://127.0.0.1:8765/$(basename "$reference_apk")"
diagnostic_dir="${FDROID_DIAGNOSTIC_DIR:-}"
server_pid=""

test -d "$fdroiddata_dir"
test -d "$fdroidserver_dir"
test -f "$metadata_path"
test -f "$reference_apk"
test -f "$metadata_helper"

cleanup() {
  local primary_status=$?
  local cleanup_status=0
  if [[ -n "$server_pid" ]]; then
    if kill "$server_pid"; then
      :
    else
      local kill_status=$?
      printf 'F-Droid HTTPS server cleanup kill failed (status=%s, pid=%s)\n' \
        "$kill_status" "$server_pid" >&2
      cleanup_status=1
    fi
    if wait "$server_pid"; then
      :
    else
      local wait_status=$?
      # A process terminated by our SIGTERM commonly reports 143; it was
      # still reaped successfully. Preserve any other wait failure.
      if [[ "$wait_status" != 143 && "$wait_status" != 0 ]]; then
        printf 'F-Droid HTTPS server cleanup wait failed (status=%s, pid=%s)\n' \
          "$wait_status" "$server_pid" >&2
        cleanup_status=1
      fi
    fi
  fi
  if ! rm -f "$baseline_path" "$reference_cert" "$reference_key" \
    "$reference_ca_bundle" "$https_log"; then
    printf 'F-Droid temporary-file cleanup failed\n' >&2
    cleanup_status=1
  fi
  # Preserve the primary build/scanner result. A cleanup failure blocks an
  # otherwise successful run, but never replaces an already-failed result.
  if (( primary_status == 0 && cleanup_status != 0 )); then
    return 1
  fi
  return "$primary_status"
}
trap cleanup EXIT

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$reference_key" \
  -out "$reference_cert" \
  -days 1 \
  -subj "/CN=127.0.0.1" \
  -addext "subjectAltName=IP:127.0.0.1"
if [[ -f /etc/ssl/certs/ca-certificates.crt ]]; then
  cat /etc/ssl/certs/ca-certificates.crt "$reference_cert" > "$reference_ca_bundle"
else
  cp "$reference_cert" "$reference_ca_bundle"
fi

python3 - "$reference_dir" "$reference_cert" "$reference_key" \
  > "$https_log" 2>&1 <<'PY' &
import functools
import http.server
import ssl
import sys

directory, certificate, private_key = sys.argv[1:]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=directory)
server = http.server.ThreadingHTTPServer(("127.0.0.1", 8765), handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(certificate, private_key)
server.socket = context.wrap_socket(server.socket, server_side=True)
server.serve_forever()
PY
server_pid=$!
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"

for _ in {1..20}; do
  if python3 - "$reference_cert" "$reference_url" <<'PY'
import ssl
import sys
import urllib.request

context = ssl.create_default_context(cafile=sys.argv[1])
with urllib.request.urlopen(sys.argv[2], context=context, timeout=2) as response:
    response.read(1)
PY
  then
    break
  fi
  sleep 1
done
kill -0 "$server_pid"

if [[ -f /etc/profile.d/bsenv.sh ]]; then
  # The image uses this file to define the same SDK and buildserver paths as
  # the production F-Droid build server.
  # shellcheck source=/dev/null
  source /etc/profile.d/bsenv.sh
fi
export DEBIAN_FRONTEND=noninteractive
export ANDROID_HOME="${ANDROID_HOME:-/opt/android-sdk}"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$ANDROID_HOME}"

apt-get update
apt-get dist-upgrade -y
if sdkmanager_path="$(command -v sdkmanager)"; then
  printf 'sdkmanager: %s\n' "$sdkmanager_path"
  sdkmanager "platform-tools" "build-tools;31.0.0"
else
  printf '%s\n' 'sdkmanager is unavailable; Android SDK package refresh was skipped' >&2
fi

fdroid_environment=(
  env
  "PATH=$fdroidserver_dir:$PATH"
  "PYTHONPATH=$fdroidserver_dir:$fdroidserver_dir/examples"
  PYTHONUNBUFFERED=true
  "TERM=${TERM:-dumb}"
  "HOME=$fdroid_home"
  GOTOOLCHAIN=local
  "NO_PROXY=$NO_PROXY"
  "no_proxy=$no_proxy"
  "REQUESTS_CA_BUNDLE=$reference_ca_bundle"
)
run_fdroid_vagrant() {
  sudo --preserve-env --user vagrant "${fdroid_environment[@]}" fdroid "$@"
}

collect_native_diagnostics() {
  local status=$1
  local diagnostic_failures=()
  [[ -n "$diagnostic_dir" ]] || return 0
  mkdir -p "$diagnostic_dir"
  {
    echo "fdroid_build_status=$status"
    echo "source_sha=$source_sha"
    echo "version_name=$version_name"
    echo "version_code=$version_code"
    echo "reference_apk=$reference_apk"
    echo "built_apk=$fdroid_home/tmp/com.dobby.vpn_${version_code}.apk"
  } > "$diagnostic_dir/summary.txt"
  if ! cat "$diagnostic_dir/summary.txt" >&2; then
    printf 'F-Droid diagnostic summary could not be emitted\n' >&2
    diagnostic_failures+=("summary output emission")
  fi

  local built_apk="$fdroid_home/tmp/com.dobby.vpn_${version_code}.apk"
  if [[ ! -f "$built_apk" ]]; then
    printf 'F-Droid diagnostic built APK is unavailable: %s\n' "$built_apk" >&2
    return 0
  fi

  run_text_diagnostic() {
    local label=$1
    local output=$2
    shift 2
    local command_status
    if "$@" >"$output" 2>&1; then
      command_status=0
    else
      command_status=$?
    fi
    cat "$output" >&2 || {
      printf 'F-Droid diagnostic output could not be emitted: %s\n' "$output" >&2
      diagnostic_failures+=("$label output emission")
    }
    if (( command_status != 0 )); then
      printf 'F-Droid diagnostic command failed (%s, status=%s)\n' "$label" "$command_status" >&2
      diagnostic_failures+=("$label (status $command_status)")
    fi
  }

  run_binary_diagnostic() {
    local label=$1
    local output=$2
    local error_output="${output}.stderr"
    shift 2
    local command_status
    if "$@" >"$output" 2>"$error_output"; then
      command_status=0
    else
      command_status=$?
    fi
    cat "$error_output" >&2 || {
      printf 'F-Droid diagnostic error output could not be emitted: %s\n' "$error_output" >&2
      diagnostic_failures+=("$label error output emission")
    }
    if (( command_status != 0 )); then
      printf 'F-Droid diagnostic command failed (%s, status=%s)\n' "$label" "$command_status" >&2
      diagnostic_failures+=("$label (status $command_status)")
    fi
  }

  for abi in arm64-v8a x86_64; do
    local reference_so="$diagnostic_dir/reference-${abi}.so"
    local built_so="$diagnostic_dir/fdroid-${abi}.so"
    run_binary_diagnostic "extract reference $abi" "$reference_so" \
      unzip -p "$reference_apk" "lib/$abi/libdobby_vpn.so"
    run_binary_diagnostic "extract built $abi" "$built_so" \
      unzip -p "$built_apk" "lib/$abi/libdobby_vpn.so"
    run_text_diagnostic "hash $abi" "$diagnostic_dir/sha256-${abi}.txt" \
      sha256sum "$reference_so" "$built_so"
    if [[ -s "$reference_so" && -s "$built_so" ]]; then
      # Keep the complete byte comparison. A head pipeline used to discard
      # the tail of a large mismatch, which is often where the useful build
      # provenance difference appears. cmp returns 1 for an expected
      # difference, so handle that status explicitly without suppressing its
      # output.
      run_text_diagnostic "compare $abi" "$diagnostic_dir/cmp-${abi}.txt" \
        cmp -l "$reference_so" "$built_so"
      run_text_diagnostic "reference sections $abi" "$diagnostic_dir/reference-${abi}.sections.txt" \
        readelf -S "$reference_so"
      run_text_diagnostic "built sections $abi" "$diagnostic_dir/fdroid-${abi}.sections.txt" \
        readelf -S "$built_so"
      run_text_diagnostic "reference comment $abi" "$diagnostic_dir/reference-${abi}.comment.txt" \
        readelf -p .comment "$reference_so"
      run_text_diagnostic "built comment $abi" "$diagnostic_dir/fdroid-${abi}.comment.txt" \
        readelf -p .comment "$built_so"
      # Keep the complete string table. Filtering it through grep used to
      # discard non-path strings that can identify the first divergent build
      # stage, while still leaving operators without the original bytes.
      run_text_diagnostic "reference strings $abi" "$diagnostic_dir/reference-${abi}.paths.txt" \
        strings -a "$reference_so"
      run_text_diagnostic "built strings $abi" "$diagnostic_dir/fdroid-${abi}.paths.txt" \
        strings -a "$built_so"
    fi
  done
  if ((${#diagnostic_failures[@]} > 0)); then
    printf 'F-Droid secondary diagnostic failures: %s\n' "${diagnostic_failures[*]}" >&2
  fi
}

mode="$(python3 "$metadata_helper" prepare \
  --metadata "$metadata_path" \
  --baseline "$baseline_path" \
  --version-name "$version_name" \
  --version-code "$version_code")"
if [[ "$mode" == candidate ]]; then
  (
    cd "$fdroiddata_dir"
    "${fdroid_environment[@]}" python3 "$metadata_helper" autoupdate \
      --metadata "$metadata_path" \
      --version-name "$version_name" \
      --version-code "$version_code"
  )
elif [[ "$mode" != existing ]]; then
  echo "metadata helper returned unknown mode: $mode" >&2
  exit 1
fi

python3 "$metadata_helper" finalize \
  --metadata "$metadata_path" \
  --baseline "$baseline_path" \
  --mode "$mode" \
  --version-name "$version_name" \
  --version-code "$version_code" \
  --source-sha "$source_sha" \
  --binary-url "https://127.0.0.1:8765/DobbyVPN-v%v-sign.apk"

rm -rf "$fdroid_home/build" "$fdroid_home/metadata" "$fdroid_home/tmp" "$fdroid_home/logs"
mkdir -p "$fdroid_home/build" "$fdroid_home/metadata" "$fdroid_home/tmp" "$fdroid_home/logs"
cp "$metadata_path" "$fdroid_home/metadata/com.dobby.vpn.yml"
if [[ -f "$fdroiddata_dir/config.yml" ]]; then
  cp "$fdroiddata_dir/config.yml" "$fdroid_home/config.yml"
fi
rm -rf "$fdroid_home/srclibs" "$fdroid_home/fdroiddata"
ln -s "$fdroiddata_dir/srclibs" "$fdroid_home/srclibs"
ln -s "$fdroiddata_dir" "$fdroid_home/fdroiddata"

apt-get install -y sudo openjdk-21-jdk-headless
chown -R vagrant "$fdroid_home/build" "$fdroid_home/metadata" "$fdroid_home/tmp" "$fdroid_home/logs"

(
  cd "$fdroid_home"
  run_fdroid_vagrant fetchsrclibs "com.dobby.vpn:${version_code}" --verbose
)
rm "$fdroid_home/fdroiddata"

(
  cd "$fdroid_home"
  unset CI
  run_fdroid_vagrant build \
    --verbose \
    --test \
    --refresh-scanner \
    --on-server \
    --no-tarball \
    "com.dobby.vpn:${version_code}"
) || {
  build_status=$?
  collect_native_diagnostics "$build_status"
  exit "$build_status"
}

apk_path="$fdroid_home/tmp/com.dobby.vpn_${version_code}.apk"
binary_path="$fdroid_home/tmp/binaries/com.dobby.vpn_${version_code}.binary.apk"
test -f "$apk_path"
test -f "$binary_path"

apt-get install -y sudo
run_fdroid_vagrant scanner --exit-code "$apk_path"

echo "F-Droid compatibility check passed for com.dobby.vpn:${version_code}"
