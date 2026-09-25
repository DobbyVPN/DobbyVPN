# DobbyVPN development

This repository owns the product, builds, unit tests, and the functional suite
in `torturer/`. Product and functional tests change together at one revision.
The app must build and run without the private Harness.
Do not import test or owner-infrastructure packages into production code.

## Architecture

Native UI frontends use SwiftUI on macOS/iOS, Kotlin with Jetpack Compose on
Android, and C# with WinUI 3 on Windows. Linux remains CLI/service only. Do
not introduce Kotlin Multiplatform or remove Outline, Xray, or TrustTunnel.

Keep configuration, protocol selection, probing, recovery, session/generation
state, runtime policy, protocol engines, and cleanup in the shared Go backend.
Native code owns UI, VPN permissions, services, and the required OS VPN APIs;
it does not duplicate Go policy. Desktop UI frontends use the privileged Go
backend through JSON on a Unix domain socket on macOS/Linux and a named pipe
with local access control and remote-client rejection on Windows. Desktop
control has `Snapshot`, `Configure`, `Start`, and `Stop`; visible frontends poll
`Snapshot`. Keep session ID, sequence, and generation fencing. The CLI uses
the backend directly, not a subprocess per UI action.

The Go backend owns and preserves an accepted configuration URL and returns it
in `Snapshot`. Existing desktop saved URLs are migrated into the backend-owned
store. Native frontends read fixed local diagnostic files directly; product
logs and development or qualification output are not sanitized.
Keep the retired Fyne UI and gRPC desktop control stack removed. Preserve
current VPN behavior unless a behavior change is separately decided.

`docs/ARCHITECTURE.md` describes the interfaces and lifecycle implemented in
the current source.

## Tests and builds

Product build entrypoints stay in this repository. `torturer/` owns shared
functional scenarios, assertions, and result values; local and hosted adapters
use the same engine. `torturer_checks.local_vm` owns private-VM
build/setup/cleanup commands. The private Harness owns SSH inventory and the
guest's exclusive VM lock, timeout, and session lifecycle. Do not duplicate
these responsibilities or introduce a separate test control service.

Use `TESTING.md` for check commands and coverage. Keep tests small, disposable,
and easy to rerun. Preserve meaningful product assertions; remove obsolete
test infrastructure and duplicate validators. Never hide failures or label an
unavailable test as passed. The complete diagnostic and cleanup contract is
authoritative in [torturer/docs/contract.md](torturer/docs/contract.md). Logs
are diagnostics, not an approval protocol. Run checks relevant to the change
and report what could not run.

## Release

Pushes and pull requests run checks. Release is dispatched from `main`,
builds packages, and qualifies those exact packages with the in-repository
functional suite. It does not publish. Publish is a separate manual workflow:
it selects a successful Release run and promotes those tested artifacts.
GitHub publication and Apple submission run independently. Signing and
publication credentials belong to their protected jobs, not the candidate
processes under test. F-Droid builds the promoted tag and `version.txt`.
Operational authorization in the private owner workspace is defined by its
`AGENTS.md`.

## Changes and diagnostics

Preserve unrelated work and use non-destructive Git operations. Keep source,
examples, and fixtures synthetic: no credentials, private profiles, private
endpoints, raw operational logs, or generated packages in commits.

Preserve complete command stdout and stderr, original exceptions, and cleanup
errors on every outcome, including timeouts. Never suppress output, replace it
with byte counts or status codes, truncate it, or sanitize diagnostic content.
Preserve the exact output bytes, including private profile values if a command
emits them. Forward output before cleaning up disposable files; diagnostic
preservation does not require a separate log or evidence archive. Report
collection failures explicitly and cleanup failures alongside the original
failure.

## Simplicity

Actively reduce complexity. Remove unnecessary code, abstractions,
configuration, dependencies, tests, and documentation. Keep one owner per
responsibility and one authoritative source per fact or instruction.

Prefer straightforward code and established tools over custom machinery.
Solve current problems; do not engineer for hypothetical needs.

Existing architecture is replaceable. Simplify the whole system, not just
individual files. Temporary breakage during an agreed rewrite is acceptable;
the completed change must preserve required behavior and complete diagnostics.
