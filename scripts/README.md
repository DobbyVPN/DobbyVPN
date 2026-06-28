# Local CLI VPN checks

These scripts build and run the local desktop VPN.
Each script does the same high-level flow:

1. Install or locate local build dependencies.
2. Prepare `Cloak/internal`.
3. Build the platform gRPC VPN service from `go_module/desktop_exports`.
4. Build the desktop JVM app.
5. Start the gRPC VPN service locally.
6. Run the CLI command `check-config` against every profile from the provided config.

The config can be either an HTTP(S) URL or a local TOML file.

## Ubuntu

Run from the repository root:

```bash
scripts/ubuntu_cli_check.sh --config 'https://example.com/config.toml'
```

The Ubuntu script installs missing APT packages, Go from `.go-version`, and the
Android SDK command line tools. It starts `ubuntu_grpcvpnserver` with `sudo`
because the VPN service needs elevated privileges.

## macOS

Run from the repository root:

```bash
scripts/macos_cli_check.sh --config 'https://example.com/config.toml'
```

The macOS script installs Go, JDK 17, and Android SDK command line tools into
`.local-tools/macos` when they are not already available. It requires Xcode
Command Line Tools and starts `macos_grpcvpnserver` with `sudo`.

## Windows

Run PowerShell as Administrator from the repository root:

```powershell
.\scripts\windows_cli_check.ps1 -Config "https://example.com/config.toml"
```

The Windows script installs Go, JDK 17, Android SDK command line tools, and
`wintun.dll` locally when they are missing. Administrator privileges are required
so the VPN service can create and configure Wintun.

## Common options

Use a different service port:

```bash
scripts/ubuntu_cli_check.sh --config 'https://example.com/config.toml' --port 50152
scripts/macos_cli_check.sh --config 'https://example.com/config.toml' --port 50152
```

```powershell
.\scripts\windows_cli_check.ps1 -Config "https://example.com/config.toml" -Port 50152
```

Reuse existing dependencies:

```bash
scripts/ubuntu_cli_check.sh --config config.toml --skip-deps
scripts/macos_cli_check.sh --config config.toml --skip-deps
```

```powershell
.\scripts\windows_cli_check.ps1 -Config config.toml -SkipDeps
```

Reuse existing build outputs:

```bash
scripts/ubuntu_cli_check.sh --config config.toml --skip-build
scripts/macos_cli_check.sh --config config.toml --skip-build
```

```powershell
.\scripts\windows_cli_check.ps1 -Config config.toml -SkipBuild
```

The config can also be passed through `DOBBYVPN_CLI_TEST_CONFIG` on all
platforms.

## Local files

The scripts may create these local files and directories:

- `.local-tools/` for local toolchains on macOS and Windows.
- `kmp_module/services/` for copied service binaries and `wintun.dll`.
- `grpcvpnserver.log`, `grpcvpnserver.out`, and `grpcvpnserver.err` for service logs.

These runtime files are ignored by git where appropriate.
