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
python .github/scripts/desktop_build.py libs --platform windows --arch amd64 --go-mod-tidy
```

The desktop app build uses service binaries downloaded into `kmp_module/services`:

```bash
python .github/scripts/desktop_build.py app --skip-libs --require-all-services
```

`--go-mod-tidy` is intentionally explicit. CI uses it to preserve the previous
workflow behavior; local service builds only run `go mod download` by default.

Use `--skip-deps` to require dependencies to already exist and `--skip-build` to
reuse existing build outputs where supported.
