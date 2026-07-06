#!/usr/bin/env python3
from __future__ import annotations

import argparse
import ctypes
import os
import platform
import shutil
import socket
import subprocess
import tarfile
import tempfile
import time
import urllib.request
import zipfile
from pathlib import Path


ROOT_DIR = Path(__file__).resolve().parents[2]
GO_MODULE_DIR = ROOT_DIR / "go_module"
KMP_DIR = ROOT_DIR / "kmp_module"
SERVICES_DIR = KMP_DIR / "services"
TOOLS_DIR = ROOT_DIR / ".local-tools" / "desktop-build"

ANDROID_PACKAGES = (
    "platforms;android-35",
    "platforms;android-36",
    "build-tools;36.0.0",
    "platform-tools",
)
ANDROID_TOOLS_VERSION = "11076708"
WINTUN_VERSION = "0.14.1"

SERVICE_NAMES = {
    "linux": "ubuntu_grpcvpnserver",
    "macos": "macos_grpcvpnserver",
    "windows": "windows_grpcvpnserver.exe",
}
GOOS_BY_PLATFORM = {
    "linux": "linux",
    "macos": "darwin",
    "windows": "windows",
}
CI_ARCH_BY_PLATFORM = {
    "linux": "amd64",
    "macos": "arm64",
    "windows": "amd64",
}


def log(message: str) -> None:
    print(f"[+] {message}", flush=True)


def fail(message: str) -> None:
    raise SystemExit(f"[!] {message}")


