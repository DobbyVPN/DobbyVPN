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
wget https://github.com/DobbyVPN/go-go-tunnel/releases/download/v1.0.0/dobby_bridge-windows-x86_64.zip
unzip dobby_bridge-windows-x86_64.zip lib/windows
go build -trimpath -ldflags="-buildid=" -o windows_grpcvpnserver.exe ./desktop_exports/
```

### Linux

```bash
wget https://github.com/DobbyVPN/go-go-tunnel/releases/download/v1.0.0/libdobby_bridge-linux-x86_64.zip
unzip libdobby_bridge-linux-x86_64.zip
CGO_LDFLAGS="-L." go build -trimpath -ldflags="-buildid=" -o ubuntu_grpcvpnserver ./desktop_exports/
```

### MacOS

```bash
go build -trimpath -ldflags="-buildid=" -o macos_grpcvpnserver ./desktop_exports/
```

### Android

```bash
export ANDROID_HOME=<ANDROID_SDK_PATH>
export ANDROID_SDK_ROOT=$ANDROID_HOME

go install golang.org/x/mobile/cmd/gomobile@$(go list -m -f '{{.Version}}' golang.org/x/mobile)
gomobile init

gomobile bind \
  -target=android/arm64 \
  -androidapi=26 \
  -tags=static \
  -javapkg=com.dobby.gomobile \
  -ldflags="-s -w -buildid=" \
  -o backend.aar \
  ./kotlin_exports
```

The Gradle `:app` module runs this `gomobile bind` step automatically before
Android compilation. The generated AAR replaces the previous manual
`libbackend.so` + JNI bridge.

### IOS

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
go get golang.org/x/mobile/bind@latest
GO111MODULE=on gomobile bind -tags=static -target=ios -o MyLibrary.xcframework ./ios_exports
```

## RPC API reference

```
// outline.go
rpc GetOutlineLastError(Empty)          returns (GetOutlineLastErrorResponse);
rpc StartOutline (StartOutlineRequest)  returns (StartOutlineResponse);
rpc StopOutline (Empty)                 returns (Empty);

// xray.go
rpc GetXrayLastError(Empty)        returns (GetXrayLastErrorResponse);
rpc StartXray (StartXrayRequest)   returns (StartXrayResponse);
rpc StopXray (Empty)               returns (Empty);

// health_check.go
rpc CouldStart (Empty)                        returns (CouldStartResponce);
rpc GetConnectionState (Empty)                returns (GetConnectionStateResponce);
rpc InitHealthCheck (Empty)                   returns (Empty);
rpc StartHealthCheck (Empty)                  returns (Empty);
rpc StopHealthCheck (Empty)                   returns (Empty);
rpc MeasureTunnelProbeAverageLatencyMillis (MeasureTunnelProbeRequest) returns (MeasureTunnelProbeResponse);

// cloak.go
rpc StartCloakClient (StartCloakClientRequest)    returns (Empty);
rpc StopCloakClient (Empty)                       returns (Empty);

// trusttunnel.go
rpc GetTrustTunnelLastError(Empty)                returns (GetTrustTunnelLastErrorResponse);
rpc StartTrustTunnel (StartTrustTunnelRequest)    returns (StartTrustTunnelResponse);
rpc StopTrustTunnel (Empty)                       returns (Empty);

// logger.go
rpc InitLogger (InitLoggerRequest)                              returns (Empty);
rpc InitTelemetry (InitTelemetryRequest)                        returns (Empty);
rpc StopTelemetry (Empty)                                       returns (Empty);
rpc SetupTelemetryAttributes (SetupTelemetryAttributesRequest)  returns (Empty);

// georouting.go
rpc SetGeoRoutingConf (SetGeoRoutingConfRequest)  returns (Empty);
rpc ClearGeoRoutingConf (Empty)                   returns (Empty);

// dns_cache.go
rpc ClearDNSCache (Empty)                                  returns (Empty);
rpc SetDNSCacheEntries (SetDNSCacheEntriesRequest)         returns (SetDNSCacheEntriesResponse);
```

Or this can be found in the [vpnserver.proto](./vpnserver.proto) file, that defines RPC API for the desktop library.

Using this file should be generated required files in the [vpnserver/](./vpnserver/) folder, using this command:

```bash
protoc --go_out=../ --go-grpc_out=../ ./grpcproto/vpnserver.proto
```

## Additional documentation

- [How to manage services on different platforms](./SERVICES.md)
