# Testing DobbyVPN

Run checks relevant to the change. Tests are disposable: emit complete
redacted output before cleanup, retain only one completed local run total, and
clean up resources and scratch files on success or failure. Trusted GitHub
push/manual workflows converge Actions storage to one completed run; public
fork and Dependabot pull-request runs may temporarily remain until the next
trusted workflow because their token cannot delete older runs.
A missing tool or unavailable platform is not a passing test.
Deferred qualification work is tracked in [docs/TODO.md](docs/TODO.md); those
items are not accepted skips inside a passing suite.
The product Go modules use the exact Go 1.26.8 version recorded in
`.go-version`; do not substitute another toolchain for qualification. This
migration has no RAM benchmark or memory-usage acceptance criterion.

## Coverage model

`mini` is the portable qualification contract. `full` is cumulative: it runs
the mini contract once and adds only the platform-specific qualification that
the environment can actually provide. A missing environment is an unavailable
check, not an accepted skip or a pass.

| Platform | `mini` | `full` |
|---|---|---|
| Windows/macOS | Headless production Go/Fyne widget and service boundary, plus the real VPN semantic scenarios | Mini once, plus the native-window journey through visible controls and native input; exact-Release mode uses the installed package |
| Android | Rendered emulator UI, consent, connect/disconnect/reconnect, and real VPN traffic/routing for one representative non-TrustTunnel profile; the binding matrix covers every discovered profile | Physical-device extension; not part of 1.5.1 |
| iOS | One comprehensive rendered Simulator UI/input/lifecycle/diagnostics contract; no VPN traffic | Physical-device NetworkExtension and traffic extension; not part of 1.5.1 |
| Linux | CLI/service and real VPN semantic scenarios | Not defined; Linux is intentionally CLI/service-only |

The canonical semantic scenario membership and assertions are in the
[functional contract](torturer/docs/contract.md). The native desktop window
journey is cumulative full coverage, not a second semantic scenario set.
Deferred work is listed in [docs/TODO.md](docs/TODO.md); an explicitly chosen
diagnostic scenario is never a qualification result.

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

The Android native VPN shell and Go/Fyne UI are built from the standalone
`android_module/` project, with JDK 17, the Android SDK, and the pinned NDK.
Android necessarily hosts the thin Kotlin/Java OS boundary on ART; that code
contains no KMP or Compose UI. The build uses the pinned Go toolchain:

```bash
cd android_module
./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" \
  :app:testReleaseUnitTest :app:assembleReleaseAndroidTest :app:assembleRelease
```

The Gradle task directly cross-compiles `go_module/cmd/dobbyui` into
`libdobby_vpn.so` for arm64-v8a and x86_64 and packages the pinned Fyne Java
activity. Kotlin contains only the permission and `VpnService` boundary. The
release driver verifies both native libraries and their TrustTunnel symbol
policy.

On an API 35 emulator whose ABI matches the installed app and test companion,
drive the rendered screen through Android instrumentation:

```bash
adb shell am instrument -w -r \
  -e class com.dobby.GoUiInstrumentedTest \
  com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner
```

The test launches the release-shaped Fyne `GoNativeActivity`, discovers its
controls through Android accessibility, enters a fresh test profile through the
production input bridge, handles VPN consent, and drives visible Connect,
Disconnect, reconnect, and reopen actions, observing the rendered state. The
Android functional adapter uses the same installed package and real VPN service
for the shared semantic mini scenarios, including routed identity and traffic;
those GUI actions are not replaced by a CLI call. A TrustTunnel bridge is
unavailable on an x86_64 emulator; the emulator contract therefore uses a
supported test profile and does not turn that ABI limitation into an accepted
skip.

From the repository root:

```bash
PYTHONPATH=torturer python3 -m pytest .github/scripts
PYTHONPATH=torturer python3 -m unittest discover -s torturer/tests -p 'test_*.py'
```

The functional-tooling suite includes Windows-only process tests, skipped on
other operating systems. Running its Python unit tests is not a live VPN test.

