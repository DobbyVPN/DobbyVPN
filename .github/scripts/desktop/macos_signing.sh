#!/bin/bash
# Developer ID signing and notarization for distributed macOS packages.
#
#   prepare              temporary keychain, both .p12 identities, env for later steps
#   sign-app <app>       sign nested executables, then the bundle (hardened runtime)
#   sign-package <pkg>   productsign, notarize, staple and verify the package in place
#   cleanup              delete the temporary keychain and key material
#
# Identities are selected by certificate type from the temporary keychain only.
set -euo pipefail

WORK="${RUNNER_TEMP:?RUNNER_TEMP is required}/dobbyvpn-macos-signing"
KEYCHAIN="$WORK/signing.keychain-db"
REQUIRED=(
  MACOS_DEVELOPER_ID_APP_P12
  MACOS_HOST_PROFILE
  MACOS_TUNNEL_PROFILE
  MACOS_FILTER_PROFILE
  MACOS_DEVELOPER_ID_INSTALLER_P12
  MACOS_DEVELOPER_ID_P12_PASSWORD
  APP_STORE_API_KEY
  APP_STORE_KEY_ID
  APP_STORE_ISSUER_ID
)

fail() { echo "::error::$*" >&2; exit 1; }

# Prints "<sha1>\t<name>" for the single valid identity of the given type.
select_identity() {
  local type="$1" matches
  matches="$(security find-identity -v -p basic "$KEYCHAIN" |
    awk -v prefix="\"$type: " 'index($0, prefix) { name = substr($0, index($0, "\"") + 1); sub(/"$/, "", name); print $2 "\t" name }')"
  [[ -n "$matches" ]] || fail "no valid \"$type\" identity in the imported .p12 files"
  [[ "$(printf '%s\n' "$matches" | wc -l | tr -d ' ')" == 1 ]] || fail "more than one \"$type\" identity in the imported .p12 files"
  printf '%s\n' "$matches"
}

mask_identity() {
  local name="$1" team
  echo "::add-mask::$name"
  team="${name##*(}"
  team="${team%)}"
  [[ -n "$team" && "$team" != "$name" ]] && echo "::add-mask::$team"
  return 0
}

prepare() {
  local present=0 missing=() name password original
  for name in "${REQUIRED[@]}"; do
    if [[ -n "${!name:-}" ]]; then present=$((present + 1)); else missing+=("$name"); fi
  done
  if (( present == 0 )); then
    # Only forks and pull requests may skip; a Build in this repository must
    # sign, so a Release (which selects a successful main Build) is signed.
    if [[ "${REPOSITORY_IS_FORK:-}" != true && "${GITHUB_EVENT_NAME:-}" != pull_request ]]; then
      fail "macOS signing secrets are missing; only forks and pull requests may build unsigned release packages"
    fi
    echo "macOS signing skipped: Developer ID and notarization secrets are not available to this run (fork or external pull request). The package stays unsigned."
    echo "enabled=false" >> "$GITHUB_OUTPUT"
    return 0
  fi
  (( ${#missing[@]} == 0 )) || fail "macOS signing secrets are incomplete; missing: ${missing[*]}"

  rm -rf "$WORK"
  mkdir -m 700 -p "$WORK"
  password="$(openssl rand -hex 24)"
  echo "::add-mask::$password"
  security create-keychain -p "$password" "$KEYCHAIN"
  security set-keychain-settings -lut 7200 "$KEYCHAIN"
  security unlock-keychain -p "$password" "$KEYCHAIN"

  security list-keychains -d user | sed -e 's/^[[:space:]]*"//' -e 's/"$//' > "$WORK/search-list"
  original=()
  while IFS= read -r name; do [[ -n "$name" ]] && original+=("$name"); done < "$WORK/search-list"
  security list-keychains -d user -s "$KEYCHAIN" ${original[@]+"${original[@]}"}

  # Developer ID intermediates, in case an exported .p12 holds only the leaf.
  for name in DeveloperIDG2CA DeveloperIDCA; do
    curl -fsSL --retry 3 -o "$WORK/$name.cer" "https://www.apple.com/certificateauthority/$name.cer"
    security import "$WORK/$name.cer" -k "$KEYCHAIN"
  done

  for name in MACOS_DEVELOPER_ID_APP_P12 MACOS_DEVELOPER_ID_INSTALLER_P12; do
    (umask 077; printf '%s' "${!name}" | tr -d '\r\n ' | base64 --decode > "$WORK/identity.p12")
    security import "$WORK/identity.p12" -k "$KEYCHAIN" -f pkcs12 \
      -P "$MACOS_DEVELOPER_ID_P12_PASSWORD" \
      -T /usr/bin/codesign -T /usr/bin/productsign
    rm -f "$WORK/identity.p12"
  done
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$password" "$KEYCHAIN"

  local role key
  for role in host provider filter; do
    case "$role" in
      host) key=MACOS_HOST_PROFILE ;;
      provider) key=MACOS_TUNNEL_PROFILE ;;
      filter) key=MACOS_FILTER_PROFILE ;;
    esac
    (umask 077; printf '%s' "${!key}" | base64 --decode > "$WORK/$role-MAC_APP_DIRECT.provisionprofile")
  done
  local app installer
  app="$(select_identity "Developer ID Application")"
  installer="$(select_identity "Developer ID Installer")"
  mask_identity "${app#*$'\t'}"
  mask_identity "${installer#*$'\t'}"
  {
    echo "MACOS_SIGNING_SCRIPT=$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
    echo "MACOS_SIGNING_KEYCHAIN=$KEYCHAIN"
    echo "MACOS_APP_IDENTITY=${app%%$'\t'*}"
    echo "MACOS_INSTALLER_IDENTITY=${installer#*$'\t'}"
  } >> "$GITHUB_ENV"
  echo "enabled=true" >> "$GITHUB_OUTPUT"
  echo "macOS signing prepared: Developer ID Application and Developer ID Installer identities imported."
}

