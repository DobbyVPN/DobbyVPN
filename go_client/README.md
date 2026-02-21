# Go Library

VPN protocols multiplatform library. 

On desktop platfotms this lirary is a gRPC server, to run with super user privileges in service to use go code via RPC.

On mobile platforms this library is a `.so` library (on Android) or `.xcframework` library (on IOS) to import into the application to use go code via JNI.

## Build

```bash
cp -r Cloak/internal go_client/modules/Cloak/
go mod tidy
go mod download
```

### Windows

```bash
go build -trimpath -ldflags="-buildid=" -o windows_grpcvpnserver.exe ./desktop_exports/...
```

### Linux/

```bash
go build -trimpath -ldflags="-buildid=" -o ubuntu_grpcvpnserver ./desktop_exports/...
```

### MacOS

```bash
go build -trimpath -ldflags="-buildid=" -o macos_grpcvpnserver ./desktop_exports/...
```

### Android

```bash
go build -v -trimpath -ldflags="-buildid=" -buildvcs=false -buildmode=c-shared -o liboutline.so ./kotlin_exports/...
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
protoc --go_out=../ --go-grpc_out=../ vpnserver.proto
```
