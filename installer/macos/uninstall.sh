#!/bin/bash
set -euo pipefail

# This is the only supported macOS uninstall path.  Keep every target fixed to
# the product-owned paths; accepting a path argument here would make a root
# uninstall script unsafe to expose from the installed application.
if [ "$(id -u)" -ne 0 ]; then
    echo "DobbyVPN uninstall must run as root (use sudo)" >&2
    exit 1
fi

PLIST_DEST="/Library/LaunchDaemons/com.dobby.vpnservice.plist"
APP_BUNDLE="/Applications/Dobby VPN.app"
CONTROL_SOCKET="/var/run/dobbyvpn/control.sock"
CONTROL_DIRECTORY="/var/run/dobbyvpn"
UNINSTALLER="/usr/local/libexec/dobbyvpn-uninstall"

service_state=""
if service_state="$(launchctl print system/com.dobby.vpnservice 2>&1)"; then
    launchctl bootout system/com.dobby.vpnservice
    service_removed=""
    for _ in {1..50}; do
        if launchctl print system/com.dobby.vpnservice >/dev/null 2>&1; then
            sleep 0.1
            continue
        fi
        service_removed=1
        break
    done
    if [ -z "$service_removed" ]; then
        echo "Timed out waiting for com.dobby.vpnservice removal" >&2
        exit 1
    fi
else
    case "$service_state" in
        *"Could not find service"*) ;;
        *) printf '%s\n' "$service_state" >&2; exit 1 ;;
    esac
fi

# Remove only the product's launchd registration and runtime endpoint.  Do not
# follow symlinks or sweep the parent runtime directory if another owner uses
# it; an empty product directory is removed as a cosmetic final step.
rm -f "$PLIST_DEST" "$CONTROL_SOCKET"
rmdir "$CONTROL_DIRECTORY" 2>/dev/null || true

# Forgetting the receipt makes a subsequent package install a true fresh
# install.  Missing receipts are expected when cleanup is repeated.
pkgutil --forget com.dobby.pkg >/dev/null 2>&1 || true
rm -rf "$APP_BUNDLE"
rm -f "$UNINSTALLER"
