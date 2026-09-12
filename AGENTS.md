# DobbyVPN development

This repository owns the product, builds, unit tests, and the functional suite
in `torturer/`. Product and functional tests change together at one revision.
The app must build and run without the private Harness.
Do not import test or owner-infrastructure packages into production code.

## Architecture

Use one shared UI where sharing is valuable, one Go runtime for product
behavior, and thin OS-specific shells at the VPN API boundaries. Go owns
configuration, protocol selection, session/generation state, and runtime
policy. Platform shells own native VPN permissions, services, and transport.
See `docs/ARCHITECTURE.md` for the interfaces and lifecycle.

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
unavailable test as passed. Logs are diagnostics, not an approval protocol.
Run checks relevant to the change and report what could not run.

## Release

Pushes and pull requests run checks. Release starts explicitly from `main`,
builds packages, and qualifies those exact packages with the in-repository
functional suite. It does not publish. Publish is a separate manual workflow:
it selects a successful Release run and promotes those tested artifacts.
GitHub publication and Apple submission run independently. Signing and
publication credentials belong to their protected jobs, not the candidate
processes under test. F-Droid builds the promoted tag and `version.txt`.
Operational authorization in the private owner workspace is defined by its
`AGENTS.md`.

## Changes and evidence

Preserve unrelated work and use non-destructive Git operations. Keep source,
examples, and fixtures synthetic: no credentials, private profiles, private
endpoints, raw operational logs, or generated packages in commits.

Preserve useful original errors and available command output. Report cleanup
failures alongside the original failure, and clean up disposable test resources
on every outcome.

## Simplicity

Keep one implementation per responsibility. Prefer straightforward functions,
scripts, and job dependencies to custom protocols. Internal test tooling has
no backward-compatibility requirement. Add protections for concrete risks
(such as exposing signing credentials), not hypothetical adversaries.