sign_app() {
  python3 "$(dirname "$0")/sign_macos_extensions.py" "$1" --profiles "$WORK" \
    --identity "${MACOS_APP_IDENTITY:?}" --keychain "${MACOS_SIGNING_KEYCHAIN:?}"
}

# Prints a labelled check; a non-zero exit fails the job through set -e.
check() {
  echo "== $1"
  shift
  "$@" 2>&1
}

notarize() {
  local package="$1" key="$WORK/AuthKey.p8" result="$WORK/notary.json" status=0 id state
  (umask 077; printf '%s\n' "${APP_STORE_API_KEY//\\n/$'\n'}" > "$key")
  xcrun notarytool submit "$package" --key "$key" --key-id "$APP_STORE_KEY_ID" \
    --issuer "$APP_STORE_ISSUER_ID" --wait --timeout 30m --output-format json > "$result" || status=$?
  cat "$result"
  id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id", ""))' "$result" 2>/dev/null || true)"
  state="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status", ""))' "$result" 2>/dev/null || true)"
  echo "== notarytool submission ${id:-unknown}: status ${state:-unknown} (exit $status)"
  if [[ "$state" != Accepted || "$status" != 0 ]]; then
    [[ -z "$id" ]] || xcrun notarytool log "$id" --key "$key" --key-id "$APP_STORE_KEY_ID" --issuer "$APP_STORE_ISSUER_ID" || true
    fail "notarization was not accepted"
  fi
}

sign_package() {
  local package="$1" expanded apps
  [[ -f "$package" ]] || fail "package not found: $package"
  mv "$package" "$package.unsigned"
  productsign --sign "${MACOS_INSTALLER_IDENTITY:?}" --keychain "${MACOS_SIGNING_KEYCHAIN:?}" \
    --timestamp "$package.unsigned" "$package"
  rm -f "$package.unsigned"
  notarize "$package"
  xcrun stapler staple "$package"
  xcrun stapler validate "$package"

  check "pkgutil --check-signature" pkgutil --check-signature "$package"
  check "spctl -a -vv -t install (package)" spctl -a -vv -t install "$package"
  expanded="$WORK/expanded"
  rm -rf "$expanded"
  pkgutil --expand-full "$package" "$expanded"
  apps=("$expanded"/Payload/Applications/*.app)
  [[ ${#apps[@]} == 1 && -d "${apps[0]}" ]] || fail "expected one app bundle in the package payload"
  check "codesign --verify --strict --deep (app)" \
    codesign --verify --strict --deep --verbose=2 "${apps[0]}"
  check "spctl -a -vv (app)" spctl -a -vv "${apps[0]}"
  check "SHA-256 (verified package)" shasum -a 256 "$package"
}

cleanup() {
  local original=() name status=0
  if [[ -f "$WORK/search-list" ]]; then
    while IFS= read -r name; do [[ -n "$name" ]] && original+=("$name"); done < "$WORK/search-list"
    security list-keychains -d user -s ${original[@]+"${original[@]}"} || status=$?
  fi
  if [[ -e "$KEYCHAIN" ]]; then security delete-keychain "$KEYCHAIN" || status=$?; fi
  rm -rf "$WORK" || status=$?
  if [[ "$status" == 0 ]]; then
    echo "macOS signing keychain and key material removed."
  else
    echo "macOS signing cleanup failed; original command errors precede this message." >&2
  fi
  return "$status"
}

case "${1:-}" in
  prepare) prepare ;;
  sign-app) sign_app "${2:?app bundle path is required}" ;;
  sign-package) sign_package "${2:?package path is required}" ;;
  cleanup) cleanup ;;
  *) echo "usage: $0 prepare|sign-app <app>|sign-package <pkg>|cleanup" >&2; exit 2 ;;
esac
