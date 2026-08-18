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

## Release promotion

Every `main` push produces candidate artifacts and uploads a successful iOS
build to internal TestFlight. It does not create a final release tag.

The authorized release process dispatches `promote_release.yml`, which
revalidates the exact successful `main` Release run and source commit,
downloads its artifacts only to a GitHub-hosted runner, and creates `vX.Y.Z`
plus the GitHub Release. Promotion also receives authorized non-secret SHA-256
values for the exact Linux DEB, Windows amd64 MSI, and macOS amd64 PKG, then
re-hashes the selected run's downloaded files before publication. Every public
asset is recorded in `release-provenance.json`; a draft is downloaded and
verified byte-for-byte before publication, and retries re-download both the
selected run and published release rather than trusting release state alone.
That final tag is the sole signal for official F-Droid update processing.

Android and F-Droid use a stable version code derived from the marketing
version: `major * 1,000,000 + minor * 1,000 + maintenance`. For example,
`1.4.3` is `1004003`. This is intentionally independent of the GitHub Actions
run number, which remains Apple's monotonically increasing `CFBundleVersion`.
The release workflow and APK scan both enforce this mapping.

Android builds also carry their exact selected source commit in BuildConfig.
`verify_android_apk_source.py` reads that value back from both signed and
unsigned APK bytecode with `apkanalyzer`; the build, legacy F-Droid repair, and
public promotion all fail unless the embedded commit and repository link match
the selected full source SHA. The explicit `APP_SOURCE_*` values are passed to
Gradle as build properties for both current source and a legacy-tag repair.
Because an old tag cannot contain a verifier added later, the reusable build
checks out only the verifier from its trusted workflow revision after the tag
source has already been copied to the F-Droid-compatible build directory. The
trusted checkout is never mixed into the selected source tree.

The current Android release job also performs two clean, uncached unsigned APK
builds with distinct Go build caches and temporary directories. Both builds use
the F-Droid canonical source, Go, and GOPATH locations, the pinned Go/JDK/NDK/
Gradle/gomobile toolchain, and path-normalizing compiler flags. Promotion calls
`verify_android_reproducibility.py` and fails unless the complete APKs are
byte-identical and the retained provenance matches the published unsigned APK,
including both packaged `libgojni.so` payloads. The developer signature is
applied directly to that proven unsigned APK; a second verifier compares every
logical ZIP payload entry after signing and promotion repeats both checks. This
check also pins the public SHA-256 fingerprint of the established Android
signing certificate so an otherwise valid but upgrade-incompatible key cannot
be published. Together these gates prevent another tagged-source versus
F-Droid-build mismatch.

Android build-tools `36.0.0` is pinned for packaging, signing, and verification.
The gate does not claim that signature-block bytes are reproducible: it proves
the complete unsigned APK byte-for-byte, then separately proves that signing
changed only signature metadata and used the established certificate.

`repair_fdroid_release.yml` is a guarded recovery path for a release whose
Android assets predate that enforcement. It rebuilds from the exact existing
tag commit and replaces only the signed APK, unsigned APK, their Android
provenance, and `version.txt` after checking the protected `release`
environment and an explicit `replace-vX.Y.Z` confirmation. Releases carrying
the release-wide provenance manifest are immutable and cannot use this legacy
repair path.

The same operator command dispatches `submit_app_store.yml` for production
App Review. Its secretless validation job binds the request to the exact
successful `main` Release run, source version, iOS job, and build number.
Only then does a separate job enter the protected `release` environment and
use its existing App Store Connect secrets. The selected build is submitted
with automatic release after approval.

Torturer remains the independent secretless gate for candidate code. Store
credentials never enter Torturer or any pull-request job; production
submission consumes the already-gated, successful Release result.

## iOS IPA provenance

Before provenance is created, the iOS build extracts the signed IPA and
verifies both the app and packet-tunnel signatures and provisioning profiles.
`verify_ios_app_group.py` requires their exact bundle/application identifiers,
Apple team, shared `group.vpn.dobby.app` App Group, and the tunnel's
`packet-tunnel-provider` entitlement. This prevents a signed package that
cannot open its shared container from reaching TestFlight.

`ios_artifact_provenance.py` is a standard-library-only, fail-closed contract
between the reusable iOS build and protected App Store submission workflows.
The build creates one canonical JSON sidecar for exactly one regular IPA. It
records the full lowercase source SHA, semantic version, positive Apple build
number, IPA filename, byte size, and SHA-256. Submission downloads both
artifacts from the selected successful Release run and verifies all of those
fields before Fastlane is allowed to submit the already-uploaded TestFlight
build. The sidecar carries no credentials, configuration, or private evidence.
The successful Release iOS job proves that its named IPA upload command
completed after both retained artifacts were created. App Store Connect does
not expose a downloadable binary hash, so the sidecar cannot independently
prove that Apple's retained TestFlight binary is byte-for-byte identical. It
also intentionally does not prove Apple has finished processing that upload:
`skip_waiting_for_build_processing` keeps normal release CI bounded, and the
later protected submission lane retries only the documented still-processing
attachment response for the selected version and build number.

A separate F-Droid candidate-testing repository is planned outside this
public application repository; it is not part of the current workflow.

## Public release provenance

`release_provenance.py` creates and verifies the deterministic public
`release-provenance.json` beside the release assets. It accepts the exact tag,
version, source SHA, release run ID/number, Android version code, and a sorted
repeated asset allowlist. It fails closed if the directory contains anything
else, any entry is not a regular file, or hashes, sizes, metadata, or canonical
JSON disagree. The manifest deliberately contains public release metadata only.

```bash
python3 .github/scripts/release_provenance.py create --directory release \
  --tag v1.5.0 --version 1.5.0 \
  --source-sha 0123456789abcdef0123456789abcdef01234567 \
  --release-run-id 12345 --release-run-number 678 \
  --android-version-code 1005000 \
  --asset DobbyVPN.apk --asset DobbyVPN.zip
```
