#!/bin/bash

PLIST_SRC="/Applications/Dobby VPN.app/Contents/Resources/vpnservice.plist"
PLIST_DEST="/Library/LaunchDaemons/com.dobby.vpnservice.plist"
CONTROL_SOCKET="/var/run/dobbyvpn/control.sock"

CONSOLE_USER="$(stat -f '%Su' /dev/console)"
if [ -z "$CONSOLE_USER" ] || [ "$CONSOLE_USER" = "root" ]; then
    echo "Unable to identify the installed desktop user" >&2
    exit 1
fi
CONSOLE_UID="$(id -u "$CONSOLE_USER")"

chmod +x "/Applications/Dobby VPN.app/Contents/Resources/macos_grpcvpnserver"
TRUSTTUNNEL_HELPER="/Applications/Dobby VPN.app/Contents/Resources/trusttunnel_client"
if [ -f "$TRUSTTUNNEL_HELPER" ]; then
    chmod 755 "$TRUSTTUNNEL_HELPER"
    chown root:wheel "$TRUSTTUNNEL_HELPER"
fi

mkdir -p "/Library/LaunchDaemons"
cp "$PLIST_SRC" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables dict" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBYVPN_CONTROL_SOCKET string $CONTROL_SOCKET" "$PLIST_DEST"
/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:DOBBYVPN_CONTROL_PEER_UID string $CONSOLE_UID" "$PLIST_DEST"
chown root:wheel "$PLIST_DEST"
chmod 644 "$PLIST_DEST"

# Unload existing service in case of reinstall, ignore errors
launchctl bootout system "$PLIST_DEST" || true

launchctl bootstrap system "$PLIST_DEST"
