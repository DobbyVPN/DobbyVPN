#!/bin/bash
set -euo pipefail

# The installer runs as root; launchd owns the privileged VPN daemon. The
# desktop application remains a normal-user client of its control socket.

RESOURCES="${DOBBYVPN_SERVICE_RESOURCES:-/Applications/Dobby VPN.app/Contents/Resources}"
PLIST_SRC="${DOBBYVPN_SERVICE_PLIST:-$RESOURCES/vpnservice.plist}"
PLIST_DEST="/Library/LaunchDaemons/com.dobby.vpnservice.plist"
CONTROL_SOCKET="/var/run/dobbyvpn/control.sock"

CONSOLE_UID="${DOBBYVPN_CONTROL_PEER_UID:-}"
if [ -z "$CONSOLE_UID" ]; then
    CONSOLE_USER="$(stat -f '%Su' /dev/console)"
    if [ -z "$CONSOLE_USER" ] || [ "$CONSOLE_USER" = "root" ]; then
        echo "Unable to identify the installed desktop user" >&2
        exit 1
    fi
    CONSOLE_UID="$(id -u "$CONSOLE_USER")"
fi

chmod +x "$RESOURCES/macos_grpcvpnserver"
TRUSTTUNNEL_HELPER="$RESOURCES/trusttunnel_client"
if [ -f "$TRUSTTUNNEL_HELPER" ]; then
    chmod 755 "$TRUSTTUNNEL_HELPER"
fi

mkdir -p "/Library/LaunchDaemons"
cp "$PLIST_SRC" "$PLIST_DEST"
# Source-built candidates and installed packages use this same launchd owner.
/usr/libexec/PlistBuddy -c "Set :ProgramArguments:0 $RESOURCES/macos_grpcvpnserver" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Set :WorkingDirectory $RESOURCES" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables dict" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBYVPN_CONTROL_SOCKET string $CONTROL_SOCKET" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBYVPN_CONTROL_PEER_UID string $CONSOLE_UID" "$PLIST_DEST"
if [ -n "${DOBBY_LOG_PATH:-}" ]; then
    /usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBY_LOG_PATH string $DOBBY_LOG_PATH" "$PLIST_DEST"
    /usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBY_LOG_ROOT string $(dirname "$DOBBY_LOG_PATH")" "$PLIST_DEST"
    /usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBY_LOG_PRECREATED string 1" "$PLIST_DEST"
    /usr/libexec/PlistBuddy -c "Set :StandardOutPath ${DOBBY_SERVICE_STDOUT_PATH:-$DOBBY_LOG_PATH.stdout}" "$PLIST_DEST"
    /usr/libexec/PlistBuddy -c "Set :StandardErrorPath ${DOBBY_SERVICE_STDERR_PATH:-$DOBBY_LOG_PATH.stderr}" "$PLIST_DEST"
fi
chown root:wheel "$PLIST_DEST"
chmod 644 "$PLIST_DEST"

if service_state="$(launchctl print system/com.dobby.vpnservice 2>&1)"; then
    launchctl bootout system/com.dobby.vpnservice
    # launchd can reject an immediate bootstrap while bootout is still
    # removing the same label.
    service_removed=""
    for _ in {1..50}; do
        if service_state="$(launchctl print system/com.dobby.vpnservice 2>&1)"; then
            sleep 0.1
            continue
        fi
        case "$service_state" in
            *"Could not find service"*) service_removed=1; break ;;
            *) printf '%s\n' "$service_state" >&2; exit 1 ;;
        esac
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

launchctl bootstrap system "$PLIST_DEST"
