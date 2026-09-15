#!/bin/bash
set -euo pipefail

EXTRACTED_APP_BUNDLE="Dobby Vpn.app"
APP_BUNDLE="Dobby VPN.app"

mkdir -p "bin/amd64"
mkdir -p "bin/aarch64"

APP_VERSION="$APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION"

install_service() {
  local service="$1"
  local expected_arch="$2"
  local destination="$3"
  local actual_arches

  actual_arches="$(lipo -archs "$service")"
  if ! tr ' ' '\n' <<<"$actual_arches" | grep -Fx "$expected_arch"; then
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
  if ! tr ' ' '\n' <<<"$actual_arches" | grep -Fx "x86_64"; then
    echo "[!] Refusing to package TrustTunnel helper: x86_64 slice is missing ($actual_arches)" >&2
    exit 1
  fi

  cp "$helper" "$destination"
  chmod 755 "$destination"
}

write_fixed_payload_component_plist() {
  cat >component.plist <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><array/></plist>
PLIST
}

build_package() {
  local payload_arch="$1"
  local service_arch="$2"
  local service_path="$3"

  echo "[+] Extracting dobbyVPN-macos-$payload_arch.zip"
  unzip "dobbyVPN-macos-$payload_arch.zip" -d "bin/$payload_arch/"

  (
    cd "bin/$payload_arch/"
    echo "[+] Normalizing application bundle name"
    mv "$EXTRACTED_APP_BUNDLE" "$APP_BUNDLE"

    mkdir Scripts
    cp ../../postinstall.sh Scripts/postinstall
    chmod +x Scripts/postinstall
    cp ../../vpnservice.plist "$APP_BUNDLE/Contents/Resources/"
    install_service \
      "$service_path" \
      "$service_arch" \
      "$APP_BUNDLE/Contents/Resources/macos_grpcvpnserver"

    if [[ "$payload_arch" == "amd64" ]]; then
      install_trusttunnel_helper \
        ../../services/amd64/trusttunnel_client \
        "$APP_BUNDLE/Contents/Resources/trusttunnel_client"
      [[ -x "$APP_BUNDLE/Contents/Resources/dobby-cli" ]] || {
        echo "[!] Native dobby-cli is missing from the macOS bundle" >&2
        exit 1
      }
      ln -s ../Resources/dobby-cli "$APP_BUNDLE/Contents/MacOS/dobby-cli"
    fi

    mkdir Payload
    cp -R "$APP_BUNDLE" Payload/
    write_fixed_payload_component_plist
    pkgbuild --root Payload \
             --component-plist component.plist \
             --scripts Scripts \
             --identifier com.dobby.pkg \
             --version "$APP_VERSION" \
             --install-location /Applications \
             "dobbyVPN-macos-$payload_arch.pkg"
  )
}

build_package aarch64 arm64 ../../services/arm64/macos_grpcvpnserver
build_package amd64 x86_64 ../../services/amd64/macos_grpcvpnserver
