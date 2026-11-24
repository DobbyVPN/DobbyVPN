#!/usr/bin/env bash
# Build the native SwiftUI iOS app and its NetworkExtension tunnel.
set -euo pipefail

target=${1:-}
output=${2:-}
configuration=${DOBBY_BUILD_CONFIGURATION:-Release}
case "$configuration" in
  Release) ;;
  Debug) [[ "$target" == iosarchive ]] || { echo "Debug supports unsigned iosarchive only" >&2; exit 2; } ;;
  *) echo "Unknown build configuration: $configuration" >&2; exit 2 ;;
esac
if [[ -z "$target" || -z "$output" ]]; then
  echo "usage: $0 ios|iosarchive|iosanalyze|iosexport OUTPUT [runtime-xcframework|archive.tar.gz]" >&2
  exit 2
fi
case "$target" in
  ios|iosarchive|iosanalyze|iosexport) ;;
  *) echo "unsupported iOS target: $target" >&2; exit 2 ;;
esac

script_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
swift_root="$script_root/ui/apple"
runtime=${3:-"$swift_root/DobbyVPNRuntime.xcframework"}
version=${VERSION_NAME:-$(tr -d '[:space:]' < "$script_root/VERSION")}
build=${APP_BUILD:-$(python3 "$script_root/.github/scripts/release/version_metadata.py" --version "$version" --field android_version_code)}
if [[ -n "${SOURCE_COMMIT:-}" ]]; then
  source_commit=$SOURCE_COMMIT
elif [[ "${GITHUB_SHA:-}" =~ ^[0-9a-f]{40}$ ]]; then
  source_commit=$GITHUB_SHA
elif source_commit=$(git -C "$script_root" rev-parse HEAD); then
  :
else
  source_commit=0000000000000000000000000000000000000000
  echo "source commit unavailable; using the local-candidate sentinel" >&2
fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "VERSION_NAME must be x.y.z" >&2; exit 2; }
[[ "$build" =~ ^[1-9][0-9]*$ ]] || { echo "APP_BUILD must be a positive integer" >&2; exit 2; }
[[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || { echo "SOURCE_COMMIT must be a full commit SHA" >&2; exit 2; }
for tool in xcodebuild python3; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 2; }
done

team_id=${IOS_TEAM_ID:-${APPLE_TEAM_ID:-}}
if [[ "$target" == ios || "$target" == iosexport ]]; then
  [[ -n "$team_id" ]] || { echo "IOS_TEAM_ID or APPLE_TEAM_ID is required for an iOS IPA" >&2; exit 2; }
  [[ "$team_id" =~ ^[A-Z0-9]{10}$ ]] || { echo "IOS_TEAM_ID must be ten uppercase letters or digits" >&2; exit 2; }
  profile_name=${IOS_PROFILE_NAME:-DobbyVPNAppStore}
  tunnel_profile_name=${IOS_TUNNEL_PROFILE_NAME:-DobbyVPNTunnelAppStore}
  profile_name_pattern='^[A-Za-z0-9_. -]+$'
  [[ "$profile_name" =~ $profile_name_pattern && "$tunnel_profile_name" =~ $profile_name_pattern ]] || {
    echo "iOS provisioning profile names contain unsupported characters" >&2
    exit 2
  }
fi

if [[ "$target" != iosexport ]]; then
  [[ -d "$runtime" ]] || { echo "DobbyVPNRuntime.xcframework is unavailable: $runtime" >&2; exit 2; }
  python3 "$script_root/core/scripts/ios_runtime_framework.py" \
    ios "$runtime" arm64
fi

archive_tool="$script_root/.github/scripts/ios/ios_archive.py"
metadata=(--source-sha "$source_commit" --version "$version" --build-number "$build")

sign_and_export_ipa() {
  local archive=$1 entitlements_dir=$2
  python3 "$archive_tool" sign-for-export \
    --archive-dir "$archive" --source-entitlements-dir "$entitlements_dir" \
    --output-dir "$derived/signing" --team-id "$team_id" \
    --identity "${IOS_SIGNING_IDENTITY:-Apple Distribution}" "${metadata[@]}"
  local export_options="$derived/ExportOptions.plist"
  local export_dir="$derived/export"
  cat > "$export_options" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store</string>
  <key>teamID</key><string>$team_id</string>
  <key>signingStyle</key><string>manual</string>
  <key>manageAppVersionAndBuildNumber</key><false/>
  <key>provisioningProfiles</key><dict>
    <key>vpn.dobby.app</key><string>$profile_name</string>
    <key>vpn.dobby.app.tunnel</key><string>$tunnel_profile_name</string>
  </dict>
</dict></plist>
PLIST
  xcodebuild -exportArchive -archivePath "$archive" -exportPath "$export_dir" \
    -exportOptionsPlist "$export_options"
  shopt -s nullglob
  local ipas=("$export_dir"/*.ipa)
  [[ "${#ipas[@]}" -eq 1 ]] || { echo "Xcode export did not produce exactly one IPA" >&2; exit 1; }
  python3 "$archive_tool" compress-ipa \
    --ipa "${ipas[0]}"
  python3 "$archive_tool" verify-ipa \
    --ipa "${ipas[0]}" "${metadata[@]}"
  mkdir -p "$(dirname -- "$output")"
  cp "${ipas[0]}" "$output"
}

if [[ "$target" == iosexport ]]; then
  archive_tar=${3:-}
  [[ -n "$archive_tar" && -f "$archive_tar" ]] || { echo "unsigned iOS archive tarball is unavailable: $archive_tar" >&2; exit 2; }
  derived=$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-ios-export.XXXXXX")
  trap 'rm -rf "$derived"' EXIT
  python3 "$archive_tool" extract \
    --input "$archive_tar" --output-dir "$derived/unpacked"
  sign_and_export_ipa "$derived/unpacked/DobbyVPN.xcarchive" "${IOS_ENTITLEMENTS_DIR:-$swift_root/ios}"
  exit 0
fi

derived=$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-ios-native-ui.XXXXXX")
runtime_stage="$derived/DobbyVPNRuntime.xcframework"
fixed_runtime="$swift_root/DobbyVPNRuntime.xcframework"
runtime_backup="$derived/existing-DobbyVPNRuntime.xcframework"
runtime_staged=0
runtime_replaced=0
cleanup() {
  if [[ "$runtime_staged" == 1 ]]; then rm -rf "$fixed_runtime"; fi
  if [[ "$runtime_replaced" == 1 ]]; then mv "$runtime_backup" "$fixed_runtime"; fi
  rm -rf "$derived"
}
trap cleanup EXIT

runtime_real=$(cd -- "$runtime" && pwd -P)
fixed_real=""
if [[ -d "$fixed_runtime" ]]; then fixed_real=$(cd -- "$fixed_runtime" && pwd -P); fi
if [[ "$fixed_real" != "$runtime_real" ]]; then
  cp -R "$runtime" "$runtime_stage"
  if [[ -e "$fixed_runtime" || -L "$fixed_runtime" ]]; then
    mv "$fixed_runtime" "$runtime_backup"
    runtime_replaced=1
  fi
  mv "$runtime_stage" "$fixed_runtime"
  runtime_staged=1
fi

xcode_args=(
  -project "$swift_root/ios/iosApp.xcodeproj"
  -scheme iosApp
  -configuration "$configuration"
  -sdk iphoneos
  MARKETING_VERSION="$version"
  CURRENT_PROJECT_VERSION="$build"
  DOBBY_SOURCE_COMMIT="$source_commit"
  -destination "generic/platform=iOS"
  DEVELOPMENT_TEAM="" CODE_SIGN_IDENTITY="" CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO
)
if [[ "$configuration" == Debug ]]; then
  xcode_args+=(GCC_OPTIMIZATION_LEVEL=0 SWIFT_OPTIMIZATION_LEVEL=-Onone DEBUG_INFORMATION_FORMAT=dwarf-with-dsym STRIP_INSTALLED_PRODUCT=NO COPY_PHASE_STRIP=NO)
fi
if [[ "$target" == iosanalyze ]]; then
  mkdir -p "$output"
  xcodebuild "${xcode_args[@]}" -derivedDataPath "$output" analyze
  exit 0
fi

if [[ "$target" == iosarchive && "$output" != *.tar.gz ]]; then
  echo "unsigned archive output must end in .tar.gz" >&2
  exit 2
fi
archive="$derived/DobbyVPN.xcarchive"
xcodebuild "${xcode_args[@]}" -archivePath "$archive" CODE_SIGN_STYLE=Manual archive
if [[ "$configuration" == Debug ]]; then
  test -d "$archive/dSYMs"
  find "$archive/dSYMs" -type f -print
  test -n "$(find "$archive/dSYMs" -type f -path '*/DWARF/*' -print)"
fi
if [[ "$target" == iosarchive ]]; then
  python3 "$archive_tool" pack --archive-dir "$archive" --output "$output" "${metadata[@]}"
else
  sign_and_export_ipa "$archive" "$swift_root/ios"
fi
