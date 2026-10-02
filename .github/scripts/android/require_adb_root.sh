#!/usr/bin/env bash
set -uo pipefail

if (( $# != 1 )) || [[ ! -d "$1" ]]; then printf 'usage: require_adb_root.sh RUN_DIR\n' >&2; exit 2; fi
run_dir=$1

run_adb() {
  local label=$1 status=0
  shift
  local stdout_file="$run_dir/$label.stdout.log" stderr_file="$run_dir/$label.stderr.log"
  "$@" >"$stdout_file" 2>"$stderr_file" || status=$?
  cat "$stdout_file" || return 125
  cat "$stderr_file" >&2 || return 125
  return "$status"
}

is_root_disconnect() {
  local status=$1 stderr_file=$2
  [[ $status -eq 1 ]] && cmp -s "$stderr_file" \
    <(printf '%s\n' 'adb: unable to connect for root: closed')
}

parse_uid() {
  python3 - "$1" <<'PY'
import pathlib, re, sys
raw = pathlib.Path(sys.argv[1]).read_bytes()
raw = raw[:-2] if raw.endswith(b"\r\n") else raw[:-1] if raw.endswith(b"\n") else raw
if not re.fullmatch(rb"[0-9]+", raw): raise SystemExit(1)
sys.stdout.buffer.write(raw)
PY
}

for attempt in 1 2; do
  run_adb "android-adb-root-$attempt" adb root
  root_status=$?
  disconnected=false
  if (( root_status != 0 )); then
    if is_root_disconnect "$root_status" "$run_dir/android-adb-root-$attempt.stderr.log"; then
      disconnected=true
      printf 'ADB disconnected during root request %s; waiting to verify device state\n' "$attempt" >&2
    else printf 'Android ADB root request %s failed (exit %s)\n' "$attempt" "$root_status" >&2; exit 1; fi
  fi
  run_adb "android-adb-root-wait-$attempt" adb wait-for-device || { printf 'Android ADB did not return after root\n' >&2; exit 1; }
  uid_stdout="$run_dir/android-adb-root-uid-$attempt.stdout.log"
  run_adb "android-adb-root-uid-$attempt" adb shell id -u || { printf 'Android ADB UID probe failed\n' >&2; exit 1; }
  uid_value=$(parse_uid "$uid_stdout") || { printf 'Android ADB UID probe was malformed\n' >&2; exit 1; }
  [[ $uid_value == 0 ]] && exit 0
  if (( attempt == 1 )) && [[ $disconnected == true ]]; then
    printf 'ADB reconnected with uid=%s; repeating the interrupted root request once\n' "$uid_value" >&2
    continue
  fi
  printf 'Android ADB is not root after attempt %s (uid=%s)\n' "$attempt" "$uid_value" >&2
  exit 1
done