Go/Fyne component tests cover UI state without opening a window. The native
`dobby-vpn-ui-test` companion drives the production widgets with Fyne's test
driver and the authenticated desktop service; hosted Windows/macOS mini runs
use it for the UI/service boundary while the shared semantic engine owns VPN
observations and cleanup. Local Windows/macOS full runs the mini contract once,
then launch the native binary in an interactive desktop session. Exact-Release
full mode launches the binary installed from that Release package. The journey
discovers Fyne controls through the platform accessibility tree, enters a fresh
profile with native keyboard input, clicks Connect and Disconnect, observes
rendered status, opens Settings, and closes/reopens the UI while the
service-owned session remains available. It then proves the explicit native
reconnect with a fresh tunnel/routing/stability/throughput observation set.
For process-loss qualification the desktop service controller only kills and
restarts the service; the UI re-enters the profile through native input and
clicks Connect, followed by another independent tunnel/routing/public-IP,
stability, and throughput proof. No CLI `connect-profile` command is allowed
to recover that native-window lane. If an interactive desktop or
accessibility permission is unavailable, the GUI lane is incomplete or failed;
it is not converted to a pass. On Windows, the local runner first probes for
an Explorer process owned by the configured interactive account; when that
desktop is absent it records `native_ui_status: unavailable` and exits
nonzero before registering a task. Linux remains CLI/service qualification
only. On Windows full runs, the native journey copies the exact production UI
into a disposable run-local staging directory, downloads the pinned MSVC Mesa
archive, verifies its SHA-256, and extracts only `x64/opengl32.dll` and
`x64/libgallium_wgl.dll` beside that copy. `GALLIUM_DRIVER=llvmpipe` is scoped
to the GUI controller and its UI child; no registry, System32, machine
environment, release, or installer state is changed, and the archive/staging
tree is removed on every outcome.
On macOS, the local full lane reads the authoritative
`scutil` ConsoleUser dictionary (`show State:/Users/ConsoleUser` followed by
`quit` on bounded stdin), requires that username and UID both match the SSH
worker, verifies `launchctl print gui/<uid>`, requires
WindowServer to report an unlocked screen, and starts the native smoke command
through the bounded `sudo -n launchctl asuser <uid> sudo -n -u <user>` handoff.
The preflight also requires a bounded System Events accessibility check against
Finder; an absent WindowServer lock key is accepted only with that active
console/Finder evidence, while conflicting or malformed values fail closed.
`/dev/console` ownership is not used as the desktop decision because it can
remain owned by `root` while a user's Aqua session is active. Native controls
are bound to the launched window/process identity, and disposable macOS runs
terminate pre-existing `Dobby Vpn` instances before starting a new one.
The macOS full lane must create and drive the real production window. The
pinned GLFW Darwin bridge first requests an accelerated NSGL pixel format and
retries the same format without only the accelerated flag when the host offers
an Apple software renderer; this keeps the window test viable on the current
virtualized guest without requiring Metal or a larger framebuffer. A failure
to create its graphics context is still reported with complete process output;
do not infer the cause from VM display inventory alone or convert it to a pass.
GUI automation must drive visible controls and may not substitute CLI commands.

The Go job emits one repository-wide coverage profile with
`go test -coverpkg=./...` and writes its `go tool cover -func` report to the
job summary without retaining a coverage artifact. This is the shared Go
coverage source; platform UI qualification is reported separately and does
not invent per-platform coverage numbers.

The Go module keeps the upstream Fyne and GLFW import paths, while exact
version-specific replacements bind them to the public DobbyVPN fork commits
recorded by the Android provenance and reproducibility manifests. The
intentional local tun2socks and TrustTunnel replacements remain separate and
are not rejected by the UI dependency check.

Mobile Go/Fyne qualification must use real Android/iOS rendering, keyboard,
tap, and lifecycle interaction. Android mini combines the rendered emulator
journey with VPN consent, real Connect/Disconnect/reconnect, and traffic and
routing assertions. Headless Fyne tests prove widget state and callbacks only;
they do not prove that a platform renderer presented a frame or that a user tap
reached it. Simulator GUI tests also do not prove a physical iOS
NetworkExtension tunnel. The Go/Fyne UI and native lifecycle shell are
integrated, so qualification uses the same package rather than a renderer-only
migration artifact.

On a macOS runner with Xcode, the integrated iOS Go/Fyne app can be built for
the Simulator with:

