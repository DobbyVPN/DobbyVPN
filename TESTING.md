# Testing DobbyVPN

Run checks relevant to the change. Tests are disposable: rerun them freely,
keep useful diagnostics, and clean up resources on success or failure.
A missing tool or unavailable platform is not a passing test.

Optional Git hooks can be installed with:

```bash
go install github.com/evilmartians/lefthook/v2@v2.1.10
lefthook install
```

## Source checks

On Linux, Go tests need the TrustTunnel bridge and C++ runtimes staged in
`go_module/`. The Test workflow runs
`python3 .github/scripts/desktop_build.py prepare-go-test-deps --go-mod-tidy`
and sets `CGO_LDFLAGS` and `LD_LIBRARY_PATH` for later workflow steps. That
environment handoff uses GitHub Actions; running the setup command as a local
subprocess will not export those variables into your shell.

With those dependencies and environment variables available, from
`go_module/`:

```bash
go test -tags=ci ./...
go test -race ./routing/... ./sessionapi/... ./tunnel/...
```

From `kmp_module/`, with JDK 17 and the Android SDK:

```bash
./gradlew :grpcstub:test :app:jvmTest :app:testDebugUnitTest :app:verifyDebugNativeAbiPayloads :app:assembleReleaseAndroidTest
./gradlew :app:detektMetadataCommonMain :app:detektJvmMain :grpcstub:detekt
```

The root KMP `detekt` aggregate has no sources; use the source-set tasks above.

With a disposable Android emulator/device connected, run the app's own
instrumentation tests directly: `./gradlew :app:connectedReleaseAndroidTest`.
These service-shell tests do not replace VPN traffic tests.

From the repository root:

```bash
python3 -m pytest .github/scripts
PYTHONPATH=torturer python3 -m unittest discover -s torturer/tests -p 'test_*.py'
```

The functional-tooling suite includes Windows-only process tests, skipped on
other operating systems. Running its Python unit tests is not a live VPN test.

Go/Fyne component tests cover UI state without opening a window. Desktop
release artifacts are native Go/Fyne binaries on Windows, macOS and Linux; the
first two are the GUI-automation targets, while Linux remains CLI/service
qualification only. Production Android Kotlin/Compose and iOS Swift/Compose
activities retain their native VPN shells and exercise the shared Go session
binding. The opt-in Fyne mobile entry point is built with `-tags=fyne_mobile`;
it is a packaging experiment, not yet the production Android/iOS activity. GUI
automation must drive visible controls and may not substitute CLI commands for
GUI actions.

## iOS

On a Mac with Xcode and an installed Simulator runtime:

```bash
swift test --enable-code-coverage --package-path swift_module
cd kmp_module
./gradlew :app:linkDebugFrameworkIosSimulatorArm64 :app:iosSimulatorArm64Test
```

On Intel, use `:app:linkDebugFrameworkIosX64 :app:iosX64Test` instead.
The Test workflow covers Swift lifecycle and KMP shared-core tests.
The private Harness also runs the app-contract helper in
`torturer/tests/ios_simulator/`. Local Intel runs use explicit Mini mode and
check initialization without Metal. GitHub uses explicit Metal mode, which
requires usable Metal and checks that the normal app builds, launches, and
attaches its main view. This startup smoke does not prove that Metal presented
a frame. Neither mode is VPN traffic qualification.

Simulator coverage is not physical-device VPN coverage. The vendor
TrustTunnel bridge is device-only; the Simulator returns an unsupported
error for that protocol. Suspend/resume is also not currently covered by the
functional suite.

## Functional tests and releases

Product and functional tests live at one revision. See
[the functional suite](torturer/README.md) for setup and
[scenario definitions](torturer/docs/contract.md) for assertions.

Pushes to `main` and pull requests run **Test** automatically. To check a
feature branch before opening a pull request, use **Actions → Test → Run
workflow** and select that branch. Its goal is source/build checks, including
the iOS Simulator Metal app-startup smoke on a Metal-capable runner; it does
not create a Render VPN or publish anything.

After the intended change is merged, use **Actions → Release → Run workflow**
on `main` when you want to qualify real signed packages. It tests those exact
packages on Linux, Windows, macOS, and Android, shares one Render VPN, then
deletes it. Release does not publish. When that run succeeds and you want to
distribute it, use **Actions → Publish → Run workflow** on `main` and select
its run ID. Publish uses those retained artifacts. Apple submission and
GitHub/F-Droid publication are independent jobs. Publishing credentials stay
in those jobs.

If a Release fails, fix the cause and dispatch a new Release workflow. Do not
rerun the old Release: only attempt 1 can qualify for Publish. This restriction
does not change reruns of the standalone Test workflow.

To Publish, copy the digits after `/actions/runs/` in the successful Release
run's URL into the required `release_run_id` field. Use a completed successful
Release from `main`; Publish rejects failed, in-progress, retried, or expired
artifact runs.

Enter the selected version's English “What's New” text in the required
`release_notes` Publish input. It is passed directly to Apple submission;
no repository variable is needed.

Publish waits up to 15 minutes for the exact Apple version/build to finish
processing. A retry reuses an existing build instead of uploading it again;
failed, invalid, or expired builds stop submission.

If the qualified commit differs from the workflow revision, GitHub may reject
tag creation by `GITHUB_TOKEN` because workflow files differ. Before dispatch,
an authorized maintainer can create the annotated `vX.Y.Z` tag at the qualified
Release SHA using an account with Contents and Workflows write access. Never
move an existing release tag. Publish verifies its target before promotion.

Hosted tests share one disposable Render VPN for the whole run. Public
services provide IP and bounded upload/download checks; there is no custom
HTTP measurement server. All platform traffic must originate inside the
tested VPN environment (including inside Android, not on its host). The shared
functional deadline starts when Render service creation begins, includes
platform setup and queue time, and reserves five minutes for final deletion.
External-service failures must be distinguished from product failures.
Scenario resets and deletion of the shared Render service are checked. The
initial VPN service and Android emulator are left to the disposable
GitHub-hosted runner; their shutdown is not separately verified.

Local VM tests take one product worktree and a fresh owner profile. They build
for iteration, not for release reproducibility, and clean up after every
result. See the private Harness README for the launcher command.

Keep available logs even when a test fails. Missing logs are reported, not
used to prevent cleanup. A failed cleanup is reported separately and prevents
a release from being treated as successful.

Release-only checks retain Android reproducibility and signing-certificate
verification, and iOS signature, entitlement, provisioning, and version
checks. The Android Release also fetches the current F-Droid metadata and
fdroidserver source, then runs the app through the official F-Droid
`buildserver-trixie` environment. For a version that is not in fdroiddata yet,
the check uses fdroidserver's update logic to copy the latest build stanza and
binds only the candidate source commit and local Release APK reference. The
source scan, F-Droid APK scan, reproducible comparison, and
`AllowedAPKSigningKeys` check must all pass. This catches recipe drift such as
an inherited `submodules` flag after `.gitmodules` was removed. The live
fdroiddata, fdroidserver, and container revisions are printed in the job
summary; an unavailable external revision fails the F-Droid job instead of
being treated as a pass.
Publish never rebuilds or requalifies packages. It rejects incomplete or
unsuccessful Release runs, checks that artifacts remain available, and uses
the selected run's source commit and Apple build number. An Apple API failure
does not block GitHub/F-Droid publication, and vice versa.
