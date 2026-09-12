# Functional tests

This file describes coverage, not an additional approval process.
Product and tests live in one repository. Local and hosted runs use the same
[scenario definitions](../torturer_contract/functional/scenarios.py),
[assertions](../torturer_contract/functional/assertions.py), and
[result fields](../torturer_contract/functional/results.py).

## What is tested

The full suite runs these scenarios for each connection reported by the app:

| Scenario | Behavior |
|---|---|
| `functional.configure` | Accept and configure the profile. |
| `functional.core-connection` | Connect, observe the tunnel and routed identity, measure traffic, check stability, disconnect. |
| `functional.start-stop-start` | Disconnect and reconnect, then independently observe the new tunnel and routing. |
| `functional.network-transition` | Recover after a reversible network change. |
| `functional.product-process-loss` | Recover after the product process is stopped and restarted. |

Measurements must be finite and positive. Stability uses five successful
samples at one-second intervals. Scenario reset and process cleanup must
succeed; a failure is not converted into a skip or a pass.

A focused local scenario is a diagnostic subset, not a claim that the full
suite passed. Run it freely while developing. The default local suite has no
accepted skips.

## Hosted limitations

GitHub runners currently cannot safely perform the network-transition
scenario on Windows or macOS; Linux requires a separately selected
non-control interface. These known limitations are recorded explicitly in
[the hosted limitation list](../torturer_checks/public_qualification.py).
Android has no accepted hosted scenario limitation. Unknown unavailability
fails the run rather than silently reducing coverage.

Suspend/resume is not tested on any platform. Simulator checks do not prove
physical-device iOS VPN behavior.

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

Android's instrumentation-only driver makes requests through the VPN network
and reports observations. The Python engine owns the assertions; see
[the observation contract](../torturer_contract/functional/android_observation.py).
Process disappearance alone is not recovery: a new working session must be
observed.

## Results, diagnostics, and cleanup

The engine decides pass/fail from behavior and required reset/cleanup.
Logs are diagnostics: retain available output and report collection problems,
but do not gate cleanup or test success on log filenames, completeness, or
heuristic inspection.

A failed test remains failed even if cleanup succeeds. A failed cleanup is
reported separately and blocks successful release completion.
Local candidates are disposable and cleaned up after every run. Rerun the
same command to repeat a test; a failed cleanup is reported separately.

Start a fresh explicit Release to retry hosted qualification. Re-running only
failed jobs cannot reuse the Render server deleted by final cleanup. A
successful Release retains its tested packages for the separate manual Publish
workflow; Publish takes that Release run's ID and does not rebuild or retest.
Signing and Render account credentials stay in their protected jobs; the
test runner receives only the disposable connection profile.

For commands and configuration, see [the suite README](../README.md) and
[product testing](../../TESTING.md).
