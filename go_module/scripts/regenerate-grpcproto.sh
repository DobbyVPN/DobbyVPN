#!/usr/bin/env bash
# Copy the canonical vpnserver.proto from KMP grpcprotos, then regenerate Go stubs.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_MODULE="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$GO_MODULE/.." && pwd)"
CANONICAL="$REPO_ROOT/kmp_module/grpcprotos/src/main/proto/com/dobby/vpnserver/vpnserver.proto"
PROTO_DEST="$GO_MODULE/grpcproto/vpnserver.proto"

if [[ ! -f "$CANONICAL" ]]; then
  echo "error: canonical proto not found: $CANONICAL" >&2
  exit 1
fi

cp "$CANONICAL" "$PROTO_DEST"

WORKSPACE_PROTOC="$REPO_ROOT/../tools/protoc/bin/protoc"
if [[ -x "$WORKSPACE_PROTOC" ]]; then
  export PATH="$(dirname "$WORKSPACE_PROTOC"):$(go env GOPATH)/bin:$PATH"
elif ! command -v protoc >/dev/null; then
  echo "error: protoc not found. Use workspace tools/protoc/ (see AGENTS.md)." >&2
  exit 1
fi

for plugin in protoc-gen-go protoc-gen-go-grpc; do
  if ! command -v "$plugin" >/dev/null; then
    echo "error: $plugin not found. Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" >&2
    exit 1
  fi
done

cd "$GO_MODULE"
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  ./grpcproto/vpnserver.proto

echo "Updated grpcproto/vpnserver.pb.go and vpnserver_grpc.pb.go from $CANONICAL"
