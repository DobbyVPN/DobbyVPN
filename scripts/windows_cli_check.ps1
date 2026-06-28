param(
  [string]$Config = $env:DOBBYVPN_CLI_TEST_CONFIG,
  [int]$Port = 50151,
  [switch]$SkipDeps,
  [switch]$SkipBuild,
  [switch]$Help
)

$ErrorActionPreference = "Stop"

if ($env:PORT -and -not $PSBoundParameters.ContainsKey("Port")) {
  $Port = [int]$env:PORT
}

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$GoVersion = (Get-Content (Join-Path $RootDir ".go-version") -Raw).Trim()
$ToolsDir = Join-Path $RootDir ".local-tools\windows"
$ServiceProcess = $null

function Show-Usage {
  Write-Host @"
Usage:
  scripts\windows_cli_check.ps1 -Config <url-or-toml-file>

Builds and checks the local VPN path on Windows without Hydraulic Conveyor:
  Go gRPC VPN service -> Gradle desktop CLI -> check-config for every profile.

Options:
  -Config <value>   Config URL or local TOML file. Can also be set with DOBBYVPN_CLI_TEST_CONFIG.
  -Port <port>      gRPC VPN service port. Default: $Port
  -SkipDeps         Do not install local Go/JDK/Android SDK/Wintun dependencies.
  -SkipBuild        Reuse existing build outputs.
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

function Test-Admin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = [Security.Principal.WindowsPrincipal]::new($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-GoArch {
  switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { return "amd64" }
    "ARM64" { return "arm64" }
    default { Fail "Unsupported CPU architecture: $env:PROCESSOR_ARCHITECTURE" }
  }
}

function Get-AdoptiumArch {
  switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { return "x64" }
    "ARM64" { return "aarch64" }
    default { Fail "Unsupported CPU architecture: $env:PROCESSOR_ARCHITECTURE" }
  }
}

function Test-GoVersion {
  $candidates = @()
  $goCommand = Get-Command go -ErrorAction SilentlyContinue
  if ($goCommand) {
    $candidates += $goCommand.Source
  }
  $candidates += (Join-Path $ToolsDir "go-$GoVersion\bin\go.exe")

  foreach ($candidate in $candidates) {
    if ((Test-Path $candidate) -and ((& $candidate version) -match "go$GoVersion")) {
      return (Split-Path -Parent $candidate)
    }
  }

  return $null
}

function Install-Go {
  $goBinDir = Test-GoVersion
  if ($goBinDir) {
    $env:PATH = "$goBinDir;$env:PATH"
    Write-Log "Go $GoVersion already available"
    return
  }

  $arch = Get-GoArch
  $goRoot = Join-Path $ToolsDir "go-$GoVersion"
  if (-not (Test-Path (Join-Path $goRoot "bin\go.exe"))) {
    Write-Log "Installing Go $GoVersion locally"
    New-Item -ItemType Directory -Force -Path $ToolsDir | Out-Null
    $zip = Join-Path $env:TEMP "go$GoVersion.windows-$arch.zip"
    $extract = Join-Path $env:TEMP "dobby-go-$GoVersion"
    Remove-Item -Path @($extract, $goRoot) -Recurse -Force -ErrorAction SilentlyContinue
    Invoke-WebRequest -Uri "https://go.dev/dl/go$GoVersion.windows-$arch.zip" -OutFile $zip
    Expand-Archive -Path $zip -DestinationPath $extract
    Move-Item -Path (Join-Path $extract "go") -Destination $goRoot
  } else {
    Write-Log "Go $GoVersion already installed locally"
  }

  $env:PATH = "$(Join-Path $goRoot 'bin');$env:PATH"
}

function Find-Java17 {
  if ($env:JAVA_HOME) {
    $java = Join-Path $env:JAVA_HOME "bin\java.exe"
    if ((Test-Path $java) -and ((& $java -version 2>&1 | Out-String) -match 'version "17')) {
      return $env:JAVA_HOME
    }
  }

  $javaCommand = Get-Command java -ErrorAction SilentlyContinue
  if ($javaCommand -and ((& $javaCommand.Source -version 2>&1 | Out-String) -match 'version "17')) {
    return (Resolve-Path (Join-Path (Split-Path -Parent $javaCommand.Source) "..")).Path
  }

  return $null
}

