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
CLI_LINK="/usr/local/bin/dobby-cli"
extensions="$APP_BUNDLE/Contents/Library/SystemExtensions"

# A partially removed service can still have active Network Extension
# preferences. Keep its signed removal tool until the installer's account
# has removed those preferences. Never infer that account from the console.
owner_uid=""
if [ -f "$PLIST_DEST" ]; then
    owner_uid="$(/usr/libexec/PlistBuddy -c 'Print :EnvironmentVariables:DOBBYVPN_CONTROL_PEER_UID' "$PLIST_DEST")"
elif [ -d "$extensions" ]; then
    owner_uid="${DOBBYVPN_CONTROL_PEER_UID:-}"
fi
if [[ -f "$PLIST_DEST" || -d "$extensions" ]] && ! [[ "$owner_uid" =~ ^[1-9][0-9]*$ ]]; then
    echo "Cannot identify the installed DobbyVPN owner. For a missing service plist, supply the installing account's numeric DOBBYVPN_CONTROL_PEER_UID to uninstall." >&2
    exit 1
fi

capture_command launchctl-print launchctl print system/com.dobby.vpnservice
service_state="$CAPTURE_OUTPUT"
service_status="$CAPTURE_STATUS"
if [ "$service_status" -eq 0 ]; then
    launchctl bootout system/com.dobby.vpnservice
    service_removed=""
    poll_count=0
    poll_limit=50
    if [ -n "${DOBBYVPN_COMMAND_DEADLINE_MONOTONIC:-}" ]; then
        # The supervised guest owns the absolute deadline and process-group
        # cleanup. Keep polling until it ends this uninstall command.
        poll_limit=0
    fi
    while [ "$poll_limit" -eq 0 ] || [ "$poll_count" -lt "$poll_limit" ]; do
        poll_count=$((poll_count + 1))
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
    if [ -z "$service_removed" ] && [ "$poll_limit" -ne 0 ]; then
        echo "Timed out waiting for com.dobby.vpnservice removal" >&2
        exit 1
    fi
else
    case "$service_state" in
        *"Could not find service"*) ;;
        *) printf 'launchctl service lookup failed (status %s)\n' "$service_status" >&2; exit "$service_status" ;;
    esac
fi

# The daemon is stopped. Its migration cleanup verifies the saved token and
# current pf reference before releasing the legacy installation's anchor.
if [[ -e "$CONTROL_DIRECTORY/pf.token" || -L "$CONTROL_DIRECTORY/pf.token" ]]; then
    "$APP_BUNDLE/Contents/Resources/dobbyvpn-backend" -mode release-legacy-protection
fi

if [ -n "$owner_uid" ]; then
    if [ -d "$extensions" ]; then
        echo "Removing DobbyVPN Network Extension preferences and registrations" >&2
        /bin/launchctl asuser "$owner_uid" /usr/bin/sudo -n -H -u "#$owner_uid" \
            "$APP_BUNDLE/Contents/MacOS/DobbyVPNMacApp" --network-extension uninstall
        protection="/Library/Application Support/DobbyVPN/Protection"
        if [ -d "$protection" ] && [ ! -L "$protection" ]; then
            rm -f "$protection/tunnel.lock"
            /usr/bin/find "$protection" -maxdepth 1 -type s -name '*.sock' -delete
            rmdir "$protection"
        fi
    fi
    capture_command remove-configuration /usr/bin/sudo -n -H -u "#$owner_uid" /bin/sh -eu -c '
        cd -P -- "$HOME"
        for part in .dobbyvpn configs; do
            if [ -L "$part" ]; then echo "Saved configuration directory is a symlink" >&2; exit 1; fi
            if [ ! -e "$part" ]; then exit 0; fi
            cd -P -- "$part"
        done
        /bin/rm -f -- connection-url.txt.activation.json connection-url.txt accepted-configuration.v1.json
    '
    if [ "$CAPTURE_STATUS" -ne 0 ]; then exit "$CAPTURE_STATUS"; fi
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
# Remove the PATH link only while it still points at this application; a link
# that another tool re-pointed is not ours to delete.
if [ -L "$CLI_LINK" ] && [ "$(readlink "$CLI_LINK")" = "$APP_BUNDLE/Contents/Resources/dobby-cli" ]; then
    rm -f "$CLI_LINK"
fi
rm -rf "$APP_BUNDLE"
rm -f "$UNINSTALLER"
