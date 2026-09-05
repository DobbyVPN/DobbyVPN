# DobbyVPN product-repository guidance

This repository contains the public DobbyVPN product. It must build, test, and
run without the private Harness or the public Torturer repository. Do not
import, vendor, download, invoke, or otherwise make either repository a
product dependency. Harness coordinates owner-local qualification,
tests-orchestrator owns the local runner VMs, and Torturer owns scenario
execution and result validation for both local and public tests.

## Architecture driver

The architecture is: **one shared UI layer where sharing is valuable, one Go
product/runtime layer for behavior, and only thin OS-specific shells where VPN
APIs require them.** Keep platform-specific behavior at those OS API
boundaries. Keep lifecycle, protocol, safety, and product policy in the shared
Go runtime. Do not duplicate product build logic in Harness.

Torturer alone owns the shared functional scenarios, platform test operations,
assertions, result validation, and pass/fail meaning for private VM and public
GitHub runs. The orchestrator may invoke DobbyVPN-owned build/install interfaces
and Torturer, but must not duplicate either implementation.

App Store submission and GitHub Release/tag creation remain DobbyVPN GitHub
Actions responsibilities. F-Droid detects the promoted Release's `version.txt`
and builds its matching tag. Do not move product signing or publication
credentials into Torturer. A successful trusted Torturer release qualification
starts the DobbyVPN publication workflow automatically; this repository must
verify that run and its own successful Release run for the exact same revision
before changing external release state.
The Release run, including internal TestFlight upload, completes before that
Torturer qualification starts. Public Torturer qualification installs the
exact packages from that Release run and does not rebuild the application;
publication uses only the exact verified Release run's outputs.

## Product and test boundaries

- Keep product unit and seam tests in this repository. The existing testing
  suite, its assertions, and its norms are authoritative; do not weaken,
  replace, or silently skip them as part of an application change.
- Preserve the platform build/test contracts. Torturer owns functional time
  bounds and pass criteria; do not weaken them or hide a runner failure.
  Runner provisioning belongs in `dobby-tests-orchestrator`, not DobbyVPN.
- Public source and examples contain only synthetic, non-sensitive data. Never
  commit private profiles, credentials, credential-bearing URLs, endpoints,
  observed identities, screenshots, raw logs, VM state, or generated release
  artifacts.
- Local VPN qualifications preserve every byte of the VPN application and VPN
  service logs from start through cleanup. They also preserve complete raw
  stdout and stderr for every runner-invoked command from candidate preparation
  through cleanup, including empty streams; missing or incomplete command
  output fails the run. This does not require a broader archive of screenshots,
  system snapshots, workspaces, caches, or unrelated machine state. If any log
  file is found to be
  larger than 300 MB, stop the current work, investigate exactly what produced
  it and why, and report that explanation to the owner before proceeding. Do
  not impose an artificial cap or truncate the log, and do not infer any other
  size threshold or automatic action from this rule.

## Repository safety

- Inspect and preserve existing work before editing. Do not use destructive
  branch or cleanup operations to reconcile divergent history; establish the
  exact source commit and review the diff first.
- Project remote pushes are permanently authorized. Do not pause for a push
  approval, but never push unrelated work or unreviewed generated/private
  material.
- Keep changes small, formatted, and covered by the existing product tests.
  Run focused checks after edits and report any failed, skipped, or
  missing-required-VPN-log check explicitly.

## Universal failure diagnosis

For every product build, package, install, emulator, runner, functional test,
cleanup, or release operation, treat any error, timeout, exception, non-zero
command, missing or partial output, unclear state, flaky result, logging
defect, or cleanup problem as a held incident. Preserve complete stdout and
stderr and the complete canonical application/service logs, inspect the first
actionable product-side error and the live affected state, and do not describe
a generic exception, exit code, or timeout as the root cause. Before teardown,
deletion, retry, or release continuation, record exactly what failed, exactly
what caused it (or that the cause remains unproven), and the specific fix or
mitigation. A clear success is evidence-checked before normal cleanup; an
incident on one platform must not silently change another platform's contract.
