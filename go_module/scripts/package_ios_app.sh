#!/usr/bin/env bash
# Build the native SwiftUI iOS app and its NetworkExtension tunnel.
set -euo pipefail

target=${1:-}
output=${2:-}
architecture=${4:-}
if [[ -z "$target" || -z "$output" ]]; then
  echo "usage: $0 ios|iossimulator OUTPUT [runtime-xcframework] [simulator-architecture]" >&2
  exit 2
fi
case "$target" in
  ios) sdk=iphoneos; scheme=iosApp ;;
  iossimulator)
    sdk=iphonesimulator; scheme=iosSimulatorApp
    case "$architecture" in arm64) xcode_arch=arm64 ;; amd64) xcode_arch=x86_64 ;; "") xcode_arch="" ;; *) echo "unsupported Simulator architecture: $architecture" >&2; exit 2 ;; esac
    ;;
  *) echo "unsupported iOS target: $target" >&2; exit 2 ;;
esac

script_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
swift_root="$script_root/swift_module"
runtime=${3:-"$swift_root/DobbyVPNRuntime.xcframework"}
version=${VERSION_NAME:-$(tr -d '[:space:]' < "$script_root/VERSION")}
build=${APP_BUILD:-1005001}
if [[ -n "${SOURCE_COMMIT:-}" ]]; then
  source_commit=$SOURCE_COMMIT
elif source_commit=$(git -C "$script_root" rev-parse HEAD); then
  :
else
  source_commit=0000000000000000000000000000000000000000
  echo "source commit unavailable; using the local-candidate sentinel" >&2
fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "VERSION_NAME must be x.y.z" >&2; exit 2; }
[[ "$build" =~ ^[1-9][0-9]*$ ]] || { echo "APP_BUILD must be a positive integer" >&2; exit 2; }
[[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || { echo "SOURCE_COMMIT must be a full commit SHA" >&2; exit 2; }
if [[ "$target" == ios ]]; then
  [[ -d "$runtime" ]] || { echo "DobbyVPNRuntime.xcframework is unavailable: $runtime" >&2; exit 2; }
fi

for tool in xcodebuild python3; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 2; }
done

team_id=${IOS_TEAM_ID:-${APPLE_TEAM_ID:-}}
if [[ "$target" == ios ]]; then
  [[ -n "$team_id" ]] || { echo "IOS_TEAM_ID or APPLE_TEAM_ID is required for an iOS IPA" >&2; exit 2; }
  [[ "$team_id" =~ ^[A-Za-z0-9]+$ ]] || { echo "IOS_TEAM_ID must contain only letters and digits" >&2; exit 2; }
fi

if [[ "$target" == ios ]]; then
  python3 "$script_root/go_module/scripts/ios_runtime_framework.py" \
    "$target" "$runtime" arm64
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

if [[ "$target" == ios ]]; then
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
fi

xcode_args=(
  -project "$swift_root/iosApp.xcodeproj"
  -scheme "$scheme"
  -configuration Release
  -sdk "$sdk"
  MARKETING_VERSION="$version"
  CURRENT_PROJECT_VERSION="$build"
  DOBBY_SOURCE_COMMIT="$source_commit"
)
if [[ "$target" == iossimulator ]]; then
  derived_data=${output%/Build/Products/Release-iphonesimulator/Dobby-Vpn-Simulator.app}
  [[ "$derived_data" != "$output" ]] || { echo "Simulator output path must be the Xcode app product path" >&2; exit 2; }
  xcode_args+=(-derivedDataPath "$derived_data" CODE_SIGNING_ALLOWED=YES CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY=-)
  if [[ -n "${xcode_arch:-}" ]]; then xcode_args+=(ARCHS="$xcode_arch" ONLY_ACTIVE_ARCH=YES); fi
  xcodebuild "${xcode_args[@]}" build
else
  identity=${IOS_SIGNING_IDENTITY:-Apple\ Distribution}
  archive="$derived/DobbyVPN.xcarchive"
  export_dir="$derived/export"
  xcodebuild "${xcode_args[@]}" \
    -destination "generic/platform=iOS" \
    -archivePath "$archive" \
    "DEVELOPMENT_TEAM=$team_id" \
    "CODE_SIGN_IDENTITY=$identity" \
    CODE_SIGN_STYLE=Manual \
    CODE_SIGNING_ALLOWED=YES \
    CODE_SIGNING_REQUIRED=YES \
    archive
  export_options="$derived/ExportOptions.plist"
  cat > "$export_options" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store</string>
  <key>teamID</key><string>$team_id</string>
  <key>signingStyle</key><string>manual</string>
  <key>manageAppVersionAndBuildNumber</key><false/>
  <key>provisioningProfiles</key><dict>
    <key>vpn.dobby.app</key><string>${IOS_PROFILE_NAME:-DobbyVPNAppStore}</string>
    <key>vpn.dobby.app.tunnel</key><string>${IOS_TUNNEL_PROFILE_NAME:-DobbyVPNTunnelAppStore}</string>
  </dict>
</dict></plist>
PLIST
  xcodebuild -exportArchive -archivePath "$archive" -exportPath "$export_dir" -exportOptionsPlist "$export_options"
  shopt -s nullglob
  ipas=("$export_dir"/*.ipa)
  [[ "${#ipas[@]}" -eq 1 ]] || { echo "Xcode export did not produce exactly one IPA" >&2; exit 1; }
  mkdir -p "$(dirname -- "$output")"
  cp "${ipas[0]}" "$output"
fi
