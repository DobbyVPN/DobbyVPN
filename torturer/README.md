# DobbyVPN functional tests

This directory contains the product's functional suite, formerly the separate
Torturer repository. Product and tests now share one commit and review process.
It is test tooling, not a production runtime dependency.

## Run checks

From this directory:

```bash
python3 -m unittest discover -s tests -p 'test_*.py'
```

Windows Job Object integration checks require Windows. Actual VPN qualification
uses the private Harness launcher or the product's explicitly started Release
workflow. A push or pull request runs checks, not publication.

## Ownership

`torturer_contract/` defines scenarios, assertions, and result semantics.
`torturer_checks/` supplies platform adapters and validation.
`torturer_provider/` manages disposable Render test resources.
The parent repository owns product build/install interfaces and all GitHub
workflows. Private VM setup lives in `torturer_checks/local_vm.py`; the owner
workspace supplies SSH transport and a small guest-side lock/deadline helper.

See [the functional contract](docs/contract.md) for coverage and
[product testing](../TESTING.md) for contributor commands. Development rules
are in [the product instructions](../AGENTS.md); this directory adds no
separate agent approval policy.

## Hosted and local qualification

Hosted qualification installs the exact packages GitHub built, using tests
from the same source revision. It covers Linux, Windows, macOS, and Android.
Public fixtures and disposable connection details are synthetic; account,
signing, and publication credentials are excluded from candidate execution.

One Release qualification creates one Render VPN service, shares it across the
Linux, Windows, macOS, and Android jobs, and deletes it in a final cleanup job.
The tests query `api.ipify.org` for the exit IP and use Cloudflare's public
speed-test endpoints for bounded download/upload probes. There is no
test-owned HTTP server and no second Render service. Cleanup runs even when a
platform fails; a cleanup failure fails the run and is reported separately.
Start a new explicit Release run to retry qualification. Re-running only failed
jobs after cleanup is not supported: the old Render service and profile are
already gone, while build artifact names belong to the original run attempt.

The private Harness packages the product and this directory from one selected
worktree, downloads a fresh owner profile, and invokes the same functional
engine on local VMs. Local evidence remains private.

The iOS Simulator app-contract helper is a local side check, not VPN E2E.
The parent Test workflow owns the Go runtime XCFramework and Go/Fyne app
startup check; there is no separate public Torturer Simulator workflow.

## Hosted configuration after the merge

GitHub repository settings do not move with source files. The DobbyVPN
repository needs the existing `render-functional` environment, its
`RENDER_API_TOKEN` secret, and these variables: `RENDER_OWNER_ID`,
`RENDER_IMAGE_OWNER_ID`, `RENDER_IMAGE_PATH`, `RENDER_IMAGE_DIGEST`, and
`RENDER_REGION`. The one pinned Outline image is reused; no sink image or
image-publishing workflow is required. Existing product signing/publication
environments stay with the product.

All workflows are in the parent `.github/workflows/`. The former external
Torturer dispatch, release-read, and publication tokens are not needed by the
integrated workflow chain. Retire the old repository's workflows when the new
configuration is installed, so there is only one active qualification path.

## Import history

The suite was imported from DobbyVPN/Torturer commit
`d71462247704de99ad302246ee9dc979da454d39`. Its original license is retained in [LICENSE](LICENSE).
The former public Torturer repository is no longer the source of truth. Archive
it after the integrated hosted workflow is confirmed; preserve its history and
license attribution.
