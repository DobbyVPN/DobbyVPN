# Functional tests

This file describes coverage, not an additional approval process.
Product and tests live in one repository. Local and hosted runs use the same
[scenario definitions](../torturer_contract/functional/scenarios.py),
[assertions](../torturer_contract/functional/assertions.py), and
[result fields](../torturer_contract/functional/results.py).

## Coverage model

`mini` is the portable qualification contract. `full` is cumulative: it runs
mini once and adds environment-specific coverage. Hosted qualification accepts
mini. Local full is currently defined only for Windows and macOS, where it
adds the AUTO native-window journey after the shared semantic lane (exact-
Release mode uses the installed package).
Android and iOS full remain physical-device work; Linux is intentionally
CLI/service mini-only.

GUI connection journeys temporarily exercise AUTO only and do not iterate
individual profiles. The Android non-GUI binding matrix still exercises every
discovered profile; other non-GUI profile matrices remain unchanged. Desktop
full is mini once plus the AUTO native-window journey; all other required UI
actions are unchanged.

| Platform | Mini qualification | Full qualification |
|---|---|---|
| Windows/macOS | Headless production Go/Fyne AUTO widget/service boundary plus the semantic scenarios below and real VPN observations | Mini once plus the AUTO native-window journey: visible native-window input, Connect/Disconnect/reconnect, settings, and close/reopen actions |
| Android | Rendered emulator AUTO journey plus consent, Connect/Disconnect/reconnect, traffic, and routing; the binding matrix covers every discovered profile | Physical device, including the device-only VPN bridge |
| iOS | One comprehensive rendered Simulator UI/input/lifecycle/diagnostics contract; no VPN traffic | Physical device and NetworkExtension traffic |
| Linux | CLI/service and real VPN observations | Not defined |

## Semantic scenarios

The semantic mini lane (desktop, Android, and Linux) runs these scenarios for
the connections its platform adapter exposes. GUI adapters expose the single
AUTO selection and do not iterate individual profiles. Android additionally
runs the same scenarios through its non-GUI binding matrix for every discovered
profile. The iOS Simulator mini lane is the separate comprehensive rendered UI
contract described in the coverage table.

The iOS Simulator UI journey types and edits a non-empty value, then submits
it to the production Connect action. The current Simulator run surfaces
`PLATFORM_FAILED: configuration mailbox write returned failure`; this verifies
only that the rendered UI handles the provider-side failure without claiming a
connection. It does not establish that Go parsed that value or that VPN traffic
works. Empty input is validated locally and has a separate required-input
assertion. Do not describe the non-empty submission as malformed-config parser
qualification.

| Scenario | Behavior |
|---|---|
| `functional.configure` | Accept and configure the profile. |
| `functional.core-connection` | Connect, observe the tunnel and routed identity, measure traffic, check stability, disconnect. |
| `functional.start-stop-start` | Disconnect and reconnect, then independently observe the new tunnel and routing. |
| `functional.product-process-loss` | Recover after the product process is stopped and restarted. |

Measurements must be finite and positive. Stability uses five successful
samples at one-second intervals. Scenario reset and process cleanup must
succeed; a failure is not converted into a skip or a pass.

`functional.network-transition` remains defined for focused diagnostics but is
deferred from both qualification suites. Suspend/resume is not yet defined as
a qualification scenario. A focused or explicitly selected scenario is
diagnostic output only, not a claim that the suite passed. The default
qualification suites have no accepted unavailable skips.

## Hosted boundaries

Hosted Windows/macOS runs use the production Go/Fyne widgets and authenticated
service boundary through the headless Fyne driver; they do not claim that a
hosted runner displayed a native desktop window. The real native-window journey
belongs to local full qualification and requires an interactive desktop.
The local native-window journey keeps service fault injection separate from UI
recovery: process-loss control kills/restarts only the desktop service, then
the visible Go/Fyne window re-enters the profile and clicks Connect. The lane
repeats tunnel, routed public-IP, stability, and throughput observations after
both explicit reconnect and UI-driven process-loss recovery; a CLI
`connect-profile` shortcut is not qualification coverage.

