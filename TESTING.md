# Testing DobbyVPN

Run checks relevant to the change. Tests are disposable: rerun them freely,
keep useful diagnostics, and clean up resources on success or failure.
A missing tool or unavailable platform is not a passing test.

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
go test ./...
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
fails if the host has no usable Metal device and includes one bounded native UI
smoke. Neither mode is VPN traffic qualification.

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
the iOS Simulator Metal UI smoke on a Metal-capable runner; it does not create
a Render VPN or publish anything.

After the intended change is merged, use **Actions → Release → Run workflow**
on `main` when you want to qualify real signed packages. It tests those exact
packages on Linux, Windows, macOS, and Android, shares one Render VPN, then
deletes it. Release does not publish. When that run succeeds and you want to
distribute it, use **Actions → Publish → Run workflow** on `main` and select
its run ID. Publish uses those retained artifacts. Apple submission and
GitHub/F-Droid publication are independent jobs. Publishing credentials stay
in those jobs.

To Publish, copy the digits after `/actions/runs/` in the successful Release
run's URL into the required `release_run_id` field. Use a completed successful
Release from `main`; Publish rejects failed, in-progress, retried, or expired
artifact runs.

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
checks. They protect deliverable correctness, not routine test bookkeeping.
Publish never rebuilds or requalifies packages. It rejects incomplete or
unsuccessful Release runs, checks that artifacts remain available, and uses
the selected run's source commit and Apple build number. An Apple API failure
does not block GitHub/F-Droid publication, and vice versa.
