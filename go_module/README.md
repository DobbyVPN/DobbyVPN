# Go Library

VPN protocols multiplatform library. 

On desktop platfotms this lirary is a gRPC server, to run with super user privileges in service to use go code via RPC.

On mobile platforms this library is a `.so` library (on Android) or `.xcframework` library (on IOS) to import into the application to use go code via JNI.

## Build

```bash
cp -r Cloak/internal go_module/modules/Cloak/
go mod tidy
go mod download
```

### Windows

```bash
go build -trimpath -ldflags="-buildid=" -o windows_grpcvpnserver.exe ./desktop_exports/
```

### Linux/

```bash
go build -trimpath -ldflags="-buildid=" -o ubuntu_grpcvpnserver ./desktop_exports/
```

### MacOS

```bash
go build -trimpath -ldflags="-buildid=" -o macos_grpcvpnserver ./desktop_exports/
```

### Android

```bash
export ANDROID_SDK_ROOT=<ANDROID_SDK_PATH>
export ANDROID_NDK_HOME=$ANDROID_SDK_ROOT/ndk/<ANDROID_NDK_VERSION>
export PATH=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin:$PATH
export DEBUG_PREFIX_FLAGS="-fdebug-prefix-map=$ANDROID_SDK_ROOT=/android-sdk -fdebug-prefix-map=$ANDROID_NDK_HOME=/android-ndk"
export CGO_CFLAGS="$DEBUG_PREFIX_FLAGS ${CGO_CFLAGS:-}"
export CGO_LDFLAGS="$DEBUG_PREFIX_FLAGS ${CGO_LDFLAGS:-}"
export CC=aarch64-linux-android21-clang
export CXX=aarch64-linux-android21-clang++
export CGO_ENABLED="1"
export GOOS="android"
export GOARCH="arm64"

go build -tags linux -ldflags="-buildid=" -v -trimpath -buildvcs=false -o libbackend.so -buildmode c-shared ./kotlin_exports/
```

### IOS

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
go get golang.org/x/mobile/bind@latest
GO111MODULE=on gomobile bind -target=ios -o MyLibrary.xcframework ./ios_exports
```

## RPC API reference

```
// awg.go
rpc StartAwg (StartAwgRequest)  returns (Empty);
rpc StopAwg (Empty)             returns (Empty);

// outline.go
rpc GetOutlineLastError(Empty)          returns (GetOutlineLastErrorResponse);
rpc StartOutline (StartOutlineRequest)  returns (StartOutlineResponse);
rpc StopOutline (Empty)                 returns (Empty);

// health_check.go
rpc StartHealthCheck (StartHealthCheckRequest)    returns (Empty);
rpc StopHealthCheck (Empty)                       returns (Empty);
rpc Status (Empty)                                returns (StatusResponce);
rpc TcpPing (TcpPingRequest)                      returns (TcpPingResponce);
rpc UrlTest (UrlTestRequest)                      returns (UrlTestResponce);
rpc CouldStart (Empty)                            returns (CouldStartResponce);
rpc CheckServerAlive (CheckServerAliveRequest)    returns (CheckServerAliveResponce);

// cloak.go
rpc StartCloakClient (StartCloakClientRequest)    returns (Empty);
rpc StopCloakClient (Empty)                       returns (Empty);

// logger.go
rpc InitLogger (InitLoggerRequest)                returns (Empty);
```

Or this can be found in the [vpnserver.proto](./vpnserver.proto) file, that defines RPC API for the desktop library.

Using this file should be generated required files in the [vpnserver/](./vpnserver/) folder, using this command:

```bash
protoc --go_out=../ --go-grpc_out=../ ./grpcproto/vpnserver.proto
```
