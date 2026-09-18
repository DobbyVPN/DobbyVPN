#!/usr/bin/env bash
# Build the iOS Go/Fyne application and attach the native NetworkExtension
# shell.  The Go executable owns the rendered UI; CommonDI is only the C ABI
# bridge to the containing app's NetworkExtension manager, while tunnel.appex
# owns the packet-tunnel process.
set -euo pipefail

target=${1:-}
output=${2:-}
architecture=${4:-}
if [[ -z "$target" || -z "$output" ]]; then
  echo "usage: $0 ios|iossimulator OUTPUT [runtime-xcframework] [simulator-architecture]" >&2
  exit 2
fi
case "$target" in
  ios) sdk=iphoneos; fyne_target=ios; device=1 ;;
  iossimulator)
    sdk=iphonesimulator; device=0; fyne_target=iossimulator
    case "$architecture" in arm64) xcode_arch=arm64 ;; amd64) xcode_arch=x86_64 ;; "") xcode_arch="" ;; *) echo "unsupported Simulator architecture: $architecture" >&2; exit 2 ;; esac
    ;;
  *) echo "unsupported iOS target: $target" >&2; exit 2 ;;
esac

script_root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
swift_root="$script_root/swift_module"
go_root="$script_root/go_module"
runtime=${3:-"$swift_root/DobbyVPNRuntime.xcframework"}
version=${VERSION_NAME:-$(tr -d '[:space:]' < "$script_root/VERSION")}
build=${APP_BUILD:-1005001}
source_commit=${SOURCE_COMMIT:-$(git -C "$script_root" rev-parse HEAD)}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "VERSION_NAME must be x.y.z" >&2; exit 2; }
[[ "$build" =~ ^[1-9][0-9]*$ ]] || { echo "APP_BUILD must be a positive integer" >&2; exit 2; }
[[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || { echo "SOURCE_COMMIT must be a full commit SHA" >&2; exit 2; }
[[ -d "$runtime" ]] || { echo "DobbyVPNRuntime.xcframework is unavailable: $runtime" >&2; exit 2; }
command -v xcodebuild >/dev/null || { echo "xcodebuild is required" >&2; exit 2; }
command -v codesign >/dev/null || { echo "codesign is required" >&2; exit 2; }

if [[ "$device" == 1 ]]; then
  identity=${IOS_SIGNING_IDENTITY:-Apple\ Distribution}
  certificate=${IOS_CERTIFICATE_NAME:-Apple\ Distribution}
  profile=${IOS_PROFILE_NAME:-DobbyVPNAppStore}
  tunnel_profile=${IOS_TUNNEL_PROFILE_NAME:-DobbyVPNTunnelAppStore}
  team_id=${IOS_TEAM_ID:-${APPLE_TEAM_ID:-}}
  [[ -n "$team_id" ]] || { echo "IOS_TEAM_ID or APPLE_TEAM_ID is required for a device IPA" >&2; exit 2; }
  [[ "$team_id" =~ ^[A-Za-z0-9]+$ ]] || { echo "IOS_TEAM_ID must contain only letters and digits" >&2; exit 2; }
fi

if [[ "$device" == 0 && -n "$architecture" ]]; then
  go_arch=$(go env GOARCH)
  if [[ "$go_arch" != "$architecture" ]]; then
    echo "Fyne's iossimulator packager builds the host Go architecture ($go_arch), requested $architecture" >&2
    echo "run this lane on a matching macOS runner or invoke it through the matching Go toolchain" >&2
    exit 2
  fi
fi

derived=$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-ios-go-ui.XXXXXX")
payload=""
copied_runtime=0
native_framework_dir="$go_root/native-ios"
staged_native_framework=0
cleanup() {
  rm -rf "$derived"
  if [[ "$staged_native_framework" == 1 ]]; then
    rm -rf "$native_framework_dir"
  fi
  if [[ "$copied_runtime" == 1 ]]; then
    rm -rf "$swift_root/DobbyVPNRuntime.xcframework"
  fi
  if [[ -n "$payload" ]]; then
    rm -rf "$payload"
  fi
}
trap cleanup EXIT
mkdir -p "$swift_root"
if [[ ! -e "$swift_root/DobbyVPNRuntime.xcframework" ]]; then
  cp -R "$runtime" "$swift_root/DobbyVPNRuntime.xcframework"
  copied_runtime=1
fi

xcode_args=(
  -project "$swift_root/iosApp.xcodeproj"
  -configuration Release
  -sdk "$sdk"
  # xcodebuild's -derivedDataPath mode requires a scheme.  These are direct
  # target builds so use the equivalent build-directory settings instead;
  # this keeps CommonDI/tunnel isolated without forcing the diagnostic
  # iosApp target into the generated Fyne bundle.
  "SYMROOT=$derived/products"
  "OBJROOT=$derived/intermediates"
  CODE_SIGNING_ALLOWED=NO
  CODE_SIGNING_REQUIRED=NO
  DOBBY_SOURCE_COMMIT="$source_commit"
)
if [[ "$device" == 0 && -n "${xcode_arch:-}" ]]; then
  xcode_args+=(ARCHS="$xcode_arch" ONLY_ACTIVE_ARCH=YES)
fi
# Build by target rather than relying on an Xcode shared-scheme file. The
# repository intentionally keeps only the user-facing iosApp scheme; the
# generated Fyne bundle consumes these two native targets directly.
xcodebuild "${xcode_args[@]}" -target CommonDI build
tunnel_xcode_args=("${xcode_args[@]}")
if [[ "$device" == 1 ]]; then
  # The tunnel target must carry its installed NetworkExtension profile. The
  # containing Fyne app is signed by Fyne below; CommonDI is signed when it is
  # embedded. Keeping the profile selection explicit avoids Xcode silently
  # choosing a development identity.
  tunnel_xcode_args+=(
    CODE_SIGNING_ALLOWED=YES
    CODE_SIGNING_REQUIRED=YES
    "CODE_SIGN_IDENTITY=$identity"
    "DEVELOPMENT_TEAM=$team_id"
    "PROVISIONING_PROFILE_SPECIFIER=$tunnel_profile"
  )
fi
xcodebuild "${tunnel_xcode_args[@]}" -target tunnel build

common_framework="$derived/products/Release-$sdk/CommonDI.framework"
tunnel_product="$derived/products/Release-$sdk/tunnel.appex"
[[ -d "$common_framework" ]] || { echo "CommonDI.framework was not built" >&2; exit 1; }
[[ -d "$tunnel_product" ]] || { echo "tunnel.appex was not built" >&2; exit 1; }

# Fyne's iOS builder supplies its own per-architecture environment and drops
# caller-provided CGO_LDFLAGS. The Go UI package therefore links through this
# source-relative staging directory, which is removed by the trap above.
rm -rf "$native_framework_dir"
mkdir -p "$native_framework_dir"
cp -R "$common_framework" "$native_framework_dir/CommonDI.framework"
staged_native_framework=1

# Fyne 2.8 reuses the mobile toolchain's cache directory as a readiness
# check, even though its iOS packager does not invoke gomobile or gobind. Keep
# that cache private and empty here; running `gomobile init` would install an
# unpinned toolchain and is unnecessary for the Go/Fyne executable.
gomobile_cache="$(go env GOPATH)/pkg/gomobile"
mkdir -p "$gomobile_cache"

fyne_security_path=""
if [[ "$device" == 0 ]]; then
  # Fyne 2.8 asks macOS security for a team ID even when the final Simulator
  # bundle is ad-hoc signed. A temporary self-signed certificate gives the
  # packager the metadata it needs; no Apple Development certificate or
  # provisioning profile is required for Simulator builds.
  cert_dir="$derived/simulator-cert"
  mkdir -p "$cert_dir"
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=Dobby Simulator/O=DobbyVPN/OU=SIMULATOR" \
    -keyout "$cert_dir/key.pem" -out "$cert_dir/cert.pem" >/dev/null 2>&1
  cat > "$cert_dir/security" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "find-certificate" && "${2:-}" == "-c" ]]; then
  cat "${DOBBY_SIMULATOR_CERT:?}"
  exit 0