Android hosted mini uses a rendered emulator and the real VPN service. Its
`gui-auto` lane takes the first complete `Outline` or `Xray` protocol block
from the fresh private bundle without rewriting its bytes; the separate
`protocol-matrix` lane keeps the original bundle and exercises every product
profile through the binding. This keeps the real GUI AUTO journey bounded
while preserving full profile-matrix coverage. iOS
Simulator mini proves rendered controls, input, lifecycle, and diagnostics but
cannot prove a physical-device NetworkExtension tunnel or VPN traffic. Unknown
unavailability fails the run rather than silently reducing coverage.

## Traffic and infrastructure

Hosted Release jobs install the exact packages built earlier in the same
workflow run. Linux, Windows, macOS, and Android run in parallel against one
fresh Render Outline WebSocket VPN. The workflow requests its deletion after
all platform jobs finish, even when tests fail. The Render test service has a
30-minute lifetime. One shared deadline starts when service creation begins;
platform setup and queue time count against it. Each platform receives only
the remaining time, with five minutes reserved for Render deletion. Cleanup is
still attempted at the end.

Scenario reset and deletion of the shared Render service are checked. The
initial VPN service and Android emulator are left to the disposable
GitHub-hosted runner; their shutdown is not separately verified.

IP checks use `api.ipify.org`. Cloudflare's public speed-test service supplies
bounded 1-MiB download/upload probes and a tiny latency request.
There is no custom measurement server. Defaults live in
[the adapter factory](../torturer_checks/hosted/factory.py), shared by local
and hosted runs. Requests must traverse the app's VPN, not merely execute
on a host outside the tested environment.

Non-success measurement HTTP responses are reported separately from routing
or transport failures. No fallback silently turns an unavailable check into
a pass.

Android's observation driver makes requests through the VPN network and
reports observations. The Python engine owns the assertions; see
[the observation contract](../torturer_contract/functional/android_observation.py).
Process disappearance alone is not recovery: a new working session must be
observed.

## Results, diagnostics, and cleanup

The engine decides pass/fail from behavior and required reset/cleanup.
Command, service, app, device, build, and cleanup output is part of the
diagnostic contract. Every repository-owned process boundary preserves the
complete stdout and stderr streams, including successful output, non-zero
exit output, launch exceptions, timeout output after termination, and cleanup
output. Output is never replaced with byte counts or status codes, selected as
“useful” lines, tailed, or truncated by a repository-owned size limit. Execution
deadlines remain bounded, but output has no artificial repository limit.

Adapters may keep output in memory or in owner-only disposable scratch while
parsing and asserting. The invoking process receives complete redacted streams
before scratch is removed. Local detached guests expose separate test and
cleanup streams after completion; the controller attempts to deliver every
stream before removing the guest run. A collection failure is reported beside
the original test failure and does not erase it. A failed test remains failed
even if cleanup succeeds; a failed cleanup remains separately visible and
blocks successful release completion.

Credentials and private profile values are redacted at the transport boundary
without deleting surrounding diagnostic text. Public service names and error
context such as `api.ipify.org` are not removed merely because they occur near a
redacted value. No separate log or evidence archive is created; the retained
structured result, complete redacted `streams/`, and collection status are the
one local run governed by the owner and workflow retention policies described
by the testing documentation. No older stream copy is retained elsewhere.

Every GUI lane retains required milestone and failure screenshots in that same
current run: headless Go/Fyne frames for desktop mini, exact native windows for
desktop full, rendered emulator frames for Android mini, and XCTest captures
for iOS Simulator mini. When a rendered frame can contain private profile or
diagnostic content, configuration, log, and detail regions are masked before
transfer. Complete PNG structure, CRCs, dimensions, byte length, and SHA-256
are validated. A required capture, masking, transfer, or validation failure
fails the lane while preserving any earlier product failure as primary.
Screenshots supplement complete streams and functional assertions; they never
replace either.

Local candidates are disposable and cleaned up after every run. Rerun the
same command to repeat a test; a failed cleanup is reported separately.

Start a fresh explicit Release to retry hosted qualification. Re-running only
failed jobs cannot reuse the Render server deleted by final cleanup. A
successful Release retains only the package artifacts needed by exact-package
qualification and the separate manual Publish workflow while it remains the
newest completed workflow run. Publish takes that Release's ID and does not
rebuild or retest.
Signing and Render account credentials stay in their protected jobs; the
test runner receives only the disposable connection profile.

For commands and configuration, see [the suite README](../README.md) and
[product testing](../../TESTING.md).
