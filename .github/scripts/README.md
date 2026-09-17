# Desktop Build Script

`desktop_build.py` is the shared entry point for desktop service builds, native
Go/Fyne UI builds, and local CLI checks. It is intended to be used both locally and from
GitHub Actions.

The script checks required dependencies and installs missing local toolchains
where practical:

- Go from `.go-version`
- Fyne's native desktop headers on Linux
- Linux compiler packages through `apt-get`
- Windows MinGW through Chocolatey when needed
- `wintun.dll` for Windows CLI checks

Local toolchains are installed under `.local-tools/desktop-build`.

## Commands

Build the current platform gRPC VPN service:

```bash
python3 .github/scripts/desktop_build.py libs
```

Build the native desktop inputs for the current host:

```bash
python3 .github/scripts/desktop_build.py app
```

Build the production-widget headless UI companion for Windows/macOS GUI
qualification (on the matching native host):

```bash
python3 .github/scripts/desktop_build.py ui-test --platform current
```

Build release archives from staged native inputs:

```bash
python3 .github/scripts/package_desktop.py --version 1.5.1 --output output
```

This produces the Windows/macOS ZIPs and the Linux DEB consumed by the
platform installers; it does not invoke Java, Gradle, or a third-party
packager.

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

The Windows service artifact is a minimal runtime closure. It contains
`windows_grpcvpnserver.exe`, its checksum-pinned `dobby_bridge.dll` import,
and checksum-pinned `wintun.dll`, which the service loads at startup. The
installer build requires those files and verifies their names in the finished
MSI before upload.

The desktop app build uses service binaries downloaded into `kmp_module/services`:

```bash
python .github/scripts/desktop_build.py app --skip-libs --require-all-services
```

`--go-mod-tidy` is intentionally explicit. CI uses it to preserve the previous
workflow behavior; local service builds only run `go mod download` by default.

Use `--skip-deps` to require dependencies to already exist and `--skip-build` to
reuse existing build outputs where supported.

## Local Android builds

The private runner calls `local_candidate.py`, which uses
`android_build_driver.sh --local` and a disposable signing key.
Local mode accepts the supplied worktree, builds the app once with normal
incremental caches, and builds its test companion. It does not prove release
reproducibility or invent a Git identity for uncommitted source.
For desktop targets, the resulting `candidate.json` is a flat map of the
built native paths (`service`, `cli`, and `network`); Windows/macOS also carry
the headless `ui_test` companion. Linux local checks remain CLI/service-only.
Desktop local VM checks do not build or discover the JVM application. Android
additionally records the signed `app` and `test_companion` APK paths.
The runner already owns platform identities and logs, so the descriptor does
not repeat those values or perform a cross-user permission handoff.

## Release

Pushes and pull requests run checks. For the manual Release and Publish steps,
see [Testing DobbyVPN](../../TESTING.md); that is the single source for the
workflow instructions.

Release-only Android checks build unsigned APKs twice and compare them.
`verify_android_reproducibility.py` verifies identical payloads;
`verify_android_apk_source.py` checks the embedded source identity.
Signing verification checks the established certificate and that signing did
not change application payloads. The F-Droid Release lane then fetches the
current upstream recipe and server, uses fdroidserver's own update logic for a
new candidate, and builds the exact source in the official buildserver
container. `fdroid_release_metadata.py` validates that inherited recipe fields
are preserved; `fdroid_release_check.sh` runs `fetchsrclibs`, the on-server
build, and the official APK scanner. F-Droid compares its unsigned output with
the signed Release APK through the temporary local reference URL and verifies
the declared signing key. These checks protect the recipe as well as the
artifact.

The marketing version determines Android's version code:
`major * 1,000,000 + minor * 1,000 + maintenance`.
Apple uses the release run number as its build number.
F-Droid builds the promoted source commit. Publish creates the `version.txt`
release asset used by F-Droid's update check. The Release check is a
pre-publication compatibility check; it does not publish metadata or packages.

## Signed iOS packages

`verify_ios_app_group.py` checks signatures, profiles, bundle identifiers,
Apple team, source revision, version, build number, App Group, and
packet-tunnel entitlement before the exact IPA is uploaded as a run-scoped
artifact for TestFlight submission.

## Public release metadata

`release_provenance.py` creates and verifies the asset checksums and source
metadata in `release-provenance.json`. It contains no credentials or private
test evidence. Publishing credentials are confined to protected jobs, never
passed to the candidate application.
