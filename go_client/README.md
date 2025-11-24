# gRPC tunnels server

## Build and run

### Build executable

```bash
protoc --go_out=../ --go-grpc_out=../ vpnserver.proto
go build -o grpcvpnserver ./desktop_exports/...
```