fi
exec /usr/bin/security "$@"
SH
  chmod 700 "$cert_dir/security"
  fyne_security_path="$cert_dir"
fi

fyne_output="$go_root/cmd/dobbyui/Dobby-Vpn.app"
rm -rf "$fyne_output"
fyne_env=(
  SOURCE_COMMIT="$source_commit"
)
fyne_tags=accessibility
if [[ "$device" == 0 ]]; then
  # The Simulator deliberately excludes the physical-only TrustTunnel bridge;
  # without this tag the arm64 Simulator compile would select the device
  # implementation and fail at link/load time.
  # Fyne's --tags flag is a comma-separated list.
  fyne_tags="accessibility,simulator"
fi
if [[ "$device" == 1 ]]; then
  fyne_env+=(IOS_CERTIFICATE_NAME="$certificate" IOS_PROFILE_NAME="$profile")
  fyne_args=(--certificate "$certificate" --profile "$profile" --release)
else
  fyne_args=(--certificate "Dobby Simulator" --profile "")
  # The generated Fyne Xcode project is unsigned first, then its final app is
  # ad-hoc signed below. Fyne 2.8 still invokes xcodebuild with
  # -allowProvisioningUpdates and a synthetic DEVELOPMENT_TEAM even when the
  # profile is empty. That makes Xcode look for an Apple account/profile on a
  # Simulator-only lane. The shim below gives only Fyne's generated app build
  # explicit no-signing settings; the native CommonDI/tunnel builds above keep
  # their normal command line and the final bundle is signed ad hoc here.
  fyne_env+=(CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO)
fi

fyne_command=(go tool fyne package --os "$fyne_target" --name "Dobby Vpn" \
  --app-id vpn.dobby.app --icon "$script_root/assets/logo.png" \
  --app-version "$version" --app-build "$build" --tags "$fyne_tags" "${fyne_args[@]}")
