# Go product/runtime

This module owns configuration acquisition and parsing, session policy and
generation state, protocol-device construction, routing/TUN/tun2socks
resources, probes, cleanup, local diagnostics, and the native desktop CLI.
The shared Go/Fyne UI talks to this layer through the authenticated desktop
gRPC service or the one protocol-neutral mobile binding. Platform code
only supplies the OS VPN callbacks and permission/lifecycle hooks it cannot
provide in Go.

The supported configuration sections are Outline (including its WebSocket
transport variant), Xray, and TrustTunnel. Unsupported sections are reported
in session diagnostics and never reach protocol-device construction.

## Build

```bash
go mod tidy
go mod download
```

### Go desktop UI feasibility build

The UI is built on the native target host because Fyne uses the platform
graphics toolchain. Accessibility support is enabled so Windows UI Automation,
macOS XCTest and the mobile accessibility bridges can locate controls by
labels rather than screen coordinates:

```bash
python3 .github/scripts/desktop_build.py ui --platform current
```

The command writes an unbundled executable to `go_module/dobby-vpn-ui` (or
`dobby-vpn-ui.exe` on Windows). It is suitable for disposable native-window
qualification or direct injection into a test VM; Release separately tests
the packaged installer/archive.

Build the headless UI companion on the native target host for service-backed
UI qualification:

```bash
python3 .github/scripts/desktop_build.py ui-test --platform current
```

It drives the production Fyne widgets with the Fyne test driver and speaks a
small private JSON-lines protocol to the shared functional harness. The
companion is not an operating-system input simulator; Windows/macOS release
qualification additionally runs `.github/scripts/native_ui_smoke.py` against
the packaged GUI to inject a real click and close gesture.

Release desktop archives are assembled from those native binaries by the
small, deterministic packager (no JVM or Gradle runtime is involved):

```bash
python3 .github/scripts/package_desktop.py --version 1.5.1 --output output
```

The existing WiX and macOS `pkgbuild` steps consume the generated Windows and
macOS archives; Linux receives the generated Debian package directly.

### Mobile Go/Fyne UI

The shared Fyne screens are the release UI on Android and iOS. Android's plain
`android_module` Gradle project cross-compiles `cmd/dobbyui` directly and
packages the pinned Fyne Java activity:

```bash
cd ../android_module
./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" :app:assembleRelease
```

The Kotlin sources in that project contain only the Android permission,
foreground `VpnService`, TUN allocation, and JNI callback boundary. The
session manager and all visible state remain in Go.

For a real Android emulator, use the matching ABI and then drive the package
through the accessibility tree:

```bash
python3 .github/scripts/mobile_android_ui_smoke.py \
  --apk /tmp/dobby-vpn.apk --profile /path/to/fresh/profile.toml
```

The reviewed tun2socks v2.6.0 dependency closure is likewise tracked under
`go_module/modules/tun2socks`. It contains the upstream correction from
`xjasonlyu/tun2socks#495`, backported without the unrelated post-v2.6.0
networking changes: closing an FD-backed device is idempotent, so stack teardown
cannot close a descriptor number after the operating system has reassigned it.

### Windows

```bash
wget https://github.com/DobbyVPN/go-go-tunnel/releases/download/v1.0.1/dobby_bridge-windows-x86_64.zip
echo "a7e64db0568547d395bc45e33787f22c7303dca6f5c575c84439e73a70124331  dobby_bridge-windows-x86_64.zip" | sha256sum -c -
mkdir -p lib/windows
unzip -j dobby_bridge-windows-x86_64.zip dobby_bridge.dll dobby_bridge.lib -d lib/windows
  go build -trimpath -ldflags="-buildid=" -o dobby-cli.exe ./cmd/dobbyvpn/
```

### Linux

```bash
wget https://github.com/DobbyVPN/go-go-tunnel/releases/download/v1.0.1/libdobby_bridge-linux-x86_64.zip
echo "67536090d74212a5635739d297f5a78fbabda1966d161b12a16bfe487a8c68b9  libdobby_bridge-linux-x86_64.zip" | sha256sum -c -
unzip libdobby_bridge-linux-x86_64.zip
CGO_LDFLAGS="-L." go build -trimpath -ldflags="-buildid=" -o dobby-cli ./cmd/dobbyvpn/
```

Both archives and the Go module tag are bound to go-go-tunnel source commit
`6115b0e372ecf6daed2ae6bf4afe56bef03ef45c`. The release's
`release-assets.manifest.json` is the canonical machine-readable member and
platform-run provenance record.

