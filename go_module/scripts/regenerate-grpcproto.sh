#!/usr/bin/env bash
# Regenerate Go stubs from the repository's canonical Go-owned proto.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_MODULE="$(cd "$SCRIPT_DIR/.." && pwd)"
CANONICAL="$GO_MODULE/grpcproto/vpnserver.proto"

if [[ ! -f "$CANONICAL" ]]; then
  echo "error: canonical proto not found: $CANONICAL" >&2
  exit 1
fi

if ! command -v protoc >/dev/null; then
  echo "error: protoc not found on PATH; install protoc and retry." >&2
  exit 1
fi

for plugin in protoc-gen-go protoc-gen-go-grpc; do
  if ! command -v "$plugin" >/dev/null; then
    echo "error: $plugin not found. Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2" >&2
    exit 1
  fi
done

cd "$GO_MODULE"
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  ./grpcproto/vpnserver.proto

echo "Updated grpcproto/vpnserver.pb.go and vpnserver_grpc.pb.go from $CANONICAL"
