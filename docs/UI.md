# UI Behavior

This document describes the sequence of operations executed by the VPN client during different lifecycle events.

## Turn On

When the VPN feature is turned on, the client performs the following step:

1. `StartConnectionStateDetector` _(only if VPN tunnel is enabled)_

### Flow

```text
Turn On
└── StartConnectionStateDetector [VPN tunnel enabled only]
```

---

## Connect (for single connection)

When a connection is initiated, the following steps are executed in order:

1. `InitTelemetry`
2. `SetConfig`
3. `SaveTelemetry`
4. `StartTunnel`
5. `StartHealthCheck` _(only if VPN tunnel is enabled)_
6. `StartConnectionStateDetector` _(only if VPN tunnel is enabled)_

### Flow

```text
Connect
├── InitTelemetry
├── SetConfig
├── SaveTelemetry
├── StartTunnel
├── StartHealthCheck              [VPN tunnel enabled only]
└── StartConnectionStateDetector  [VPN tunnel enabled only]
```

---

## Disconnect

When disconnecting, the client performs cleanup in the following order:

1. `StopTunnel`
2. `StopHealthCheck`
3. `StopConnectionStateDetector`

### Flow

```text
Disconnect
├── StopTunnel
├── StopHealthCheck
└── StopConnectionStateDetector
```

---

## Summary

| Event          | Steps                                                                                                                                                         |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Turn On**    | `StartConnectionStateDetector` _(VPN tunnel only)_                                                                                                            |
| **Connect**    | `InitTelemetry` → `SetConfig` → `SaveTelemetry` → `StartTunnel` → `StartHealthCheck` _(VPN tunnel only)_ → `StartConnectionStateDetector` _(VPN tunnel only)_ |
| **Disconnect** | `StopTunnel` → `StopHealthCheck` → `StopConnectionStateDetector`                                                                                              |

## Notes

- `StartConnectionStateDetector` and `StartHealthCheck` are executed only when the VPN tunnel feature is enabled.
- Telemetry initialization and persistence always occur before tunnel startup with standard user network.
- During disconnect, monitoring components are stopped after the tunnel shutdown to ensure proper cleanup.
