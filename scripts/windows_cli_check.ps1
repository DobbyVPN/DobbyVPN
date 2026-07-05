param(
  [string]$Config = $env:DOBBYVPN_CLI_TEST_CONFIG,
  [int]$Port = 50151,
  [string]$Branch = $env:DOBBYVPN_ARTIFACT_BRANCH,
  [string]$Repository = $(if ($env:DOBBYVPN_GITHUB_REPOSITORY) { $env:DOBBYVPN_GITHUB_REPOSITORY } else { "DobbyVPN/DobbyVPN" }),
  [switch]$Help
)

$ErrorActionPreference = "Stop"

if ($env:PORT -and -not $PSBoundParameters.ContainsKey("Port")) {
  $Port = [int]$env:PORT
}

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$Workflow = "release.yml"
$ArtifactsDir = Join-Path $RootDir ".local-artifacts\windows"
$CliPath = $null
$ServiceProcess = $null

function Show-Usage {
  Write-Host @"
Usage:
  scripts\windows_cli_check.ps1 -Config <url-or-toml-file>

Options:
  -Config <value>   Config URL, local TOML file, or inline TOML.
  -Port <port>      gRPC VPN service port. Default: $Port
  -Branch <branch>  Artifact branch. Default: current git branch, or main.
  -Help             Show this help.
"@
}

function Write-Log {
  param([string]$Message)
  Write-Host "[+] $Message"
}

function Fail {
  param([string]$Message)
  throw "[!] $Message"
}

function Require-Gh {
  if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    Fail "GitHub CLI (gh) is required to download artifacts"
  }
}

function Get-GitHubRepository {
  try {
    $remote = git -C $RootDir remote get-url origin 2>$null
    $remote = $remote -replace '\.git$', ''

    if ($remote.StartsWith("git@github.com:")) {
      return $remote.Substring("git@github.com:".Length)
    }
    if ($remote.StartsWith("https://github.com/")) {
      return $remote.Substring("https://github.com/".Length)
    }
    if ($remote.StartsWith("http://github.com/")) {
      return $remote.Substring("http://github.com/".Length)
    }
  } catch {
  }

  return $Repository
}

function Get-ArtifactBranch {
  if (-not [string]::IsNullOrWhiteSpace($Branch)) {
    return $Branch
  }

  try {
    $gitBranch = git -C $RootDir rev-parse --abbrev-ref HEAD 2>$null
    if (-not [string]::IsNullOrWhiteSpace($gitBranch) -and $gitBranch -ne "HEAD") {
      return $gitBranch
    }
  } catch {
  }

  return "main"
}

function Invoke-Gh {
  param([string[]]$Arguments)

  $repo = Get-GitHubRepository
  & gh -R $repo @Arguments
  if ($LASTEXITCODE -ne 0) {
    Fail "gh command failed: gh -R $repo $($Arguments -join ' ')"
  }
}

function Get-LatestRunId {
  param([string]$BranchName)

  $json = Invoke-Gh -Arguments @(
    "run", "list",
    "--workflow", $Workflow,
    "--branch", $BranchName,
    "--status", "success",
    "--limit", "1",
    "--json", "databaseId"
  )
  $runs = @($json | ConvertFrom-Json)
  if ($runs.Count -eq 0 -or -not $runs[0].databaseId) {
    return $null
  }

  return [string]$runs[0].databaseId
}

function Download-Artifacts {
  Require-Gh

  $repo = Get-GitHubRepository
  $branchName = Get-ArtifactBranch
  Write-Log "Finding latest successful $Workflow run for ${repo}:${branchName}"

  $runId = Get-LatestRunId -BranchName $branchName
  if ([string]::IsNullOrWhiteSpace($runId) -and $branchName -ne "main") {
    Write-Log "No successful $Workflow run for $branchName; falling back to main"
    $branchName = "main"
    $runId = Get-LatestRunId -BranchName $branchName
  }
  if ([string]::IsNullOrWhiteSpace($runId)) {
    Fail "No successful $Workflow run found for ${repo}:${branchName}"
  }

  $appDir = Join-Path $ArtifactsDir "app"
  $serviceDir = Join-Path $ArtifactsDir "service"

  Remove-Item -Path $ArtifactsDir -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path @($appDir, $serviceDir) | Out-Null

  Invoke-Gh -Arguments @("run", "download", $runId, "--name", "dobbyVPN-windows.zip", "--dir", $appDir)
  Invoke-Gh -Arguments @("run", "download", $runId, "--name", "windows_grpcvpnserver.exe", "--dir", $serviceDir)
}

