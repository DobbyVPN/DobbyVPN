## Telemetry

Remote telemetry has been removed. DobbyVPN writes detailed logs only to local,
owner-restricted log files and its user-initiated local log export. No OTLP
exporter, endpoint, authorization token, external-IP lookup, or telemetry
network request is initialized in production.

Legacy `[Telemetry]` TOML is accepted only for configuration compatibility. The
session API returns a compatibility warning and discards the section; it never
uses its endpoint, token, or attributes.