### MacOS

```bash
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-buildid=" -o dobby-cli-macos-arm64 ./cmd/dobbyvpn/
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-buildid=" -o dobby-cli-macos-amd64 ./cmd/dobbyvpn/
```

With CGO enabled, build each target on its matching macOS runner/toolchain. CI
uses GitHub-hosted `macos-15` for arm64 and `macos-15-intel` for amd64.

### Android runtime and app

```bash
export ANDROID_HOME=<ANDROID_SDK_PATH>
export ANDROID_SDK_ROOT=$ANDROID_HOME

cd ../android_module
./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" :app:assembleRelease
```

The release driver verifies `libdobby_vpn.so` in both ABI payloads. The
TrustTunnel native bridge is linked only for arm64-v8a; x86_64 reports the
typed unsupported-protocol failure.

### iOS runtime and app

```bash
go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260520154334-0e4426e1883d
go install golang.org/x/mobile/cmd/gobind@v0.0.0-20260520154334-0e4426e1883d
gopath="$(go env GOPATH)"
mkdir -p "$gopath/pkg/gomobile"
export PATH="$gopath/bin:$PATH"
go mod download golang.org/x/mobile
./scripts/build_ios_xcframework.sh
```

Do not run `gomobile init` here. It deletes and recreates the shared
`$GOPATH/pkg/gomobile` directory and installs an unpinned gobind tool; the pinned
bootstrap above creates only the required directory and installs both tools at
the exact revision recorded by `go.mod`. The build therefore does not mutate
the module files or resolve an unpinned tool.

With no arguments, the script builds one physical-iOS slice and one universal
Simulator slice for the release XCFramework. A local Simulator check can avoid
the unused slice by selecting its native architecture explicitly:

```bash
./scripts/build_ios_xcframework.sh --simulator-architecture arm64
# or: ./scripts/build_ios_xcframework.sh --simulator-architecture amd64
```

The XCFramework is only the Go NetworkExtension runtime. It is linked by the
Swift tunnel target; the containing app's visible controls are the Go/Fyne
binary packaged by `scripts/package_ios_app.sh`:

```bash
./scripts/build_ios_xcframework.sh --simulator-architecture arm64
./scripts/package_ios_app.sh iossimulator /tmp/Dobby-Vpn.app \
  DobbyVPNRuntime.xcframework arm64
```

Simulator packaging uses temporary ad-hoc signing metadata and does not look
up an Apple Development certificate. Physical-device/App Store packaging uses
the supplied distribution identity and profiles. The Simulator XCTest target
checks real Go/Fyne accessibility actions, keyboard input, visible connection
failure handling, and terminate/reopen lifecycle. Physical packet-tunnel
traffic qualification is intentionally not claimed until a real iPhone is
available; the Simulator does not run the physical NetworkExtension tunnel or
TrustTunnel bridge.

## Session API

The manager owns one process-local session. Clients attach with `Snapshot`;
`Watch` sends a current snapshot and then the latest snapshot after changes.
`ValidateConfig` is stateless and serves the Go CLI's `check-config` and
`profile-inventory` commands. App clients configure directly. `Configure` and
`Start` use an expected snapshot revision, `Stop` uses a generation, and
`Reset` clears configuration after cleanup. Configuration sources,
credentials, and protocol payloads stay out of responses and diagnostics.

The native `dobby-cli` shares this authenticated control channel with the
Go/Fyne GUI. It supports `connect`, `connect-profile`, `profile-inventory`,
`check-config`, `disconnect`, `status`, `logs clear`, `external-ip`, and
`verify-session` without starting a JVM. `profile-inventory` validates a
configuration and returns only the ordered connection indices and protocols;
it and `logs clear` do not need the VPN service to be running.

See the canonical [vpnserver.proto](grpcproto/vpnserver.proto)
for the authenticated session and local Diagnostics transport.

After editing that proto, regenerate stubs:

**Go** (local `protoc` only — see workspace `AGENTS.md`; do not rely on system install):

```bash
cd go_module
export PATH="$PWD/../../tools/protoc/bin:$(go env GOPATH)/bin:$PATH"
./scripts/regenerate-grpcproto.sh
```

The script verifies the canonical proto in `grpcproto/` and runs
the workspace-local `tools/protoc/bin/protoc`. It requires `protoc-gen-go` and
`protoc-gen-go-grpc` in `$(go env GOPATH)/bin`; install those user-local plugins
only when they are absent.
