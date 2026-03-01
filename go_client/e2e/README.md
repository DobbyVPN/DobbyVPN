# Go E2E Tests

This directory contains Docker-backed end-to-end tests for `go_client`.

## One-command launch

From repository root:

- Linux/macOS:
  - `docker compose -f go_client/e2e/docker-compose.e2e.yml up -d --build && go test -tags=e2e ./go_client/e2e ./go_client/healthcheck && docker compose -f go_client/e2e/docker-compose.e2e.yml down`
- Windows (PowerShell):
  - `docker compose -f go_client/e2e/docker-compose.e2e.yml up -d --build; go test -tags=e2e ./go_client/e2e ./go_client/healthcheck; docker compose -f go_client/e2e/docker-compose.e2e.yml down`

## Step-by-step run

1) Start Docker services:

- `docker compose -f go_client/e2e/docker-compose.e2e.yml up -d --build`

2) Run tests:

- `go test -tags=e2e ./go_client/e2e ./go_client/healthcheck`

3) Stop services:

- `docker compose -f go_client/e2e/docker-compose.e2e.yml down`

## Cloak harness run

- Start Cloak stack:
  - `docker compose -f go_client/e2e/cloak/docker-compose.cloak.e2e.yml up -d --build`
- Run only Cloak e2e:
  - `go test -count=1 -tags=e2e -run TestCloakConnectViaDockerE2E ./go_client/e2e -v`
- Stop Cloak stack:
  - `docker compose -f go_client/e2e/cloak/docker-compose.cloak.e2e.yml down`

## Optional environment variables

- `E2E_DOCKER_HOST` (default: `127.0.0.1`)
- `E2E_DOCKER_HTTP_PORT` (default: `18080`)
- `E2E_URLTEST_STANDARD` (default: `1`)
- `E2E_SS_HOST` (default: `127.0.0.1`)
- `E2E_SS_PORT` (default: `18388`)
- `E2E_SS_METHOD` (default: `aes-256-gcm`)
- `E2E_SS_PASSWORD` (default: `e2e-password`)
- `E2E_SS_TARGET` (default: `172.29.0.10:5678`)
- `E2E_WSS_HOST` (default: `127.0.0.1`)
- `E2E_WSS_PORT` (default: `18443`)
- `E2E_WSS_PATH` (default: `/ws`)

## Troubleshooting

- Docker daemon not running:
  - `docker info`
- Services are not reachable:
  - `docker compose -f go_client/e2e/docker-compose.e2e.yml ps`
  - `docker compose -f go_client/e2e/docker-compose.e2e.yml logs --tail 100`
- Port conflict on host (`18080`, `18388`, `18443`):
  - stop local process using the port or change port mapping in `docker-compose.e2e.yml`
- Run only one test:
  - `go test -tags=e2e -run TestWSSConnectViaDockerE2E ./go_client/e2e -v`
