## Telemetry

Telemetry is provided via OTLP (http one).
Endpoint and authorization token are provided inside incoming toml file.

```toml
[Telemetry] # Optional
Endpoint = "localhost:4318" # Telemetry host shared by all variants
ApiToken = "qwerty-uiop-1234567890" # Ingestion API token
```

It sends every `go_module` log line with connection attributes
(client external IP and connection parameters: `Password`, `DisguisePrefix`, etc)

### Data flow

```text
go_module -- OTLP --> OpenTelemetry Collector --> ClickHouse
```

ClickHouse and OpenTelemetry Collector can be turned on using ClickStack

Telemetry server can be started with [ClickStack](https://clickhouse.com/docs/use-cases/observability/clickstack/getting-started/oss).

### Docker Compose

```yaml
services:
    # DPI mapper
    telemetry-server:
        image: clickhouse/clickstack-all-in-one:latest
        ports:
            - "8123:8123" # ClickHouse
            - "8080:8080" # HyperDX
            - "4317:4317" # gRPC OpenTelemetry Collector
            - "4318:4318" # HTTP OpenTelemetry Collector
        restart: always
        environment:
            - CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1
        volumes:
            - "${PWD}/.volumes/db:/data/db"
            - "${PWD}/.volumes/ch_data:/var/lib/clickhouse"
            - "${PWD}/.volumes/ch_logs:/var/log/clickhouse-server"
```
