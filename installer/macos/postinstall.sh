#!/bin/bash

LOGGED_IN_USER=$(stat -f "%Su" /dev/console)

if [ -z "$LOGGED_IN_USER" ] || [ "$LOGGED_IN_USER" = "root" ]; then
    echo "No GUI user logged in, skipping LaunchAgent registration"
    exit 0
fi

USER_ID=$(id -u "$LOGGED_IN_USER")
USER_HOME=$(dscl . -read /Users/"$LOGGED_IN_USER" NFSHomeDirectory | awk '{print $2}')

LAUNCH_AGENTS_DIR="$USER_HOME/Library/LaunchAgents"
PLIST_SRC="/Applications/Dobby VPN.app/Contents/Resources/vpnservice.plist"
PLIST_DEST="$LAUNCH_AGENTS_DIR/vpnservice.plist"

mkdir -p "$LAUNCH_AGENTS_DIR"
cp "$PLIST_SRC" "$PLIST_DEST"
chown "$LOGGED_IN_USER" "$PLIST_DEST"

chmod +x "/Applications/Dobby VPN.app/Contents/Resources/macos_grpcvpnserver"

# Unload existing service in case of reinstall, ignore errors
launchctl bootout gui/"$USER_ID" "$PLIST_DEST" 2>/dev/null || true

launchctl bootstrap gui/"$USER_ID" "$PLIST_DEST"
