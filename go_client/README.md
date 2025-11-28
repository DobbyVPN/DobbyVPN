# gRPC tunnels server

## Build and run

#### Generate gRPC go files (if needed)

```bash
protoc --go_out=../ --go-grpc_out=../ vpnserver.proto
```

#### Build executable 

```bash
go build -o grpcvpnserver ./desktop_exports/...
```