function Install-Jdk {
  $javaHome = Find-Java17
  if ($javaHome) {
    $env:JAVA_HOME = $javaHome
    $env:PATH = "$env:JAVA_HOME\bin;$env:PATH"
    Write-Log "JDK 17 already available"
    return
  }

  $jdkRoot = Join-Path $ToolsDir "jdk-17"
  if (-not (Test-Path (Join-Path $jdkRoot "bin\java.exe"))) {
    Write-Log "Installing JDK 17 locally"
    New-Item -ItemType Directory -Force -Path $ToolsDir | Out-Null
    $zip = Join-Path $env:TEMP "temurin-17-windows-$(Get-AdoptiumArch).zip"
    $extract = Join-Path $env:TEMP "dobby-jdk-17"
    Remove-Item -Path @($extract, $jdkRoot) -Recurse -Force -ErrorAction SilentlyContinue
    Invoke-WebRequest -Uri "https://api.adoptium.net/v3/binary/latest/17/ga/windows/$(Get-AdoptiumArch)/jdk/hotspot/normal/eclipse" -OutFile $zip
    Expand-Archive -Path $zip -DestinationPath $extract
    $java = Get-ChildItem -Path $extract -Recurse -Filter java.exe | Where-Object { $_.FullName -like "*\bin\java.exe" } | Select-Object -First 1
    if (-not $java) {
      Fail "Downloaded JDK 17 archive did not contain java.exe"
    }
    $javaHome = (Resolve-Path (Join-Path $java.DirectoryName "..")).Path
    Move-Item -Path $javaHome -Destination $jdkRoot
  } else {
    Write-Log "JDK 17 already installed locally"
  }

  $env:JAVA_HOME = $jdkRoot
  $env:PATH = "$env:JAVA_HOME\bin;$env:PATH"
}

function Install-AndroidSdk {
  $sdkRoot = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { Join-Path $ToolsDir "android-sdk" }
  $env:ANDROID_HOME = $sdkRoot
  $env:ANDROID_SDK_ROOT = $sdkRoot
  $sdkManager = Join-Path $sdkRoot "cmdline-tools\latest\bin\sdkmanager.bat"
  $env:PATH = "$(Join-Path $sdkRoot 'cmdline-tools\latest\bin');$(Join-Path $sdkRoot 'platform-tools');$env:PATH"

  if ((Test-Path $sdkManager) -and
      (Test-Path (Join-Path $sdkRoot "platforms\android-35")) -and
      (Test-Path (Join-Path $sdkRoot "platforms\android-36")) -and
      (Test-Path (Join-Path $sdkRoot "build-tools\36.0.0"))) {
    Write-Log "Android SDK already installed"
    return
  }

  Write-Log "Installing Android SDK command line tools"
  $toolsZip = Join-Path $env:TEMP "android-commandlinetools-win.zip"
  $toolsDir = Join-Path $sdkRoot "cmdline-tools"
  New-Item -ItemType Directory -Force -Path $toolsDir | Out-Null
  Invoke-WebRequest -Uri "https://dl.google.com/android/repository/commandlinetools-win-11076708_latest.zip" -OutFile $toolsZip
  Remove-Item -Path @((Join-Path $toolsDir "latest"), (Join-Path $toolsDir "cmdline-tools")) -Recurse -Force -ErrorAction SilentlyContinue
  Expand-Archive -Path $toolsZip -DestinationPath $toolsDir
  Move-Item -Path (Join-Path $toolsDir "cmdline-tools") -Destination (Join-Path $toolsDir "latest")

  1..20 | ForEach-Object { "y" } | & $sdkManager --licenses | Out-Host
  & $sdkManager "platforms;android-35" "platforms;android-36" "build-tools;36.0.0" "platform-tools"
}

function Prepare-CloakInternal {
  $sourceDir = Join-Path $RootDir "Cloak\internal"
  $targetDir = Join-Path $RootDir "go_module\modules\Cloak\internal"

  if (-not (Test-Path $sourceDir)) {
    Write-Log "Initializing git submodules"
    git -C $RootDir submodule update --init --recursive
  }
  if (-not (Test-Path $sourceDir)) {
    Fail "Missing $sourceDir after submodule initialization"
  }

  if (Test-Path $targetDir) {
    Write-Log "Cloak/internal already vendored"
    return
  }

  Write-Log "Vendoring Cloak/internal into go_module/modules/Cloak"
  Copy-Item -Path $sourceDir -Destination $targetDir -Recurse
}