```bash
cd go_module
./scripts/build_ios_xcframework.sh --simulator-architecture arm64
./scripts/package_ios_app.sh iossimulator "$RUNNER_TEMP/Dobby-Vpn.app" \
  DobbyVPNRuntime.xcframework arm64
```

Simulator packaging uses temporary self-signed metadata and ad-hoc signing; it
does not require an Apple Development certificate. A physical-device IPA
still requires the Apple distribution certificate and provisioning profiles.
The Simulator check runs the packaged app's XCTest UI target. The single
comprehensive journey locates the real Go/Fyne controls through accessibility,
checks Settings/Back and release metadata, exercises native keyboard typing,
editing and clearing, checks empty and malformed input outcomes, verifies that
an unaccepted inline value is not restored after reopen, clears diagnostics,
and opens the app-owned export prompt, follows Save into the native document
picker, then cancels back to Fyne. The Share action remains the production
`UIActivityViewController` path for a user-selected export. Complete app-owned
startup output is forwarded for diagnostics, but a log marker cannot satisfy
the check. This proves
rendered UI and input/lifecycle/diagnostic wiring, not a physical
NetworkExtension packet-tunnel or TrustTunnel connection.

Android real-UI and functional qualification requires an API 35 device or
emulator, matching the app target and hosted qualification image.

## iOS

On a Mac with Xcode and an installed Simulator runtime:

```bash
swift test --enable-code-coverage --package-path swift_module
```

The Test workflow covers Swift lifecycle policy tests and the one iOS
Simulator Go/Fyne mini contract. That contract downloads the generated runtime
XCFramework, packages the app, and runs the rendered XCTest journey. There is
no Kotlin Multiplatform or Compose compile in this path; Swift remains only the
thin iOS native/VPN boundary. The private Harness also runs the app-contract
helper in `torturer/tests/ios_simulator/`.
Simulator qualification has one comprehensive non-Metal mini contract: it
builds, launches, and drives the same rendered XCTest accessibility, input,
lifecycle, and diagnostics journey on every supported host. It is not VPN
traffic qualification.

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
the iOS Simulator Go/Fyne rendered UI mini contract; it does not create a Render VPN
or publish anything.

After the intended change is merged, use **Actions → Release → Run workflow**
on `main` when you want to qualify real signed packages. It installs and tests
the exact packages built earlier in that run on Linux, Windows, macOS, and
Android, shares one Render VPN, then deletes it. Hosted desktop qualification
is mini; the native-window full journey requires a local interactive Windows or
macOS session. Release's prerequisite Test workflow also runs the one iOS
Simulator XCTest mini contract; iOS is intentionally absent from the later
traffic matrix because the Simulator cannot qualify a physical-device
NetworkExtension tunnel. Release does not publish. When that run succeeds and
you want to distribute it, use **Actions → Publish → Run workflow** on `main`
and select its run ID. Publish uses that Release's artifacts while they remain
the latest run. Any later completed workflow supersedes the Release under the
single-run retention policy, so publication then requires a new Release.
Apple submission and GitHub/F-Droid publication are independent jobs.
Publishing credentials stay in those jobs.

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
result. Android mini drives the rendered emulator UI, fresh-profile entry, VPN
consent, Connect/Disconnect/reconnect, and the semantic traffic/routing
scenarios. Its rendered lane stages the first complete non-TrustTunnel
`Outline`/`Xray` block without reserializing it; the Android binding lane still
receives the untouched full bundle and exercises every profile. Windows/macOS
full runs mini once and then drives the native
window; exact-Release mode uses the installed package. Linux runs only the
CLI/service contract. See the private
Harness README for the launcher command.

Complete command, service, app, and device stdout and stderr is emitted before
disposable scratch is removed, including on failure, timeout, and cleanup
failure. Repository-owned wrappers do not truncate or suppress output, replace
it with byte counts or status codes, or select only supposedly useful lines.
Credentials and private profile values are redacted without removing
surrounding diagnostic content. Original and cleanup exceptions are both
reported. Complete redacted streams and collection status are retained under
the current local run's private `streams/` directory while local retention
keeps one completed run total; this is not a second evidence archive. Trusted
Actions workflows keep one completed run total; pull-request backlog is
temporary and is removed by the next trusted workflow. A Release is
publishable only while it remains the newest completed workflow run.

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
