# Cloak E2E Test Harness

This directory contains an isolated Docker harness for upcoming Cloak e2e tests.

## What is included

- `docker-compose.cloak.e2e.yml`:
  - `cloak-e2e-server` (builds local `ck-server`)
  - `cloak-e2e-http` (upstream test target)
- `ckserver.e2e.json`:
  - deterministic Cloak test config with fixed bind/proxy/keys

## Bring up the harness

From repository root:

- `docker compose -f go_client/e2e/cloak/docker-compose.cloak.e2e.yml up -d --build`
- `docker compose -f go_client/e2e/cloak/docker-compose.cloak.e2e.yml logs cloak-e2e-server --tail 50`

Deterministic client values for tests:

- `UID`: `BvGSsQV96aNGhKh/GQ2A3A==`
- `PublicKey`: `LWsatB8oVpTqOXFF2GK6ugW3wHhfutd5cuHGI6x57i4=`
- Server bind: `127.0.0.1:18445`

## Tear down

- `docker compose -f go_client/e2e/cloak/docker-compose.cloak.e2e.yml down`
