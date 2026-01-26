#!/bin/bash

echo "[+] Making Payload/ folder"
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo "[+] Making Scripts/ folder"
mkdir Scripts
cp -R postinstall.sh Scripts/

pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         --sign "Developer ID Installer: DobbyVPN team (TEAMID)" \
         DobbyVPN.pkg
