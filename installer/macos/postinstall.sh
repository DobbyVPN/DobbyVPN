#!/bin/bash

cp "/Applications/Dobby VPN.app/Contents/Resources/vpnservice.plist" "$HOME/Library/LaunchAgents/"
launchctl load "$HOME/Library/LaunchAgents/vpnservice.plist"
