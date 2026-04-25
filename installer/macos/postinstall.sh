#!/bin/bash

PLIST_SRC="/Applications/Dobby VPN.app/Contents/Resources/vpnservice.plist"
PLIST_DEST="/Library/LaunchDaemons/com.dobby.vpnservice.plist"

chmod +x "/Applications/Dobby VPN.app/Contents/Resources/macos_grpcvpnserver"

mkdir -p "/Library/LaunchDaemons"
cp "$PLIST_SRC" "$PLIST_DEST"
chown root:wheel "$PLIST_DEST"
chmod 644 "$PLIST_DEST"

# Unload existing service in case of reinstall, ignore errors
launchctl bootout system "$PLIST_DEST" 2>/dev/null || true

launchctl bootstrap system "$PLIST_DEST"
