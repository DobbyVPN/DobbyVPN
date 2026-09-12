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

`kmp_module/conveyor.conf` generates its shared configuration through the
paired `.github/scripts/conveyor-config` and `conveyor-config.bat` launchers.
Keep the two launchers output-clean: stdout is reserved for HOCON, while
diagnostics belong on stderr.

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
built native paths (`service`, `cli`, and `network`). Desktop local VM checks
do not build or discover the JVM application. Android additionally records
the signed `app` and `test_companion` APK paths.
The runner already owns platform identities and logs, so the descriptor does
not repeat those values or perform a cross-user permission handoff.

## Release

Pushes run checks. The explicitly started Release workflow owns package
builds, functional tests, shared Render cleanup, and protected publication jobs. After
qualification, GitHub package publication and Apple App Store submission run
independently: an App Store Connect failure does not block the verified
Android, desktop, or F-Droid GitHub Release.
Tests install the built packages rather than rebuilding a different candidate.
See [testing](../../TESTING.md) for the test lifecycle.

Release-only Android checks build unsigned APKs twice and compare them.
`verify_android_reproducibility.py` verifies identical payloads;
`verify_android_apk_source.py` checks the embedded source identity.
Signing verification checks the established certificate and that signing
did not change application payloads. These checks protect F-Droid source
matching and upgrade compatibility.

The marketing version determines Android's version code:
`major * 1,000,000 + minor * 1,000 + maintenance`.
Apple uses the release run number as its build number.
F-Droid builds the promoted tag and `version.txt`.

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
