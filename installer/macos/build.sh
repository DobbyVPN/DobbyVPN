#!/bin/bash
set -euo pipefail

APP_BUNDLE="Dobby VPN.app"

mkdir -p "bin/amd64"
mkdir -p "bin/aarch64"

APP_VERSION="$APP_MAJOR_VERSION.$APP_MINOR_VERSION.$APP_MAINTENANCE_VERSION"

install_service() {
  local service="$1"
  local expected_arch="$2"
  local destination="$3"
  local actual_arches

  actual_arches="$(lipo -archs "$service" | tee /dev/stderr)"
  local found=false architecture
  for architecture in $actual_arches; do
    if [[ "$architecture" == "$expected_arch" ]]; then found=true; fi
  done
  if [[ "$found" != true ]]; then
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

  actual_arches="$(lipo -archs "$helper" | tee /dev/stderr)"
  local found=false architecture
  for architecture in $actual_arches; do
    if [[ "$architecture" == "x86_64" ]]; then found=true; fi
  done
  if [[ "$found" != true ]]; then
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
    test -d "$APP_BUNDLE"

    mkdir Scripts
    cp ../../postinstall.sh Scripts/postinstall
    chmod +x Scripts/postinstall
    cp ../../vpnservice.plist "$APP_BUNDLE/Contents/Resources/"
    install_service \
      "$service_path" \
      "$service_arch" \
      "$APP_BUNDLE/Contents/Resources/dobbyvpn-backend"

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

    mkdir -p Payload/Applications Payload/usr/local/libexec
    cp -R "$APP_BUNDLE" Payload/Applications/
    cp ../../uninstall.sh Payload/usr/local/libexec/dobbyvpn-uninstall
    chmod 755 Payload/usr/local/libexec/dobbyvpn-uninstall
    write_fixed_payload_component_plist
    pkgbuild --root Payload \
             --component-plist component.plist \
             --scripts Scripts \
             --identifier com.dobby.pkg \
             --version "$APP_VERSION" \
             --install-location / \
             "dobbyVPN-macos-$payload_arch.pkg"
  )
}

build_package aarch64 arm64 ../../services/arm64/dobbyvpn-backend
build_package amd64 x86_64 ../../services/amd64/dobbyvpn-backend
