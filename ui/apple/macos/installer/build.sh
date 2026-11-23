#!/bin/bash
set -euo pipefail

APP_BUNDLE="Dobby VPN.app"

mkdir -p "bin/amd64"
mkdir -p "bin/aarch64" "bin/universal"

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
  if [[ "$expected_arch" == universal ]]; then
    [[ "$actual_arches" == "arm64 x86_64" || "$actual_arches" == "x86_64 arm64" ]] && found=true
  fi
  if [[ "$found" != true ]]; then
    echo "[!] Refusing to package $service: expected $expected_arch, found $actual_arches" >&2
    exit 1
  fi

  cp "$service" "$destination"
  chmod +x "$destination"
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
    sed -e "s/@PACKAGE_ARCH@/$service_arch/" -e "s/@PACKAGE_VERSION@/$APP_VERSION/" \
      ../../preinstall.sh > Scripts/preinstall
    chmod +x Scripts/postinstall Scripts/preinstall
    cp ../../vpnservice.plist "$APP_BUNDLE/Contents/Resources/"
    install_service \
      "$service_path" \
      "$service_arch" \
      "$APP_BUNDLE/Contents/Resources/dobbyvpn-backend"

    [[ -x "$APP_BUNDLE/Contents/Resources/dobby-cli" ]] || {
      echo "[!] Native dobby-cli is missing from the macOS bundle" >&2
      exit 1
    }
    if [[ "$payload_arch" == "amd64" || "$payload_arch" == "universal" ]]; then
      ln -s ../Resources/dobby-cli "$APP_BUNDLE/Contents/MacOS/dobby-cli"
    fi

    mkdir -p Payload/Applications Payload/usr/local/libexec Payload/usr/local/bin
    cp -R "$APP_BUNDLE" Payload/Applications/
    # Same path on Intel and Apple silicon: put dobby-cli on the default PATH.
    ln -s "/Applications/$APP_BUNDLE/Contents/Resources/dobby-cli" Payload/usr/local/bin/dobby-cli
    cp ../../uninstall.sh Payload/usr/local/libexec/dobbyvpn-uninstall
    chmod 755 Payload/usr/local/libexec/dobbyvpn-uninstall
    # Normalize only the public staged payload, before signing/packaging. The
    # caller's umask continues to protect private scratch and credential files.
    find Payload -type d -exec chmod 755 {} +
    find Payload -type f -perm -100 -exec chmod 755 {} +
    find Payload -type f ! -perm -100 -exec chmod 644 {} +
    chmod 755 Scripts Scripts/preinstall Scripts/postinstall
    write_fixed_payload_component_plist
    # Release CI sets this after preparing Developer ID signing; local builds stay unsigned.
    if [[ -n "${MACOS_SIGNING_SCRIPT:-}" ]]; then
      /bin/bash "$MACOS_SIGNING_SCRIPT" sign-app "Payload/Applications/$APP_BUNDLE"
    fi
    pkgbuild --root Payload \
             --component-plist component.plist \
             --scripts Scripts \
             --identifier com.dobby.pkg \
             --version "$APP_VERSION" \
             --install-location / \
             --compression latest \
             --min-os-version 12.0 \
             "dobbyVPN-macos-$payload_arch.pkg"
  )
}

case "${1:-all}" in
  universal)
    build_package universal universal ../../services/universal/dobbyvpn-backend
    ;;
  all)
    build_package aarch64 arm64 ../../services/arm64/dobbyvpn-backend
    build_package amd64 x86_64 ../../services/amd64/dobbyvpn-backend
    ;;
  arm64|aarch64)
    build_package aarch64 arm64 ../../services/arm64/dobbyvpn-backend
    ;;
  amd64|x86_64)
    build_package amd64 x86_64 ../../services/amd64/dobbyvpn-backend
    ;;
  *)
    echo "[!] Expected architecture: arm64, amd64, or all" >&2
    exit 2
    ;;
esac