def run(
    command: list[str],
    cwd: Path = ROOT_DIR,
    env: dict[str, str] | None = None,
    input_text: str | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    printable = " ".join(command)
    log(f"$ {printable}")
    try:
        result = subprocess.run(
            command,
            cwd=str(cwd),
            env=env or os.environ.copy(),
            input=input_text,
            text=True,
        )
    except FileNotFoundError as error:
        fail(f"Command was not found: {error.filename}")
    if check and result.returncode != 0:
        fail(f"Command failed with exit code {result.returncode}: {printable}")
    return result


def run_capture(command: list[str], cwd: Path = ROOT_DIR) -> str | None:
    try:
        result = subprocess.run(
            command,
            cwd=str(cwd),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
    except FileNotFoundError:
        return None
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def set_env(name: str, value: str) -> None:
    os.environ[name] = value
    github_env = os.environ.get("GITHUB_ENV")
    if github_env:
        with open(github_env, "a", encoding="utf-8") as handle:
            handle.write(f"{name}={value}\n")


def prepend_path(path: Path) -> None:
    path_str = str(path)
    if not path.exists():
        return
    current = os.environ.get("PATH", "")
    parts = current.split(os.pathsep) if current else []
    if path_str not in parts:
        os.environ["PATH"] = path_str + os.pathsep + current
    github_path = os.environ.get("GITHUB_PATH")
    if github_path:
        with open(github_path, "a", encoding="utf-8") as handle:
            handle.write(path_str + "\n")


def download(url: str, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    log(f"Downloading {url}")
    if shutil.which("curl"):
        run(
            [
                "curl",
                "--fail",
                "--location",
                "--show-error",
                "--silent",
                "--http1.1",
                "--retry",
                "5",
                "--retry-delay",
                "2",
                "--connect-timeout",
                "60",
                "--continue-at",
                "-",
                url,
                "-o",
                str(output),
            ]
        )
        return

    request = urllib.request.Request(url, headers={"User-Agent": "DobbyVPN desktop_build.py"})
    with urllib.request.urlopen(request, timeout=120) as response:
        with open(output, "wb") as handle:
            shutil.copyfileobj(response, handle)


def host_platform() -> str:
    system = platform.system().lower()
    if system == "linux":
        return "linux"
    if system == "darwin":
        return "macos"
    if system == "windows":
        return "windows"
    fail(f"Unsupported host platform: {platform.system()}")


def normalize_platform(value: str) -> str:
    aliases = {
        "current": "current",
        "all": "all",
        "ubuntu": "linux",
        "linux": "linux",
        "darwin": "macos",
        "mac": "macos",
        "macos": "macos",
        "windows": "windows",
        "win": "windows",
    }
    normalized = aliases.get(value.lower())
    if not normalized:
        fail(f"Unsupported platform: {value}")
    return normalized


def selected_platforms(value: str) -> list[str]:
    normalized = normalize_platform(value)
    if normalized == "current":
        return [host_platform()]
    if normalized == "all":
        return ["linux", "macos", "windows"]
    return [normalized]


def go_arch_from_machine() -> str:
    machine = platform.machine().lower()
    if machine in ("x86_64", "amd64"):
        return "amd64"
    if machine in ("aarch64", "arm64"):
        return "arm64"
    fail(f"Unsupported CPU architecture: {platform.machine()}")


def adoptium_arch() -> str:
    arch = go_arch_from_machine()
    if arch == "amd64":
        return "x64"
    if arch == "arm64":
        return "aarch64"
    fail(f"Unsupported CPU architecture: {platform.machine()}")


def go_version() -> str:
    return (ROOT_DIR / ".go-version").read_text(encoding="utf-8").strip()


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None


def bootstrap_local_tools() -> None:
    version = go_version()
    prepend_path(TOOLS_DIR / f"go-{version}" / "bin")
    prepend_path(TOOLS_DIR / "jdk-17" / "bin")
    prepend_path(TOOLS_DIR / "android-sdk" / "cmdline-tools" / "latest" / "bin")
    prepend_path(TOOLS_DIR / "android-sdk" / "platform-tools")


def local_go_root() -> Path:
    return TOOLS_DIR / f"go-{go_version()}"


def find_go() -> Path | None:
    version = go_version()
    candidates: list[Path] = []
    if shutil.which("go"):
        candidates.append(Path(shutil.which("go") or ""))
    candidates.append(local_go_root() / "bin" / ("go.exe" if host_platform() == "windows" else "go"))

    for candidate in candidates:
        if candidate.exists():
            output = run_capture([str(candidate), "version"])
            if output and f"go{version}" in output:
                return candidate.parent
    return None


def install_go(skip_deps: bool) -> None:
    found = find_go()
    if found:
        prepend_path(found)
        log(f"Go {go_version()} already available")
        return
    if skip_deps:
        fail(f"Go {go_version()} is required")

    current = host_platform()
    goos = {"linux": "linux", "macos": "darwin", "windows": "windows"}[current]
    arch = go_arch_from_machine()
    suffix = "zip" if current == "windows" else "tar.gz"
    archive = TOOLS_DIR / "downloads" / f"go{go_version()}.{goos}-{arch}.{suffix}"
    extract_dir = Path(tempfile.mkdtemp(prefix="dobby-go-"))
    go_root = local_go_root()

    download(f"https://go.dev/dl/go{go_version()}.{goos}-{arch}.{suffix}", archive)
    shutil.rmtree(go_root, ignore_errors=True)
    try:
        if suffix == "zip":
            with zipfile.ZipFile(archive) as zip_file:
                zip_file.extractall(extract_dir)
        else:
            with tarfile.open(archive) as tar_file:
                tar_file.extractall(extract_dir)
        shutil.move(str(extract_dir / "go"), go_root)
    finally:
        shutil.rmtree(extract_dir, ignore_errors=True)

    prepend_path(go_root / "bin")
    log(f"Installed Go {go_version()} into {go_root}")


def java_executable_name() -> str:
    return "java.exe" if host_platform() == "windows" else "java"


def java_home_from_executable(java_path: Path) -> Path:
    return java_path.resolve().parent.parent


def is_java_17(java_path: Path) -> bool:
    output = run_capture([str(java_path), "-version"])
    return bool(output and 'version "17' in output)


def find_java_17() -> Path | None:
    java_name = java_executable_name()
    java_home = os.environ.get("JAVA_HOME")
    if java_home:
        java = Path(java_home) / "bin" / java_name
        if java.exists() and is_java_17(java):
            return Path(java_home)

    if host_platform() == "macos":
        output = run_capture(["/usr/libexec/java_home", "-v", "17"])
        if output:
            candidate_home = Path(output)
            candidate_java = candidate_home / "bin" / java_name
            if candidate_java.exists() and is_java_17(candidate_java):
                return candidate_home

    java = shutil.which("java")
    if java and is_java_17(Path(java)):
        return java_home_from_executable(Path(java))

    local_java = TOOLS_DIR / "jdk-17" / "bin" / java_name
    if local_java.exists() and is_java_17(local_java):
        return TOOLS_DIR / "jdk-17"

    return None


def install_jdk(skip_deps: bool) -> None:
    found = find_java_17()
    if found:
        set_env("JAVA_HOME", str(found))
        prepend_path(found / "bin")
        log("JDK 17 already available")
        return
    if skip_deps:
        fail("JDK 17 is required")

    current = host_platform()
    adoptium_os = {"linux": "linux", "macos": "mac", "windows": "windows"}[current]
    suffix = "zip" if current == "windows" else "tar.gz"
    archive = TOOLS_DIR / "downloads" / f"temurin-17-{adoptium_os}-{adoptium_arch()}.{suffix}"
    extract_dir = Path(tempfile.mkdtemp(prefix="dobby-jdk-"))
    jdk_root = TOOLS_DIR / "jdk-17"
    url = (
        "https://api.adoptium.net/v3/binary/latest/17/ga/"
        f"{adoptium_os}/{adoptium_arch()}/jdk/hotspot/normal/eclipse"
    )

    download(url, archive)
    shutil.rmtree(jdk_root, ignore_errors=True)
    try:
        if suffix == "zip":
            with zipfile.ZipFile(archive) as zip_file:
                zip_file.extractall(extract_dir)
        else:
            with tarfile.open(archive) as tar_file:
                tar_file.extractall(extract_dir)

        java_name = java_executable_name()
        java_files = list(extract_dir.rglob(f"bin/{java_name}"))
        if not java_files:
            fail("Downloaded JDK archive does not contain java")
        shutil.move(str(java_home_from_executable(java_files[0])), jdk_root)
    finally:
        shutil.rmtree(extract_dir, ignore_errors=True)

    set_env("JAVA_HOME", str(jdk_root))
    prepend_path(jdk_root / "bin")
    log(f"Installed JDK 17 into {jdk_root}")


def sdkmanager_name() -> str:
    return "sdkmanager.bat" if host_platform() == "windows" else "sdkmanager"


def infer_android_home_from_sdkmanager(sdkmanager: Path) -> Path | None:
    try:
        return sdkmanager.resolve().parents[3]
    except IndexError:
        return None


def find_sdkmanager() -> tuple[Path, Path] | None:
    sdkmanager = shutil.which(sdkmanager_name())
    if sdkmanager:
        android_home = infer_android_home_from_sdkmanager(Path(sdkmanager))
        if android_home:
            return Path(sdkmanager), android_home

    for env_name in ("ANDROID_HOME", "ANDROID_SDK_ROOT"):
        sdk_root = os.environ.get(env_name)
        if not sdk_root:
            continue
        manager = Path(sdk_root) / "cmdline-tools" / "latest" / "bin" / sdkmanager_name()
        if manager.exists():
            return manager, Path(sdk_root)

    sdk_root = TOOLS_DIR / "android-sdk"
    manager = sdk_root / "cmdline-tools" / "latest" / "bin" / sdkmanager_name()
    if manager.exists():
        return manager, sdk_root
    return None


def android_packages_installed(sdk_root: Path) -> bool:
    return (
        (sdk_root / "platforms" / "android-35").is_dir()
        and (sdk_root / "platforms" / "android-36").is_dir()
        and (sdk_root / "build-tools" / "36.0.0").is_dir()
    )


def configure_android_env(sdk_root: Path) -> None:
    set_env("ANDROID_HOME", str(sdk_root))
    set_env("ANDROID_SDK_ROOT", str(sdk_root))
    prepend_path(sdk_root / "cmdline-tools" / "latest" / "bin")
    prepend_path(sdk_root / "platform-tools")


def ensure_android_tools_executable(sdk_root: Path) -> None:
    if host_platform() == "windows":
        return
    tools_bin = sdk_root / "cmdline-tools" / "latest" / "bin"
    if not tools_bin.is_dir():
        return
    for tool in tools_bin.iterdir():
        if tool.is_file():
            try:
                tool.chmod(tool.stat().st_mode | 0o111)
            except PermissionError:
                if TOOLS_DIR in sdk_root.resolve().parents:
                    fail(f"Android SDK tool is not writable: {tool}")
                log(f"Android SDK tools are not writable, leaving permissions unchanged: {tools_bin}")
                return


def install_android_sdk(skip_deps: bool) -> None:
    found = find_sdkmanager()
    if found:
        sdkmanager, sdk_root = found
        configure_android_env(sdk_root)
        ensure_android_tools_executable(sdk_root)
        if android_packages_installed(sdk_root):
            log("Android SDK already available")
            return
        if skip_deps:
            fail("Android SDK packages are required")
    elif skip_deps:
        fail("Android SDK command line tools are required")
    else:
        sdk_root = Path(
            os.environ.get("ANDROID_HOME")
            or os.environ.get("ANDROID_SDK_ROOT")
            or TOOLS_DIR / "android-sdk"
        )
        configure_android_env(sdk_root)
        sdkmanager = sdk_root / "cmdline-tools" / "latest" / "bin" / sdkmanager_name()

        current = host_platform()
        tools_os = {"linux": "linux", "macos": "mac", "windows": "win"}[current]
        tools_zip = TOOLS_DIR / "downloads" / f"android-commandlinetools-{tools_os}.zip"
        tools_dir = sdk_root / "cmdline-tools"

        download(
            "https://dl.google.com/android/repository/"
            f"commandlinetools-{tools_os}-{ANDROID_TOOLS_VERSION}_latest.zip",
            tools_zip,
        )
        shutil.rmtree(tools_dir / "latest", ignore_errors=True)
        shutil.rmtree(tools_dir / "cmdline-tools", ignore_errors=True)
        tools_dir.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(tools_zip) as zip_file:
            zip_file.extractall(tools_dir)
        shutil.move(str(tools_dir / "cmdline-tools"), str(tools_dir / "latest"))

    if not sdkmanager.exists():
        fail(f"sdkmanager was not found at {sdkmanager}")

    ensure_android_tools_executable(sdk_root)
    configure_android_env(sdk_root)
    run([str(sdkmanager), "--licenses"], input_text="y\n" * 100, check=False)
    run([str(sdkmanager), *ANDROID_PACKAGES])
    log("Android SDK packages are installed")


def install_linux_packages(skip_deps: bool) -> None:
    if host_platform() != "linux":
        return
    required_commands = ["curl", "unzip", "zip", "git", "gcc", "g++"]
    missing_commands = [name for name in required_commands if not command_exists(name)]
    if not missing_commands:
        return
    if skip_deps:
        fail(f"Missing required commands: {', '.join(missing_commands)}")
    if not command_exists("apt-get"):
        fail(f"Install required commands manually: {', '.join(missing_commands)}")

    sudo = [] if os.geteuid() == 0 else ["sudo"]
    packages = [
        "ca-certificates",
        "curl",
        "unzip",
        "zip",
        "git",
        "build-essential",
        "gcc",
        "g++",
        "pkg-config",
        "iproute2",
    ]
    run([*sudo, "apt-get", "update"])
    run([*sudo, "apt-get", "install", "-y", *packages])


def ensure_compiler(target_platform: str, skip_deps: bool) -> None:
    if target_platform == "linux":
        install_linux_packages(skip_deps)
        if not command_exists("gcc") or not command_exists("g++"):
            fail("gcc and g++ are required for the Linux gRPC VPN service")
    elif target_platform == "macos":
        if run_capture(["xcode-select", "-p"]):
            return
        if not skip_deps:
            run(["xcode-select", "--install"], check=False)
        fail("Install Xcode Command Line Tools, then run the script again")
    elif target_platform == "windows":
        if command_exists("gcc"):
            return
        mingw_bin = Path("C:/ProgramData/chocolatey/lib/mingw/tools/install/mingw64/bin")
        prepend_path(mingw_bin)
        if command_exists("gcc"):
            return
        if skip_deps:
            fail("MinGW gcc is required for the Windows gRPC VPN service")
        if not command_exists("choco"):
            fail("Install MinGW gcc manually or install Chocolatey")
        run(["choco", "install", "mingw", "-y"])
        prepend_path(mingw_bin)
        if not command_exists("gcc"):
            fail("MinGW gcc was not found after installation")


def install_wintun(skip_deps: bool) -> None:
    if host_platform() != "windows":
        return
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    target = SERVICES_DIR / "wintun.dll"
    if target.exists():
        log("wintun.dll already available")
        return
    if skip_deps:
        fail("wintun.dll is required for Windows CLI checks")

    arch = go_arch_from_machine()
    archive = TOOLS_DIR / "downloads" / f"wintun-{WINTUN_VERSION}.zip"
    extract_dir = Path(tempfile.mkdtemp(prefix="dobby-wintun-"))
    download(f"https://www.wintun.net/builds/wintun-{WINTUN_VERSION}.zip", archive)
    try:
        with zipfile.ZipFile(archive) as zip_file:
            zip_file.extractall(extract_dir)
        shutil.copyfile(extract_dir / "wintun" / "bin" / arch / "wintun.dll", target)
    finally:
        shutil.rmtree(extract_dir, ignore_errors=True)
    log(f"Installed {target}")


def ensure_build_dependencies(target_platform: str, skip_deps: bool, need_android: bool) -> None:
    install_linux_packages(skip_deps)
    install_go(skip_deps)
    ensure_compiler(target_platform, skip_deps)
    if need_android:
        install_jdk(skip_deps)
        install_android_sdk(skip_deps)


def prepare_cloak_internal() -> None:
    source_dir = ROOT_DIR / "Cloak" / "internal"
    target_dir = GO_MODULE_DIR / "modules" / "Cloak" / "internal"
    if not source_dir.is_dir():
        log("Initializing git submodules")
        run(["git", "-C", str(ROOT_DIR), "submodule", "update", "--init", "--recursive"])
    if not source_dir.is_dir():
        fail(f"Missing {source_dir} after submodule initialization")

    target_dir.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source_dir, target_dir, dirs_exist_ok=True)
    log("Vendored Cloak/internal into go_module/modules/Cloak")


def go_mod_download(run_tidy: bool) -> None:
    if run_tidy:
        run(["go", "mod", "tidy"], cwd=GO_MODULE_DIR)
    run(["go", "mod", "download"], cwd=GO_MODULE_DIR)


def service_output_path(target_platform: str) -> Path:
    return GO_MODULE_DIR / SERVICE_NAMES[target_platform]


def service_target_path(target_platform: str) -> Path:
    return SERVICES_DIR / SERVICE_NAMES[target_platform]


def default_service_arch(target_platform: str) -> str:
    if os.environ.get("GITHUB_ACTIONS") == "true":
        return CI_ARCH_BY_PLATFORM[target_platform]
    if target_platform == host_platform():
        return go_arch_from_machine()
    return CI_ARCH_BY_PLATFORM[target_platform]


def build_service(
    target_platform: str,
    arch: str | None,
    skip_deps: bool,
    skip_build: bool,
    run_go_mod_tidy: bool,
) -> None:
    target_arch = arch or default_service_arch(target_platform)
    ensure_build_dependencies(target_platform, skip_deps, need_android=False)
    prepare_cloak_internal()
    go_mod_download(run_go_mod_tidy)

    output = service_output_path(target_platform)
    if skip_build and output.exists():
        log(f"Reusing existing {output.name}")
    else:
        log(f"Building {target_platform} gRPC VPN service for {target_arch}")
        env = os.environ.copy()
        env.update(
            {
                "CGO_ENABLED": "1",
                "GOOS": GOOS_BY_PLATFORM[target_platform],
                "GOARCH": target_arch,
            }
        )
        run(
            [
                "go",
                "build",
                "-trimpath",
                "-ldflags=-buildid=",
                "-o",
                output.name,
                "./desktop_exports/",
            ],
            cwd=GO_MODULE_DIR,
            env=env,
        )

    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    target = service_target_path(target_platform)
    shutil.copyfile(output, target)
    if target_platform != "windows":
        target.chmod(target.stat().st_mode | 0o111)
    log(f"Copied {output.name} to {target}")


def read_gradle_properties() -> dict[str, str]:
    properties: dict[str, str] = {}
    for line in (KMP_DIR / "gradle.properties").read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        properties[key.strip()] = value.strip()
    return properties


def github_repo_from_remote() -> str | None:
    remote = run_capture(["git", "remote", "get-url", "origin"])
    if not remote or "github.com" not in remote:
        return None
    remote = remote.removesuffix(".git")
    if remote.startswith("git@github.com:"):
        return remote.removeprefix("git@github.com:")
    marker = "github.com/"
    if marker in remote:
        return remote.split(marker, 1)[1]
    return None


def desktop_version_properties() -> list[str]:
    gradle_properties = read_gradle_properties()
    major = os.environ.get("APP_MAJOR_VERSION")
    minor = os.environ.get("APP_MINOR_VERSION")
    maintenance = os.environ.get("APP_MAINTENANCE_VERSION")

    if major is not None and minor is not None and maintenance is not None:
        version_name = f"{major}.{minor}.{maintenance}"
        version_code = str(int(major) * 1_000_000 + int(minor) * 1_000 + int(maintenance))
    else:
        version_name = os.environ.get("VERSION_NAME") or gradle_properties.get("versionName", "0.0.1")
        version_code = (
            os.environ.get("VERSION_CODE")
            or os.environ.get("ANDROID_VERSION_CODE")
            or gradle_properties.get("versionCode", "1")
        )

    commit = os.environ.get("GITHUB_SHA") or run_capture(["git", "rev-parse", "HEAD"]) or "N/A"
    repo = os.environ.get("GITHUB_REPOSITORY") or github_repo_from_remote() or "DobbyVPN/DobbyVPN"
    commit_link = os.environ.get("PROJECT_REPOSITORY_COMMIT_LINK")
    if not commit_link:
        commit_link = "N/A" if commit == "N/A" else f"https://github.com/{repo}/tree/{commit}"

    return [
        f"-PprojectRepositoryCommit={commit}",
        f"-PprojectRepositoryCommitLink={commit_link}",
        f"-Pandroid.injected.version.code={version_code}",
        f"-Pandroid.injected.version.name={version_name}",
    ]


def gradle_command() -> str:
    if host_platform() == "windows":
        return str(KMP_DIR / "gradlew.bat")
    return "./gradlew"


def run_desktop_gradle(skip_deps: bool) -> None:
    install_jdk(skip_deps)
    install_android_sdk(skip_deps)

    props = desktop_version_properties()
    run([gradle_command(), "--build-cache", "--parallel", ":app:jvmJar", *props], cwd=KMP_DIR)
    run([gradle_command(), "--no-daemon", "-q", "dependencies", *props], cwd=KMP_DIR)
    run([gradle_command(), "--no-daemon", "-q", "printConveyorConfig", *props], cwd=KMP_DIR)


def required_service_platforms(require_all: bool, platform_value: str) -> list[str]:
    if require_all:
        return ["linux", "macos", "windows"]
    platforms = selected_platforms(platform_value)
    if platforms == ["linux", "macos", "windows"]:
        return platforms
    return platforms


def require_services(require_all: bool, platform_value: str) -> None:
    missing = []
    for target_platform in required_service_platforms(require_all, platform_value):
        target = service_target_path(target_platform)
        if not target.exists():
            missing.append(str(target))
            continue
        if target_platform != "windows":
            target.chmod(target.stat().st_mode | 0o111)
    if missing:
        fail("Missing service binaries:\n" + "\n".join(missing))


def run_conveyor(passphrase: str | None) -> None:
    conveyor = os.environ.get("CONVEYOR_CMD") or shutil.which("conveyor")
    if not conveyor:
        fail("Conveyor CLI was not found. Set CONVEYOR_CMD or run without --package.")
    command = [conveyor, "make", "site", "-f", str(KMP_DIR / "conveyor.conf")]
    if passphrase:
        command.insert(3, f"--passphrase={passphrase}")
    run(command)


def build_app(args: argparse.Namespace) -> None:
    if not args.skip_libs:
        for target_platform in selected_platforms(args.platform):
            build_service(
                target_platform,
                args.arch,
                args.skip_deps,
                args.skip_build,
                args.go_mod_tidy,
            )

    if args.require_all_services:
        require_services(True, args.platform)
    run_desktop_gradle(args.skip_deps)
    if args.package:
        run_conveyor(args.conveyor_passphrase)


def is_windows_admin() -> bool:
    if host_platform() != "windows":
        return True
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def prepare_config_arg(config: str) -> str:
    if config.startswith("http://") or config.startswith("https://"):
        return config
    path = Path(config)
    if path.exists():
        return str(path)
    config_path = ROOT_DIR / "cli-test-config.toml"
    config_path.write_text(config, encoding="utf-8")
    return str(config_path)


def wait_for_port(port: int, timeout_seconds: int = 30) -> bool:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return True
        except OSError:
            time.sleep(1)
    return False


def sudo_prefix() -> list[str]:
    if host_platform() == "windows":
        return []
    if hasattr(os, "geteuid") and os.geteuid() == 0:
        return []
    return ["sudo"]


def start_service(target_platform: str, port: int) -> tuple[subprocess.Popen[str], list[object]]:
    service = service_target_path(target_platform)
    if not service.exists():
        fail(f"Missing service binary: {service}")

    handles: list[object] = []
    if target_platform == "windows":
        stdout = open(ROOT_DIR / "grpcvpnserver.out", "w", encoding="utf-8")
        stderr = open(ROOT_DIR / "grpcvpnserver.err", "w", encoding="utf-8")
        command = [str(service), "-port", str(port)]
    else:
        stdout = open(ROOT_DIR / "grpcvpnserver.log", "w", encoding="utf-8")
        stderr = subprocess.STDOUT
        command = [*sudo_prefix(), str(service), "-port", str(port)]
    handles.append(stdout)
    if hasattr(stderr, "close"):
        handles.append(stderr)

    log(f"Starting gRPC VPN service on port {port}")
    process = subprocess.Popen(command, cwd=str(ROOT_DIR), stdout=stdout, stderr=stderr, text=True)
    if wait_for_port(port):
        log("gRPC VPN service is ready")
        return process, handles

    stop_service(process)
    print_service_logs()
    fail(f"gRPC VPN service did not start on port {port}")


def stop_service(process: subprocess.Popen[str]) -> None:
    if process.poll() is not None:
        return
    log("Stopping gRPC VPN service")
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()


def print_service_logs() -> None:
    for name in ("grpcvpnserver.log", "grpcvpnserver.out", "grpcvpnserver.err"):
        path = ROOT_DIR / name
        if path.exists():
            print(path.read_text(encoding="utf-8", errors="replace"))


def run_cli_check(config_arg: str, port: int) -> None:
    props = desktop_version_properties()
    env = os.environ.copy()
    env["PORT"] = str(port)
    run(
        [gradle_command(), "--quiet", ":app:run", f"--args=check-config {config_arg}", *props],
        cwd=KMP_DIR,
        env=env,
    )


def cli_test(args: argparse.Namespace) -> None:
    config = args.config or os.environ.get("DOBBYVPN_CLI_TEST_CONFIG")
    if not config:
        fail("Pass --config <url-or-file> or set DOBBYVPN_CLI_TEST_CONFIG")
    if not is_windows_admin():
        fail("Run this command from an elevated shell so the VPN service can configure Wintun")

    target_platform = host_platform()
    ensure_build_dependencies(target_platform, args.skip_deps, need_android=True)
    if target_platform == "windows":
        install_wintun(args.skip_deps)

    if not args.skip_build:
        build_service(
            target_platform,
            go_arch_from_machine(),
            args.skip_deps,
            skip_build=False,
            run_go_mod_tidy=args.go_mod_tidy,
        )
        run_desktop_gradle(args.skip_deps)
    else:
        require_services(False, "current")

    config_arg = prepare_config_arg(config)
    process: subprocess.Popen[str] | None = None
    handles: list[object] = []
    try:
        process, handles = start_service(target_platform, args.port)
        run_cli_check(config_arg, args.port)
    finally:
        if process:
            stop_service(process)
        for handle in handles:
            close = getattr(handle, "close", None)
            if close:
                close()
        print_service_logs()


def add_common_options(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--skip-deps", action="store_true", help="Do not install missing local dependencies.")
    parser.add_argument("--skip-build", action="store_true", help="Reuse existing build outputs when possible.")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build DobbyVPN desktop services/app in the same shape used by CI."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    libs = subparsers.add_parser("libs", help="Build desktop gRPC VPN service binaries.")
    add_common_options(libs)
    libs.add_argument("--platform", default="current", help="current, linux, macos, windows, ubuntu, or all.")
    libs.add_argument("--arch", help="Override GOARCH for the service build.")
    libs.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before go mod download.")

    app = subparsers.add_parser("app", help="Build the desktop JVM app and Conveyor config.")
    add_common_options(app)
    app.add_argument(
        "--platform",
        default="current",
        help="Service platform to build/copy when --skip-libs is not set.",
    )
    app.add_argument("--arch", help="Override GOARCH for service builds.")
    app.add_argument("--skip-libs", action="store_true", help="Use existing kmp_module/services binaries.")
    app.add_argument("--require-all-services", action="store_true", help="Require Linux, macOS, and Windows services.")
    app.add_argument("--package", action="store_true", help="Run local Conveyor packaging after the Gradle build.")
    app.add_argument("--conveyor-passphrase", default=os.environ.get("CONVEYOR_PASSPHRASE"))
    app.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before service builds.")

    cli = subparsers.add_parser("cli-test", help="Build current desktop target and run check-config.")
    add_common_options(cli)
    cli.add_argument("--config", help="Config URL, TOML file path, or inline TOML.")
    cli.add_argument("--port", type=int, default=int(os.environ.get("PORT", "50151")))
    cli.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before the service build.")

    return parser.parse_args()


def main() -> None:
    if not GO_MODULE_DIR.is_dir() or not KMP_DIR.is_dir():
        fail("Run this script from a cloned DobbyVPN repository")
    bootstrap_local_tools()
    args = parse_args()

    if args.command == "libs":
        for target_platform in selected_platforms(args.platform):
            build_service(
                target_platform,
                args.arch,
                args.skip_deps,
                args.skip_build,
                args.go_mod_tidy,
            )
    elif args.command == "app":
        build_app(args)
    elif args.command == "cli-test":
        cli_test(args)
    else:
        fail(f"Unknown command: {args.command}")

    log("Done")


if __name__ == "__main__":
    main()
