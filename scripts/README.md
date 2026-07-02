# Local CLI VPN checks

These scripts run `check-config` through downloaded desktop CI artifacts.

Flow:

1. Use the current git branch, or `main` when there is no git branch.
2. Download the latest successful `release.yml` artifacts for that branch,
   falling back to `main` if that branch has no successful run.
3. Start the downloaded gRPC VPN service.
4. Run the downloaded desktop CLI.

The config can be an HTTP(S) URL, a local TOML file, or inline TOML through
`DOBBYVPN_CLI_TEST_CONFIG`.

GitHub CLI (`gh`) must be installed and authenticated. If the repository cannot
be inferred from git, the scripts use `DobbyVPN/DobbyVPN`. Override it with
`DOBBYVPN_GITHUB_REPOSITORY` when needed.

## Run

Ubuntu:

```bash
scripts/ubuntu_cli_check.sh --config 'https://example.com/config.toml'
```

macOS:

```bash
scripts/macos_cli_check.sh --config 'https://example.com/config.toml'
```

Windows, from an Administrator PowerShell:

```powershell
.\scripts\windows_cli_check.ps1 -Config "https://example.com/config.toml"
```

## Options

Use a different service port:

```bash
scripts/ubuntu_cli_check.sh --config config.toml --port 50152
scripts/macos_cli_check.sh --config config.toml --port 50152
```

```powershell
.\scripts\windows_cli_check.ps1 -Config config.toml -Port 50152
```

Use artifacts from a specific branch:

```bash
scripts/ubuntu_cli_check.sh --config config.toml --branch feature/foo
scripts/macos_cli_check.sh --config config.toml --branch feature/foo
```

```powershell
.\scripts\windows_cli_check.ps1 -Config config.toml -Branch feature/foo
```

## Local Files

The scripts may create:

- `.local-artifacts/`
- `grpcvpnserver.log`, `grpcvpnserver.out`, `grpcvpnserver.err`
- `dobby-cli.out`, `dobby-cli.err`

These runtime files are ignored by git.
