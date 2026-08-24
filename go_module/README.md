# Go product/runtime

This module owns configuration acquisition and parsing, SessionV2 policy and
generation state, protocol-device construction, routing/TUN/tun2socks
resources, probes, cleanup, local diagnostics, and the native desktop CLI.
The shared Compose UI talks to this layer through the authenticated desktop
SessionV2 transport or the one protocol-neutral mobile binding. Platform code
only supplies the OS VPN callbacks and permission/lifecycle hooks it cannot
provide in Go.

The supported configuration sections are Outline, Outline WebSocket, Xray, and
TrustTunnel. Unsupported sections are reported in the SessionV2 diagnostics
and never reach protocol-device construction.

## Build

```bash
go mod tidy
go mod download
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

### Android

```bash
export ANDROID_HOME=<ANDROID_SDK_PATH>
export ANDROID_SDK_ROOT=$ANDROID_HOME

go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260520154334-0e4426e1883d
go install golang.org/x/mobile/cmd/gobind@v0.0.0-20260520154334-0e4426e1883d
gopath="$(go env GOPATH)"
mkdir -p "$gopath/pkg/gomobile"
export PATH="$gopath/bin:$PATH"
go mod download golang.org/x/mobile

gomobile bind \
  -target=android/arm64,android/amd64 \
  -androidapi=26 \
  -tags=static \
  -javapkg=com.dobby.gomobile \
  -ldflags="-s -w -buildid=" \
  -o dobbyvpn-runtime.aar \
  ./kotlin_exports
```

The Gradle `:app` module runs this `gomobile bind` step automatically before
Android compilation. The generated AAR contains `arm64-v8a` and `x86_64` Go
JNI libraries; the Android app also packages the matching
`libc++_shared.so` runtime for both ABIs.

To verify the generated AAR and debug APK ABI payloads locally, run:

```bash
cd kmp_module
./gradlew :app:verifyDebugNativeAbiPayloads
```

### IOS

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

The script builds one physical-iOS slice and one universal Simulator slice.
Physical packet-tunnel qualification is intentionally not claimed until a real
iPhone is available; the Simulator remains a package/build check. The output
artifact is `DobbyVPNRuntime.xcframework`.

## SessionV2 API

The only product lifecycle surface is SessionV2: capabilities, create/recover,
configure, start, stop, snapshot, ordered `Watch` events, and destroy. It uses
opaque session and command IDs, generation-correlated operations, typed
warnings/failures, and safe profile summaries. Configuration URLs and bytes,
credentials, and endpoints never appear in responses or diagnostics.

The native `dobby-cli` shares this authenticated control channel with the
Compose GUI. It supports `connect`, `connect-profile`, `check-config`,
`disconnect`, `status`, `logs clear`, `external-ip`, and `verify-session`
without starting a JVM. `logs clear` is a local file reset and does not need
the VPN service to be running.

See the canonical [vpnserver.proto](../kmp_module/grpcprotos/src/main/proto/com/dobby/vpnserver/vpnserver.proto)
for the authenticated SessionV2 and local Diagnostics transport.

After editing that proto, regenerate stubs:

**Go** (local `protoc` only — see workspace `AGENTS.md`; do not rely on system install):

```bash
cd go_module
export PATH="$PWD/../../tools/protoc/bin:$(go env GOPATH)/bin:$PATH"
./scripts/regenerate-grpcproto.sh
```

The script copies the canonical proto into `grpcproto/` (gitignored) and runs
the workspace-local `tools/protoc/bin/protoc`. It requires `protoc-gen-go` and
`protoc-gen-go-grpc` in `$(go env GOPATH)/bin`; install those user-local plugins
only when they are absent.

**Kotlin** (from `kmp_module/`, uses Gradle/protobuf plugin — same canonical file):

```bash
cd kmp_module
./gradlew :grpcstub:generateProto
```
