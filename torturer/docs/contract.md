# Functional test contract

This file owns the functional scenario set, platform coverage, and required
diagnostic and cleanup behavior. Product and tests are built from one source
revision. [TESTING.md](../../TESTING.md) lists supported checks and commands.

## Suites and platforms

The mini suite is the portable qualification contract. Full is available only
on Windows and macOS with an interactive desktop; it runs mini once, then adds
the native-window journey. A missing tool or environment is unavailable
coverage and cannot be counted as a pass.

| Platform | Mini | Full |
| --- | --- | --- |
| Windows and macOS | Go backend and CLI semantic scenarios, including VPN observations | Mini once, then the native frontend journey on an interactive desktop |
| Android | Compose UI on an emulator, VPN permission and connection checks, and semantic VPN scenarios; the binding lane covers every discovered profile | Not currently supported; future full coverage needs physical-device runners |
| iOS Simulator | One SwiftUI interaction and lifecycle journey; no VPN traffic claim | Not currently supported; future full coverage needs a physical device |
| Linux | Go backend and CLI semantic scenarios, including VPN observations | Not defined; GUI work is outside the current scope |

Hosted Windows and macOS qualification runs mini. It does not claim to render
the native window. The native-window journey runs in the local full suite and
uses the candidate or exact installed Release package. It types configuration,
connects and disconnects through visible controls, checks settings and
close/reopen behavior, and proves backend restart recovery through the UI.
Independent adapter observations verify the tunnel, routing, stability,
traffic, and cleanup.

Android's rendered journey enters one complete supported Outline or Xray
profile through the production Compose screen. The separate binding lane
retains the complete profile matrix. The iOS Simulator journey types an
invalid non-empty value, observes the frontend's error handling, checks
release metadata and the Logs screen, then relaunches the app. Simulator
coverage does not assert a successful configuration or VPN connection.

## Canonical scenarios

Mini and full use the same semantic scenario set. Full adds native-window
coverage only on Windows and macOS.

| Scenario | Required behavior |
| --- | --- |
| functional.configure | Go accepts and configures the supplied profile |
| functional.core-connection | Connect, observe the tunnel and routed identity, measure stability and traffic, disconnect, and verify cleanup |
| functional.start-stop-start | Disconnect and reconnect; independently verify the second tunnel and routing before final cleanup |
| functional.product-process-loss | Recover after the Go backend process is stopped and restarted |

Measurements must be finite and positive. Stability uses five successful
samples at one-second intervals. Cleanup failure fails the scenario.

The diagnostic-only functional.network-transition scenario remains selectable
for focused work but is not part of either passing suite. Suspend/resume is
not currently a functional scenario. Focused scenario selections are diagnostic
runs and do not qualify a suite.

## Traffic and shared service

Release qualification installs the exact packages produced earlier in that
Release run. Linux, Windows, macOS, and Android use one fresh Render Outline
VPN service. The workflow deletes it after platform work, including failure
paths. One shared deadline includes service setup and platform queue time, and
reserves time for deletion.

Identity checks use api.ipify.org. Cloudflare supplies bounded latency and
traffic measurements. Requests must traverse the app tunnel. An unsupported
or failed measurement is not converted to a pass.

Android's observation driver makes requests through the VPN network; the
functional engine owns the assertions. Process disappearance alone does not
prove recovery: a new working session must be observed.

## Diagnostics and cleanup

Product log files are read and displayed as written; the product does not
sanitize their contents. When test tooling forwards command output, it redacts
credentials and private profile values while preserving the surrounding
diagnostic content.

The invoking process receives complete stdout and stderr from repository-owned
commands, including failures, timeouts, original exceptions, and cleanup
errors. Output is not truncated, reduced to selected lines, or replaced by a
status code or byte count. Collection failures are reported alongside the
test failure. Test cleanup runs after pass, failure, and timeout.

Required UI screenshots are kept with the current disposable run. Captures
are limited to the target window. Only private profile values are redacted
when output policy requires it; other diagnostic content remains intact. A
failed capture or transfer fails the check.
The test Harness creates no separate log or evidence archive. The owner
workspace retains only the latest completed run under its storage rules.

Local candidates and runner resources are disposable. A successful scenario
requires both its behavioral assertions and its required cleanup to pass.