function Download-Wintun {
  $serviceDir = Join-Path $ArtifactsDir "service"
  $wintunDll = Join-Path $serviceDir "wintun.dll"
  if (Test-Path $wintunDll) {
    return
  }

  $zip = Join-Path $env:TEMP "wintun.zip"
  $extract = Join-Path $env:TEMP "dobby-wintun"

  Write-Log "Downloading Wintun"
  Remove-Item -Path $extract -Recurse -Force -ErrorAction SilentlyContinue
  Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $zip
  Expand-Archive -Path $zip -DestinationPath $extract
  Copy-Item -Path (Join-Path $extract "wintun\bin\amd64\wintun.dll") -Destination $wintunDll
}

function Get-ConfigArg {
  if ($Config -match '^https?://' -or (Test-Path $Config)) {
    return $Config
  }

  $configFile = Join-Path $ArtifactsDir "cli-test-config.toml"
  [System.IO.File]::WriteAllText($configFile, $Config, [System.Text.UTF8Encoding]::new($false))
  return $configFile
}

function Prepare-Cli {
  $zip = Join-Path $ArtifactsDir "app\dobbyVPN-windows.zip"
  $unpacked = Join-Path $ArtifactsDir "app\unpacked"

  if (-not (Test-Path $zip)) {
    Fail "Missing downloaded Windows app artifact: $zip"
  }

  Remove-Item -Path $unpacked -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $unpacked | Out-Null
  Write-Log "Unpacking downloaded desktop app"
  Expand-Archive -Path $zip -DestinationPath $unpacked

  $cli = Get-ChildItem -Path $unpacked -Recurse -Filter "Dobby Vpn.exe" | Select-Object -First 1
  if (-not $cli) {
    Fail "Dobby CLI executable was not found"
  }

  $script:CliPath = $cli.FullName
}

function Start-GrpcService {
  $service = Join-Path $ArtifactsDir "service\windows_grpcvpnserver.exe"
  if (-not (Test-Path $service)) {
    Fail "Missing downloaded service artifact: $service"
  }

  Write-Log "Starting gRPC VPN service on port $Port"
  $serviceDir = Split-Path -Parent $service
  $serviceOut = Join-Path $RootDir "grpcvpnserver.out"
  $serviceErr = Join-Path $RootDir "grpcvpnserver.err"

  $script:ServiceProcess = Start-Process `
    -FilePath $service `
    -WorkingDirectory $serviceDir `
    -ArgumentList @("-port", $Port) `
    -RedirectStandardOutput $serviceOut `
    -RedirectStandardError $serviceErr `
    -PassThru

  foreach ($attempt in 1..30) {
    try {
      $client = [Net.Sockets.TcpClient]::new("127.0.0.1", $Port)
      $client.Close()
      Write-Log "gRPC VPN service is ready"
      return
    } catch {
      Start-Sleep -Seconds 1
    }
  }

  if (Test-Path $serviceOut) { Get-Content $serviceOut }
  if (Test-Path $serviceErr) { Get-Content $serviceErr }
  Fail "gRPC VPN service did not start on port $Port"
}

function Run-Check {
  if ([string]::IsNullOrWhiteSpace($script:CliPath) -or -not (Test-Path $script:CliPath)) {
    Fail "Dobby CLI executable is not ready"
  }

  $cliOut = Join-Path $RootDir "dobby-cli.out"
  $cliErr = Join-Path $RootDir "dobby-cli.err"

  Write-Log "Running CLI config check"
  $env:PORT = "$Port"
  $process = Start-Process `
    -FilePath $script:CliPath `
    -ArgumentList @("check-config", (Get-ConfigArg)) `
    -Wait `
    -PassThru `
    -NoNewWindow `
    -RedirectStandardOutput $cliOut `
    -RedirectStandardError $cliErr

  if (Test-Path $cliOut) { Get-Content $cliOut }
  if (Test-Path $cliErr) { Get-Content $cliErr }
  if ($process.ExitCode -ne 0) {
    Fail "check-config failed with exit code $($process.ExitCode)"
  }
}

function Stop-GrpcService {
  if ($script:ServiceProcess -and -not $script:ServiceProcess.HasExited) {
    Write-Log "Stopping gRPC VPN service"
    Stop-Process -Id $script:ServiceProcess.Id -Force
  }
}

function Test-Admin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = [Security.Principal.WindowsPrincipal]::new($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if ($Help) {
  Show-Usage
  exit 0
}

if ([string]::IsNullOrWhiteSpace($Config)) {
  Fail "Pass -Config <url-or-file> or set DOBBYVPN_CLI_TEST_CONFIG"
}

if (-not (Test-Admin)) {
  Fail "Run PowerShell as Administrator so the VPN service can create and configure Wintun"
}

try {
  Download-Artifacts
  Download-Wintun
  Prepare-Cli
  Start-GrpcService
  Run-Check
  Write-Log "Done"
} finally {
  Stop-GrpcService
  $serviceOut = Join-Path $RootDir "grpcvpnserver.out"
  $serviceErr = Join-Path $RootDir "grpcvpnserver.err"
  if (Test-Path $serviceOut) { Get-Content $serviceOut }
  if (Test-Path $serviceErr) { Get-Content $serviceErr }
}