function Install-Wintun {
  $serviceDir = Join-Path $RootDir "kmp_module\services"
  $wintunDll = Join-Path $serviceDir "wintun.dll"
  if (Test-Path $wintunDll) {
    Write-Log "wintun.dll already available"
    return
  }

  $arch = Get-GoArch
  $zip = Join-Path $env:TEMP "wintun.zip"
  $extract = Join-Path $env:TEMP "dobby-wintun"
  Write-Log "Downloading Wintun"
  New-Item -ItemType Directory -Force -Path $serviceDir | Out-Null
  Remove-Item -Path $extract -Recurse -Force -ErrorAction SilentlyContinue
  Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $zip
  Expand-Archive -Path $zip -DestinationPath $extract
  Copy-Item -Path (Join-Path $extract "wintun\bin\$arch\wintun.dll") -Destination $wintunDll
}

function Build-Service {
  $service = Join-Path $RootDir "go_module\windows_grpcvpnserver.exe"
  $serviceTarget = Join-Path $RootDir "kmp_module\services\windows_grpcvpnserver.exe"

  if ($SkipBuild -and (Test-Path $service)) {
    Write-Log "Reusing existing gRPC VPN service"
  } else {
    Write-Log "Building Windows gRPC VPN service"
    Push-Location (Join-Path $RootDir "go_module")
    try {
      $env:CGO_ENABLED = "1"
      $env:GOOS = "windows"
      $env:GOARCH = Get-GoArch
      go build -trimpath "-ldflags=-buildid=" -o windows_grpcvpnserver.exe ./desktop_exports/
    } finally {
      Pop-Location
    }
  }

  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $serviceTarget) | Out-Null
  Copy-Item -Path $service -Destination $serviceTarget -Force
}

function Build-DesktopCli {
  if ($SkipBuild) {
    Write-Log "Skipping Gradle build"
    return
  }

  Write-Log "Building desktop JVM app"
  Push-Location (Join-Path $RootDir "kmp_module")
  try {
    .\gradlew.bat --build-cache --parallel :app:jvmJar
  } finally {
    Pop-Location
  }
}

function Start-GrpcService {
  Write-Log "Starting gRPC VPN service on port $Port"
  $service = (Resolve-Path (Join-Path $RootDir "kmp_module\services\windows_grpcvpnserver.exe")).Path
  $serviceDir = Split-Path -Parent $service
  $serviceOut = Join-Path $RootDir "grpcvpnserver.out"
  $serviceErr = Join-Path $RootDir "grpcvpnserver.err"

  $script:ServiceProcess = Start-Process -FilePath $service -WorkingDirectory $serviceDir -ArgumentList @("-port", $Port) -RedirectStandardOutput $serviceOut -RedirectStandardError $serviceErr -PassThru

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

function Run-CliCheck {
  Write-Log "Running CLI config check"
  Push-Location (Join-Path $RootDir "kmp_module")
  try {
    $env:PORT = "$Port"
    .\gradlew.bat --quiet :app:run "--args=check-config $Config"
  } finally {
    Pop-Location
  }
}

function Stop-GrpcService {
  if ($script:ServiceProcess -and -not $script:ServiceProcess.HasExited) {
    Write-Log "Stopping gRPC VPN service"
    Stop-Process -Id $script:ServiceProcess.Id -Force
  }
}

if ($Help) {
  Show-Usage
  exit 0
}

if ([string]::IsNullOrWhiteSpace($Config)) {
  Fail "Pass -Config <url-or-file> or set DOBBYVPN_CLI_TEST_CONFIG"
}

if (-not (Test-Path (Join-Path $RootDir "go_module")) -or -not (Test-Path (Join-Path $RootDir "kmp_module"))) {
  Fail "Run this from a cloned DobbyVPN repository"
}

if (-not (Test-Admin)) {
  Fail "Run PowerShell as Administrator so the VPN service can create and configure Wintun"
}

try {
  if ($SkipDeps) {
    Write-Log "Skipping dependency installation"
  } else {
    Install-Go
    Install-Jdk
    Install-AndroidSdk
    Install-Wintun
  }

  Prepare-CloakInternal
  Build-Service
  Build-DesktopCli
  Start-GrpcService
  Run-CliCheck
  Write-Log "Done"
} finally {
  Stop-GrpcService
  $serviceOut = Join-Path $RootDir "grpcvpnserver.out"
  $serviceErr = Join-Path $RootDir "grpcvpnserver.err"
  if (Test-Path $serviceOut) { Get-Content $serviceOut }
  if (Test-Path $serviceErr) { Get-Content $serviceErr }
}