if [[ -n "$fyne_security_path" ]]; then
  xcrun_shim_dir="$derived/fyne-tools"
  mkdir -p "$xcrun_shim_dir"
  cat > "$xcrun_shim_dir/xcrun" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "xcodebuild" ]]; then
  shift
  exec /usr/bin/xcrun xcodebuild "$@" \
    CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO \
    CODE_SIGN_IDENTITY= PROVISIONING_PROFILE_SPECIFIER= \
    DEVELOPMENT_TEAM= CODE_SIGN_STYLE=Manual
fi
exec /usr/bin/xcrun "$@"
SH
  chmod 700 "$xcrun_shim_dir/xcrun"
  (cd "$go_root/cmd/dobbyui" && PATH="$xcrun_shim_dir:$fyne_security_path:$PATH" \
    DOBBY_SIMULATOR_CERT="$derived/simulator-cert/cert.pem" env "${fyne_env[@]}" \
    "${fyne_command[@]}")
else
  (cd "$go_root/cmd/dobbyui" && env "${fyne_env[@]}" "${fyne_command[@]}")
fi
[[ -d "$fyne_output" ]] || { echo "Fyne did not produce $fyne_output" >&2; exit 1; }

app="$fyne_output"
mkdir -p "$app/Frameworks" "$app/PlugIns"
rm -rf "$app/Frameworks/CommonDI.framework" "$app/PlugIns/tunnel.appex"
cp -R "$common_framework" "$app/Frameworks/CommonDI.framework"
cp -R "$tunnel_product" "$app/PlugIns/tunnel.appex"

/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier vpn.dobby.app" "$app/Info.plist" 2>/dev/null || true
/usr/libexec/PlistBuddy -c "Add :DobbySourceCommit string $source_commit" "$app/Info.plist" 2>/dev/null || \
  /usr/libexec/PlistBuddy -c "Set :DobbySourceCommit $source_commit" "$app/Info.plist"

if [[ "$device" == 1 ]]; then
  app_entitlements="$swift_root/iosApp/iosApp.entitlements"
  tunnel_entitlements="$swift_root/tunnel/tunnel.entitlements"
  expanded_app_entitlements="$derived/app.entitlements"
  expanded_tunnel_entitlements="$derived/tunnel.entitlements"
  # The source entitlement files intentionally use Xcode's
  # $(AppIdentifierPrefix) placeholder. Direct codesign does not expand build
  # settings, so make a private, release-only copy with the installed team
  # prefix before signing the Fyne-generated containing app. The Xcode-built
  # tunnel is re-signed with the same expanded values after it is copied.
  sed "s#\$(AppIdentifierPrefix)#$team_id.#g" "$app_entitlements" > "$expanded_app_entitlements"
  sed "s#\$(AppIdentifierPrefix)#$team_id.#g" "$tunnel_entitlements" > "$expanded_tunnel_entitlements"
  codesign --force --sign "$identity" --timestamp --entitlements "$expanded_tunnel_entitlements" "$app/PlugIns/tunnel.appex"
  codesign --force --sign "$identity" --timestamp "$app/Frameworks/CommonDI.framework"
  /usr/libexec/PlistBuddy -c "Add :DobbyKeychainAccessGroup string $team_id.vpn.dobby.app" "$app/Info.plist" 2>/dev/null || \
    /usr/libexec/PlistBuddy -c "Set :DobbyKeychainAccessGroup $team_id.vpn.dobby.app" "$app/Info.plist"
  codesign --force --sign "$identity" --timestamp --entitlements "$expanded_app_entitlements" "$app"
  payload=$(mktemp -d "${TMPDIR:-/tmp}/dobbyvpn-payload.XXXXXX")
  mkdir -p "$payload/Payload"
  cp -R "$app" "$payload/Payload/Dobby-Vpn.app"
  rm -f "$output"
  mkdir -p "$(dirname "$output")"
  (cd "$payload" && /usr/bin/zip -qry "$output" Payload)
else
  # A provisioning-free Simulator app cannot receive the physical target's
  # App Group or keychain-access entitlements.  Keeping those entitlements in
  # an ad-hoc bundle makes SpringBoard reject it at launch (and would make
  # Keychain queries fail with a missing-entitlement status).  The Swift
  # composition root selects its app-owned temporary directory on Simulator;
  # the physical target above remains the only path that uses the provisioned
  # App Group and shared keychain.
  codesign --force --sign - "$app/PlugIns/tunnel.appex"
  codesign --force --sign - "$app/Frameworks/CommonDI.framework"
  /usr/libexec/PlistBuddy -c "Delete :DobbyKeychainAccessGroup" "$app/Info.plist" 2>/dev/null || true
  codesign --force --sign - "$app"
  rm -rf "$output"
  mkdir -p "$(dirname "$output")"
  cp -R "$app" "$output"
fi

echo "iOS Go/Fyne package ready target=$target output=$output source=$source_commit"
