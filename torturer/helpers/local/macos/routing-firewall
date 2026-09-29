#!/bin/sh
# Privileged macOS routing firewall seam for one local qualification run.
set -eu

pf_anchor=com.apple/dobbyvpn-torturer-routing
pf_token=/var/run/dobbyvpn-torturer-pf.token

valid_interface() { case "$1" in ''|*[!A-Za-z0-9._-]*) return 1 ;; esac; }
valid_ipv4() {
  address=$1; old_ifs=$IFS; IFS=.; set -- $address; IFS=$old_ifs
  [ "$#" -eq 4 ] || return 1
  for octet in "$@"; do case "$octet" in ''|*[!0-9]*) return 1 ;; esac; [ "$octet" -le 255 ] || return 1; done
}
[ "$(/usr/bin/id -u)" -eq 0 ] || { echo 'routing-firewall must be invoked through sudo' >&2; exit 2; }

routing_remove() {
  set +e; flush_output=$(/sbin/pfctl -a "$pf_anchor" -F rules 2>&1); flush_status=$?; set -e
  [ -z "$flush_output" ] || printf '%s\n' "$flush_output"; [ "$flush_status" -eq 0 ] || return "$flush_status"
  [ -f "$pf_token" ] || return 0
  token=$(/bin/cat "$pf_token"); case "$token" in ''|*[!0-9]*) return 2 ;; esac
  set +e; release_output=$(/sbin/pfctl -X "$token" 2>&1); release_status=$?; set -e
  [ -z "$release_output" ] || printf '%s\n' "$release_output"; [ "$release_status" -eq 0 ] || return "$release_status"
  /bin/rm -f "$pf_token"
}

case ${1-} in
  routing-block)
    [ "$#" -eq 3 ] || exit 2; interface=$2; address=$3; valid_interface "$interface" || exit 2; valid_ipv4 "$address" || exit 2; routing_remove
    set +e; enable_output=$(/sbin/pfctl -E 2>&1); enable_status=$?; set -e; [ -z "$enable_output" ] || printf '%s\n' "$enable_output"; [ "$enable_status" -eq 0 ] || exit "$enable_status"
    token=$(printf '%s\n' "$enable_output" | /usr/bin/sed -n 's/^Token : \([0-9][0-9]*\)$/\1/p'); case "$token" in ''|*[!0-9]*) exit 2 ;; esac
    umask 077; printf '%s\n' "$token" > "$pf_token"
    set +e; rule_output=$(printf 'block drop out quick on %s inet proto tcp to %s port = 443\n' "$interface" "$address" | /sbin/pfctl -a "$pf_anchor" -f - 2>&1); rule_status=$?; set -e; [ -z "$rule_output" ] || printf '%s\n' "$rule_output"; [ "$rule_status" -eq 0 ] || { routing_remove || true; exit "$rule_status"; }
    ;;
  routing-remove) [ "$#" -eq 1 ] || exit 2; routing_remove ;;
  *) echo 'routing-firewall requires routing-block or routing-remove' >&2; exit 2 ;;
esac
