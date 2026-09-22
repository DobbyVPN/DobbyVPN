#!/bin/bash
set -euo pipefail

# This is the only supported macOS uninstall path.  Keep every target fixed to
# the product-owned paths; accepting a path argument here would make a root
# uninstall script unsafe to expose from the installed application.
capture_directory="$(mktemp -d /tmp/dobbyvpn-uninstall.XXXXXX)"
trap 'rm -rf -- "$capture_directory"' EXIT
capture_command() {
    local label="${@:1:1}" stdout_path stderr_path
    shift
    stdout_path="$capture_directory/stdout"
    stderr_path="$capture_directory/stderr"
    set +e
    "$@" >"$stdout_path" 2>"$stderr_path"
    CAPTURE_STATUS=$?
    set -e
    printf '[uninstall %s stdout begin]\n' "$label" >&2
    cat "$stdout_path" >&2
    printf '[uninstall %s stdout end]\n' "$label" >&2
    printf '[uninstall %s stderr begin]\n' "$label" >&2
    cat "$stderr_path" >&2
    printf '[uninstall %s stderr end]\n' "$label" >&2
    CAPTURE_OUTPUT="$(cat "$stdout_path" "$stderr_path")"
}

capture_command id id -u
if [ "$CAPTURE_STATUS" -ne 0 ]; then
    echo "Unable to determine effective uid (status $CAPTURE_STATUS)" >&2
    exit "$CAPTURE_STATUS"
fi
if [ "$CAPTURE_OUTPUT" -ne 0 ]; then
    echo "DobbyVPN uninstall must run as root (use sudo)" >&2
    exit 1
fi

PLIST_DEST="/Library/LaunchDaemons/com.dobby.vpnservice.plist"
APP_BUNDLE="/Applications/Dobby VPN.app"
CONTROL_SOCKET="/var/run/dobbyvpn/control.sock"
CONTROL_DIRECTORY="/var/run/dobbyvpn"
UNINSTALLER="/usr/local/libexec/dobbyvpn-uninstall"

capture_command launchctl-print launchctl print system/com.dobby.vpnservice
service_state="$CAPTURE_OUTPUT"
service_status="$CAPTURE_STATUS"
if [ "$service_status" -eq 0 ]; then
    launchctl bootout system/com.dobby.vpnservice
    service_removed=""
    for _ in {1..50}; do
        capture_command launchctl-removal-probe launchctl print system/com.dobby.vpnservice
        probe_state="$CAPTURE_OUTPUT"
        probe_status="$CAPTURE_STATUS"
        if [ "$probe_status" -eq 0 ]; then
            sleep 0.1
            continue
        fi
        case "$probe_state" in
            *"Could not find service"*) service_removed=1; break ;;
            *) printf 'Unexpected launchctl removal probe failure (status %s)\n' "$probe_status" >&2; exit "$probe_status" ;;
        esac
    done
    if [ -z "$service_removed" ]; then
        echo "Timed out waiting for com.dobby.vpnservice removal" >&2
        exit 1
    fi
else
    case "$service_state" in
        *"Could not find service"*) ;;
        *) printf 'launchctl service lookup failed (status %s)\n' "$service_status" >&2; exit "$service_status" ;;
    esac
fi

# Remove only the product's launchd registration and runtime endpoint.  Do not
# follow symlinks or sweep the parent runtime directory if another owner uses
# it; an empty product directory is removed as a cosmetic final step.
rm -f "$PLIST_DEST" "$CONTROL_SOCKET"
capture_command rmdir rmdir "$CONTROL_DIRECTORY"
rmdir_output="$CAPTURE_OUTPUT"
rmdir_status="$CAPTURE_STATUS"
if [ "$rmdir_status" -ne 0 ]; then
    case "$rmdir_output" in
        *"No such file"*|*"Directory not empty"*) ;;
        *) printf 'Runtime directory removal failed (status %s)\n' "$rmdir_status" >&2; exit "$rmdir_status" ;;
    esac
fi

# Forgetting the receipt makes a subsequent package install a true fresh
# install.  Missing receipts are expected when cleanup is repeated.
capture_command pkgutil-forget pkgutil --forget com.dobby.pkg
pkgutil_output="$CAPTURE_OUTPUT"
pkgutil_status="$CAPTURE_STATUS"
if [ "$pkgutil_status" -ne 0 ]; then
    case "$pkgutil_output" in
        *"No receipt"*|*"not found"*|*"No such file"*) ;;
        *) printf 'Package receipt removal failed (status %s)\n' "$pkgutil_status" >&2; exit "$pkgutil_status" ;;
    esac
fi
rm -rf "$APP_BUNDLE"
rm -f "$UNINSTALLER"
