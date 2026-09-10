## Telemetry

Remote telemetry has been removed. DobbyVPN writes detailed logs only to local,
owner-restricted log files and its user-initiated local log export. No OTLP
exporter, endpoint, authorization token, external-IP lookup, or telemetry
network request is initialized in production.

Legacy `[Telemetry]` TOML is rejected as an unsupported configuration section.
