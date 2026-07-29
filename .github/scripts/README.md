# Desktop Build Script

`desktop_build.py` is the shared entry point for desktop service builds, desktop
JVM builds, and local CLI checks. It is intended to be used both locally and from
GitHub Actions.

The script checks required dependencies and installs missing local toolchains
where practical:

- Go from `.go-version`
- JDK 17
- Android SDK command line tools with `platforms;android-35`,
  `platforms;android-36`, and `build-tools;36.0.0`
- Linux compiler packages through `apt-get`
- Windows MinGW through Chocolatey when needed
- `wintun.dll` for Windows CLI checks

Local toolchains are installed under `.local-tools/desktop-build`.

## Commands

Build the current platform gRPC VPN service:

```bash
python3 .github/scripts/desktop_build.py libs
```

Build the desktop JVM app and generated Conveyor config:

```bash
python3 .github/scripts/desktop_build.py app
```

Build and run the local CLI config check:

```bash
python3 .github/scripts/desktop_build.py cli-test --config 'https://example.com/config.toml'
```

The config can be an HTTP(S) URL, a local TOML file path, or inline TOML passed
through `--config` or `DOBBYVPN_CLI_TEST_CONFIG`.

## CI Usage

Desktop service binaries are built with explicit platform and architecture:

```bash
python .github/scripts/desktop_build.py libs --platform linux --arch amd64 --go-mod-tidy
python .github/scripts/desktop_build.py libs --platform macos --arch arm64 --go-mod-tidy
python .github/scripts/desktop_build.py libs --platform macos --arch amd64 --go-mod-tidy
python .github/scripts/desktop_build.py libs --platform windows --arch amd64 --go-mod-tidy
```

The macOS commands run on matching official GitHub-hosted runners: `macos-15`
for arm64 and `macos-15-intel` for amd64. Their artifacts are kept separate so
the installer and CLI lanes never combine architectures.

The desktop app build uses service binaries downloaded into `kmp_module/services`:

```bash
python .github/scripts/desktop_build.py app --skip-libs --require-all-services
```

`--go-mod-tidy` is intentionally explicit. CI uses it to preserve the previous
workflow behavior; local service builds only run `go mod download` by default.

Use `--skip-deps` to require dependencies to already exist and `--skip-build` to
reuse existing build outputs where supported.

## Release promotion

Every `main` push produces candidate artifacts and uploads a successful iOS
build to internal TestFlight. It does not create a final release tag.

The authorized release process dispatches `promote_release.yml`, which
revalidates the exact successful `main` Release run and source commit,
downloads its artifacts only to a GitHub-hosted runner, and creates `vX.Y.Z`
plus the GitHub Release. That final tag is the sole signal for official
F-Droid update processing.

Android and F-Droid use a stable version code derived from the marketing
version: `major * 1,000,000 + minor * 1,000 + maintenance`. For example,
`1.4.3` is `1004003`. This is intentionally independent of the GitHub Actions
run number, which remains Apple's monotonically increasing `CFBundleVersion`.
The release workflow and APK scan both enforce this mapping.

`repair_fdroid_release.yml` is a guarded recovery path for a release whose
Android assets predate that enforcement. It rebuilds from the exact existing
tag commit and replaces only the signed APK, unsigned APK, and `version.txt`
after checking the protected `release` environment and an explicit
`replace-vX.Y.Z` confirmation.

The same operator command dispatches `submit_app_store.yml` for production
App Review. Its secretless validation job binds the request to the exact
successful `main` Release run, source version, iOS job, and build number.
Only then does a separate job enter the protected `release` environment and
use its existing App Store Connect secrets. The selected build is submitted
with automatic release after approval.

Torturer remains the independent secretless gate for candidate code. Store
credentials never enter Torturer or any pull-request job; production
submission consumes the already-gated, successful Release result.

A separate F-Droid candidate-testing repository is planned outside this
public application repository; it is not part of the current workflow.
