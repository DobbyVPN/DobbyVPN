# DobbyVPN product-repository guidance

This repository contains the public DobbyVPN product. It must build, test, and
run without the private Harness or the public Torturer repository. Do not
import, vendor, download, invoke, or otherwise make either repository a
product dependency. Harness owns private qualification orchestration; Torturer
owns public scenario execution.

## Architecture driver

The architecture is: **one shared UI layer where sharing is valuable, one Go
product/runtime layer for behavior, and only thin OS-specific shells where VPN
APIs require them.** Keep platform-specific behavior at those OS API
boundaries. Keep lifecycle, protocol, safety, and product policy in the shared
Go runtime. Do not duplicate product build logic in Harness.

## Product and test boundaries

- Keep product unit and seam tests in this repository. The existing testing
  suite, its assertions, and its norms are authoritative; do not weaken,
  replace, or silently skip them as part of an application change.
- Preserve the current platform build/test contracts. Android test lanes have a
  hard 30-minute maximum, including cleanup; do not change VM sizing, host
  configuration, emulator configuration, or the testing suite to make a test
  pass.
- Public source and examples contain only synthetic, non-sensitive data. Never
  commit private profiles, credentials, credential-bearing URLs, endpoints,
  observed identities, screenshots, raw logs, VM state, or generated release
  artifacts.
- Local VPN qualifications preserve every byte of the VPN application and VPN
  service logs from start through cleanup. There is no standing requirement to
  archive every test action, observation, error, timing, screenshot, system
  snapshot, command stream, build transcript, cleanup record, or other
  auxiliary evidence. Retain additional material only when the owner explicitly
  requests it, a test contract defines it as an ordinary result, or a scoped
  active-failure investigation requires it. If any log file is found to be
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
