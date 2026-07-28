#!/bin/bash
set -euo pipefail

mkdir -p "bin/amd64"
mkdir -p "bin/aarch64"

install_service() {
  local service="$1"
  local expected_arch="$2"
  local destination="$3"
  local actual_arches

  actual_arches="$(lipo -archs "$service")"
  if ! tr ' ' '\n' <<<"$actual_arches" | grep -Fxq "$expected_arch"; then
    echo "[!] Refusing to package $service: expected $expected_arch, found $actual_arches" >&2
    exit 1
  fi

  cp "$service" "$destination"
  chmod +x "$destination"
}

install_trusttunnel_helper() {
  local helper="$1"
  local destination="$2"
  local actual_arches

  actual_arches="$(lipo -archs "$helper")"
  if ! tr ' ' '\n' <<<"$actual_arches" | grep -Fxq "x86_64"; then
    echo "[!] Refusing to package TrustTunnel helper: x86_64 slice is missing ($actual_arches)" >&2
    exit 1
  fi

  cp "$helper" "$destination"
  chmod 755 "$destination"
}

echo [+] Extracting dobbyVPN-macos-aarch64.zip
unzip "dobbyVPN-macos-aarch64.zip" -d "bin/aarch64/"

echo [+] Switching workdir to bin/aarch64/
cd bin/aarch64/

echo [+] Making Scripts/ folder
mkdir Scripts
cp ../../postinstall.sh Scripts/postinstall
chmod +x Scripts/postinstall

echo [+] Inserting vpnservice.plist file
cp ../../vpnservice.plist "Dobby Vpn.app/Contents/Resources/"

echo [+] Inserting arm64 macos_grpcvpnserver file
install_service ../../services/arm64/macos_grpcvpnserver arm64 "Dobby Vpn.app/Contents/Resources/macos_grpcvpnserver"

echo [+] Making Payload/ folder
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo [+] Building aarch64 PGK installer
pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         dobbyVPN-macos-aarch64.pkg

cd ../../

echo [+] Extracting dobbyVPN-macos-amd64.zip
unzip "dobbyVPN-macos-amd64.zip" -d "bin/amd64/"

echo [+] Switching workdir to bin/amd64/
cd bin/amd64/

echo [+] Making Scripts/ folder
mkdir Scripts
cp ../../postinstall.sh Scripts/postinstall
chmod +x Scripts/postinstall

echo [+] Inserting vpnservice.plist file
cp ../../vpnservice.plist "Dobby Vpn.app/Contents/Resources/"

echo [+] Inserting amd64 macos_grpcvpnserver file
install_service ../../services/amd64/macos_grpcvpnserver x86_64 "Dobby Vpn.app/Contents/Resources/macos_grpcvpnserver"

echo [+] Inserting verified TrustTunnel helper beside Intel service
install_trusttunnel_helper ../../services/amd64/trusttunnel_client "Dobby Vpn.app/Contents/Resources/trusttunnel_client"

echo [+] Making Payload/ folder
mkdir Payload
cp -R "Dobby Vpn.app" Payload/

echo [+] Building amd64 PGK installer
pkgbuild --root Payload \
         --scripts Scripts \
         --identifier com.dobby.pkg \
         --version $APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION \
         --install-location /Applications \
         dobbyVPN-macos-amd64.pkg

cd ../../
