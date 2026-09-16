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
http_log="$reference_dir/fdroid-http.log"
server_pid=""

test -d "$fdroiddata_dir"
test -d "$fdroidserver_dir"
test -f "$metadata_path"
test -f "$reference_apk"
test -f "$metadata_helper"

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -f "$baseline_path" "$http_log"
}
trap cleanup EXIT

python3 -m http.server 8765 \
  --bind 127.0.0.1 \
  --directory "$reference_dir" \
  > "$http_log" 2>&1 &
server_pid=$!
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"

for _ in {1..20}; do
  if python3 -c 'import sys, urllib.request; urllib.request.urlopen(sys.argv[1], timeout=2).read(1)' \
    "http://127.0.0.1:8765/$(basename "$reference_apk")" >/dev/null; then
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
if command -v sdkmanager >/dev/null 2>&1; then
  sdkmanager "platform-tools" "build-tools;31.0.0" >/dev/null
fi

fdroid_environment=(
  env
  "PATH=$fdroidserver_dir:$PATH"
  "PYTHONPATH=$fdroidserver_dir:$fdroidserver_dir/examples"
  PYTHONUNBUFFERED=true
  "TERM=${TERM:-dumb}"
  "HOME=$fdroid_home"
  "NO_PROXY=$NO_PROXY"
  "no_proxy=$no_proxy"
)
run_fdroid_vagrant() {
  sudo --preserve-env --user vagrant "${fdroid_environment[@]}" fdroid "$@"
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
  --binary-url "http://127.0.0.1:8765/DobbyVPN-v%v-sign.apk"

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
)

apk_path="$fdroid_home/tmp/com.dobby.vpn_${version_code}.apk"
binary_path="$fdroid_home/tmp/binaries/com.dobby.vpn_${version_code}.binary.apk"
test -f "$apk_path"
test -f "$binary_path"

apt-get install -y sudo
run_fdroid_vagrant scanner --exit-code "$apk_path"

echo "F-Droid compatibility check passed for com.dobby.vpn:${version_code}"
