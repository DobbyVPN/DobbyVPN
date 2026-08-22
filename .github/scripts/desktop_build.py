#!/usr/bin/env python3
from __future__ import annotations

import argparse
import contextlib
import ctypes
from dataclasses import dataclass
import hashlib
import os
import platform
import re
import shutil
import socket
import stat
import subprocess
import sys
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

ANDROID_NDK_VERSION = "27.2.12479018"
ANDROID_PACKAGES = (
    "platforms;android-35",
    "platforms;android-36",
    "build-tools;36.0.0",
    "platform-tools",
    f"ndk;{ANDROID_NDK_VERSION}",
)
ANDROID_TOOLS_VERSION = "11076708"
WINTUN_VERSION = "0.14.1"
WINTUN_AMD64_DLL_SHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
LLVM_LIBCXX_VERSION = "21.1.8"
LLVM_LIBCXX_PACKAGES = (
    (
        "libc++1_21.1.8~++20251221032922+2078da43e25a-1~exp1~20251221153059.70_amd64.deb",
        "d9566cd347beb65bf2e0d504fbffc6ca38c29ac2752292cba515891c84ff0bbd",
    ),
    (
        "libc++abi1_21.1.8~++20251221032922+2078da43e25a-1~exp1~20251221153059.70_amd64.deb",
        "829f02714a9daafac0cc37c81c7d02ae5cb5b63707b524b93e36dcd7e7e5549c",
    ),
)
TRUSTTUNNEL_MACOS_VERSION = "1.0.49"
TRUSTTUNNEL_MACOS_ARCHIVE_SHA256 = "f2dab732d17a885dcc4c81831fa4b263db250f5bea8a151416b518e936979c64"


@dataclass(frozen=True)
class BridgeRelease:
    version: str
    asset_name: str
    archive_sha256: str
    member_name: str
    member_sha256: str


BRIDGE_RELEASES = {
    "windows": BridgeRelease(
        version="1.0.1",
        asset_name="dobby_bridge-windows-x86_64.zip",
        archive_sha256="a7e64db0568547d395bc45e33787f22c7303dca6f5c575c84439e73a70124331",
        member_name="dobby_bridge.dll",
        member_sha256="10e2f921aaa949060bed936e3c361b0967b2ad8b7a71dd983d36abd94c903063",
    ),
    "linux": BridgeRelease(
        version="1.0.1",
        asset_name="libdobby_bridge-linux-x86_64.zip",
        archive_sha256="67536090d74212a5635739d297f5a78fbabda1966d161b12a16bfe487a8c68b9",
        member_name="libdobby_bridge.so",
        member_sha256="2fff96d2631df43168196e222bc2205157d23a2449475364c5b135fbf0aaa0ce",
    ),
}

SERVICE_NAMES = {
    "linux": "ubuntu_grpcvpnserver",
    "macos": "macos_grpcvpnserver",
    "windows": "windows_grpcvpnserver.exe",
}
CLI_NAMES = {
    "linux": "dobby-cli",
    "macos": "dobby-cli",
    "windows": "dobby-cli.exe",
}
MACOS_MINIMUM_SYSTEM_VERSION = "11.0"
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


def sha256_file(path: Path) -> str:
    with path.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


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
                "--http1.1",
                "--retry",
                "5",
                "--retry-delay",
                "2",
                "--connect-timeout",
                "60",
                "--max-time",
                "900",
                "--retry-max-time",
                "1200",
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
        and (sdk_root / "ndk" / ANDROID_NDK_VERSION).is_dir()
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
        mingw_bin = Path("C:/ProgramData/chocolatey/lib/mingw/tools/install/mingw64/bin")
        # Common standalone WinLibs installations use C:/mingw64. Prefer that
        # no-space path over WinGet's Program Files shim when it is available.
        prepend_path(mingw_bin)
        prepend_path(Path("C:/mingw64/bin"))
        usable, diagnostic = probe_windows_gcc()
        if usable:
            return
        if skip_deps:
            fail(
                "A working x86_64 MinGW gcc is required for the Windows gRPC VPN service "
                f"({diagnostic})"
            )
        if not command_exists("choco"):
            fail(f"Install or repair MinGW gcc, or install Chocolatey ({diagnostic})")
        run(["choco", "install", "mingw", "-y"])
        prepend_path(mingw_bin)
        usable, diagnostic = probe_windows_gcc()
        if not usable:
            fail(f"MinGW gcc remained unusable after installation ({diagnostic})")


def probe_windows_gcc() -> tuple[bool, str]:
    candidate = shutil.which("gcc")
    if candidate is None:
        return False, "compiler=missing"
    try:
        executable = Path(candidate).resolve(strict=True)
    except OSError:
        return False, "compiler=unresolvable"
    if any(character.isspace() for character in str(executable.parent)):
        # Go/cgo passes GCC's derived linker paths through its external-link
        # command. MinGW distributions rooted below "Program Files" split
        # those paths at the space and fail during the final link.
        return False, "compiler=unsupported_path_contains_whitespace"
    # WinGet can expose a gcc symlink without placing the real executable's
    # adjacent runtime DLLs on PATH. Prefer the resolved bin directory both for
    # this probe and for the later Go/cgo process.
    prepend_path(executable.parent)
    try:
        result = subprocess.run(
            [str(executable), "-dumpmachine"],
            cwd=str(ROOT_DIR),
            text=True,
            stdout=subprocess.PIPE,
            stderr=None,
        )
    except OSError as error:
        return False, f"compiler=launch_failed errno={error.errno or 0}"
    target = result.stdout.strip()
    if result.returncode == 0 and target == "x86_64-w64-mingw32":
        return True, f"compiler=ready target={target}"
    safe_target = target if re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", target) else "invalid"
    return False, f"compiler=unusable exit_code={result.returncode} target={safe_target}"


def install_wintun(skip_deps: bool) -> None:
    if host_platform() != "windows":
        return
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    arch = go_arch_from_machine()
    artifact = GO_MODULE_DIR / "wintun.dll"
    staged = SERVICES_DIR / "wintun.dll"
    source = artifact if artifact.is_file() else staged if staged.is_file() else None
    if source is None:
        if skip_deps:
            fail("wintun.dll is required for Windows CLI checks")
        source = artifact
        archive = TOOLS_DIR / "downloads" / f"wintun-{WINTUN_VERSION}.zip"
        member_name = f"wintun/bin/{arch}/wintun.dll"
        temporary = artifact.with_suffix(".dll.tmp")
        download(f"https://www.wintun.net/builds/wintun-{WINTUN_VERSION}.zip", archive)
        try:
            with zipfile.ZipFile(archive) as zip_file:
                member = zip_file.getinfo(member_name)
                if member.is_dir() or Path(member.filename).name != "wintun.dll":
                    fail("Wintun archive member is invalid")
                with zip_file.open(member) as input_file, open(temporary, "wb") as output_file:
                    shutil.copyfileobj(input_file, output_file)
            temporary.replace(artifact)
        finally:
            temporary.unlink(missing_ok=True)

    if arch == "amd64" and sha256_file(source) != WINTUN_AMD64_DLL_SHA256:
        fail("Wintun amd64 DLL checksum mismatch")
    if source != artifact:
        shutil.copyfile(source, artifact)
    if source != staged:
        shutil.copyfile(source, staged)
    log(f"Staged checksum-pinned Wintun DLL: {staged.name}")


def install_windows_bridge(skip_deps: bool) -> None:
    """Install and stage the checksum-pinned Windows native bridge."""
    if host_platform() != "windows":
        return
    bridge_dir = GO_MODULE_DIR / "lib" / "windows"
    bridge_dir.mkdir(parents=True, exist_ok=True)
    release = BRIDGE_RELEASES["windows"]
    bridge = bridge_dir / release.member_name
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        if skip_deps:
            fail("dobby_bridge.dll is required for the Windows gRPC VPN service")
        archive = TOOLS_DIR / "downloads" / f"{Path(release.asset_name).stem}-v{release.version}.zip"
        download(
            "https://github.com/DobbyVPN/go-go-tunnel/releases/download/"
            f"v{release.version}/{release.asset_name}",
            archive,
        )
        if sha256_file(archive) != release.archive_sha256:
            fail("Windows native bridge archive checksum mismatch")
        with zipfile.ZipFile(archive) as zip_file:
            for member in zip_file.infolist():
                member_path = Path(member.filename)
                if member.is_dir() or member_path.is_absolute() or ".." in member_path.parts:
                    continue
                if member_path.name not in {
                    "dobby_bridge.dll",
                    "dobby_bridge.lib",
                    "dobby_bridge.a",
                    "libdobby_bridge.a",
                }:
                    continue
                with zip_file.open(member) as source, open(bridge_dir / member_path.name, "wb") as target:
                    shutil.copyfileobj(source, target)
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        fail("Windows native bridge archive did not contain the expected dobby_bridge.dll")
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    for directory in (GO_MODULE_DIR, SERVICES_DIR):
        shutil.copyfile(bridge, directory / bridge.name)
    log(f"Staged checksum-pinned Windows native bridge: {bridge.name}")


def append_cgo_ldflags(env: dict[str, str], *flags: str) -> None:
    """Append required native linker flags without discarding caller flags."""
    existing = env.get("CGO_LDFLAGS", "").strip()
    env["CGO_LDFLAGS"] = " ".join(part for part in (existing, *flags) if part)


def install_linux_trusttunnel_bridge(skip_deps: bool) -> None:
    """Stage the checksum-pinned Linux bridge for linking and packaging."""
    if host_platform() != "linux":
        return

    release = BRIDGE_RELEASES["linux"]
    bridge = GO_MODULE_DIR / release.member_name
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        if skip_deps:
            fail("libdobby_bridge.so is required for the Linux gRPC VPN service")

        archive = (
            TOOLS_DIR
            / "downloads"
            / f"{Path(release.asset_name).stem}-v{release.version}.zip"
        )
        if not archive.exists():
            download(
                "https://github.com/DobbyVPN/go-go-tunnel/releases/download/"
                f"v{release.version}/{release.asset_name}",
                archive,
            )
        if sha256_file(archive) != release.archive_sha256:
            fail("TrustTunnel Linux bridge archive checksum mismatch")

        with zipfile.ZipFile(archive) as zip_file:
            candidates = [
                member
                for member in zip_file.infolist()
                if not member.is_dir() and Path(member.filename).name == bridge.name
            ]
            if len(candidates) != 1:
                fail("TrustTunnel Linux bridge archive did not contain exactly one shared library")
            member = candidates[0]
            source = zip_file.open(member)
            temporary = bridge.with_suffix(".so.tmp")
            try:
                with source, open(temporary, "wb") as handle:
                    shutil.copyfileobj(source, handle)
                temporary.chmod(0o755)
                temporary.replace(bridge)
            finally:
                temporary.unlink(missing_ok=True)

    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        fail("TrustTunnel Linux bridge archive did not contain the expected shared library")

    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    staged = SERVICES_DIR / bridge.name
    shutil.copyfile(bridge, staged)
    staged.chmod(0o755)
    log(f"Staged checksum-pinned TrustTunnel Linux bridge: {staged}")


def find_linux_libcxx_runtime() -> Path | None:
    """Find a workspace-local LLVM runtime suitable for the Linux bridge."""

    candidates = []
    configured = os.environ.get("DOBBYVPN_LIBCXX_RUNTIME")
    if configured:
        candidates.append(Path(configured))
    candidates.append(TOOLS_DIR / f"llvm-libcxx-{LLVM_LIBCXX_VERSION}")
    for runtime in candidates:
        if (runtime / "libc++.so").is_file() and (
            runtime / "libc++abi.so"
        ).is_file():
            return runtime
    return None


def install_linux_libcxx_runtime(skip_deps: bool) -> Path:
    """Stage pinned local C++ runtimes for linking and execution."""

    runtime = find_linux_libcxx_runtime()
    if runtime is None:
        if skip_deps:
            fail(
                "LLVM libc++ and libc++abi are required for the Linux "
                "TrustTunnel bridge"
            )
        if not command_exists("dpkg-deb"):
            fail("dpkg-deb is required to extract workspace-local LLVM runtimes")
        runtime = TOOLS_DIR / f"llvm-libcxx-{LLVM_LIBCXX_VERSION}"
        extract_dir = Path(tempfile.mkdtemp(prefix="dobby-libcxx-"))
        try:
            for filename, expected_digest in LLVM_LIBCXX_PACKAGES:
                archive = TOOLS_DIR / "downloads" / filename
                if not archive.exists():
                    download(
                        "https://apt.llvm.org/noble/pool/main/l/"
                        f"llvm-toolchain-21/{filename}",
                        archive,
                    )
                if hashlib.sha256(archive.read_bytes()).hexdigest() != expected_digest:
                    fail(f"LLVM runtime archive checksum mismatch: {filename}")
                run(["dpkg-deb", "-x", str(archive), str(extract_dir)])

            runtime.mkdir(parents=True, exist_ok=True)
            for library in ("libc++", "libc++abi"):
                matches = list(extract_dir.rglob(f"{library}.so.1.0"))
                if len(matches) != 1:
                    fail(f"LLVM runtime archive did not contain exactly one {library}")
                for name in (f"{library}.so", f"{library}.so.1"):
                    shutil.copyfile(matches[0], runtime / name)
                    (runtime / name).chmod(0o755)
        finally:
            shutil.rmtree(extract_dir, ignore_errors=True)
        runtime = find_linux_libcxx_runtime()
    if runtime is None:
        fail("Workspace-local LLVM runtime bootstrap did not produce required files")

    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    for name in ("libc++.so.1", "libc++abi.so.1"):
        source = runtime / name
        if not source.exists():
            fail(f"Workspace-local LLVM runtime is missing {name}")
        for directory in (GO_MODULE_DIR, SERVICES_DIR):
            target = directory / name
            shutil.copyfile(source.resolve(), target)
            target.chmod(0o755)
    log("Staged pinned workspace-local libc++ and libc++abi runtimes")
    return runtime


def ensure_build_dependencies(target_platform: str, skip_deps: bool, need_android: bool) -> None:
    install_linux_packages(skip_deps)
    install_go(skip_deps)
    ensure_compiler(target_platform, skip_deps)
    if need_android:
        install_jdk(skip_deps)
        install_android_sdk(skip_deps)


def go_mod_download(run_tidy: bool) -> None:
    if run_tidy:
        run(["go", "mod", "tidy"], cwd=GO_MODULE_DIR)
    run(["go", "mod", "download"], cwd=GO_MODULE_DIR)


def prepare_go_test_dependencies(skip_deps: bool, run_go_mod_tidy: bool) -> None:
    """Materialize the exact native closure required by Linux Go tests.

    The pinned go-go-tunnel module embeds its Linux cgo search path in the
    module cache. The public bridge and libc++ runtimes are therefore staged
    in the checkout and added through CGO_LDFLAGS/LD_LIBRARY_PATH before the
    test process starts. This keeps hosted CI on the same dependency contract
    as the desktop service build without compiling a service as a side effect.
    """
    if host_platform() != "linux":
        fail("prepare-go-test-deps is supported only on Linux CI runners")

    ensure_build_dependencies("linux", skip_deps, need_android=False)
    install_linux_trusttunnel_bridge(skip_deps)
    runtime = install_linux_libcxx_runtime(skip_deps)
    go_mod_download(run_go_mod_tidy)

    environment = os.environ.copy()
    append_cgo_ldflags(
        environment,
        f"-L{GO_MODULE_DIR}",
        f"-L{runtime}",
        "-Wl,--no-as-needed",
    )
    set_env("CGO_ENABLED", "1")
    set_env("CGO_LDFLAGS", environment["CGO_LDFLAGS"])
    existing_library_path = os.environ.get("LD_LIBRARY_PATH", "").strip()
    library_path = os.pathsep.join(
        part for part in (str(GO_MODULE_DIR), str(runtime), existing_library_path) if part
    )
    set_env("LD_LIBRARY_PATH", library_path)
    log(f"Prepared Linux Go-test native dependencies with CGO_LDFLAGS={environment['CGO_LDFLAGS']}")
    log(f"Prepared Linux Go-test runtime path: {library_path}")


def service_output_path(target_platform: str) -> Path:
    return GO_MODULE_DIR / SERVICE_NAMES[target_platform]


def service_target_path(target_platform: str) -> Path:
    return SERVICES_DIR / SERVICE_NAMES[target_platform]


def build_cli(target_platform: str, arch: str | None = None) -> Path:
    """Build the native operator CLI without invoking the JVM launcher."""
    target_arch = arch or default_service_arch(target_platform)
    output = GO_MODULE_DIR / CLI_NAMES[target_platform]
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": GOOS_BY_PLATFORM[target_platform], "GOARCH": target_arch})
    ldflags = "-buildid="
    if target_platform == "macos":
        # Go's internal Darwin linker emits a macOS 12 load command even when
        # the app declares macOS 11 support.  Use the host external linker so
        # the native CLI remains honest about the product's minimum version.
        env["CGO_ENABLED"] = "1"
        ldflags += f" -linkmode=external -extldflags=-mmacosx-version-min={MACOS_MINIMUM_SYSTEM_VERSION}"
    run(
        ["go", "build", "-trimpath", f"-ldflags={ldflags}", "-o", output.name, "./cmd/dobbyvpn/"],
        cwd=GO_MODULE_DIR,
        env=env,
    )
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    target = SERVICES_DIR / CLI_NAMES[target_platform]
    shutil.copyfile(output, target)
    if target_platform != "windows":
        target.chmod(target.stat().st_mode | 0o111)
    log(f"Built native Go CLI {target}")
    return target


def install_macos_amd64_trusttunnel_helper(skip_deps: bool) -> None:
    """Stage the pinned official helper beside the Intel macOS service.

    The in-process bridge remains the arm64 implementation. Intel macOS uses
    this separate universal executable so it never links the arm64-only
    go-go-tunnel archive.
    """
    if skip_deps:
        helper = GO_MODULE_DIR / "trusttunnel_client"
        if not helper.exists():
            fail("official TrustTunnelClient helper is required for macOS amd64")
    archive = TOOLS_DIR / "downloads" / f"trusttunnel_client-v{TRUSTTUNNEL_MACOS_VERSION}-macos-universal.tar.gz"
    if not archive.exists() and not skip_deps:
        download(
            "https://github.com/TrustTunnel/TrustTunnelClient/releases/download/"
            f"v{TRUSTTUNNEL_MACOS_VERSION}/trusttunnel_client-v{TRUSTTUNNEL_MACOS_VERSION}-macos-universal.tar.gz",
            archive,
        )
    if archive.exists():
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        if digest != TRUSTTUNNEL_MACOS_ARCHIVE_SHA256:
            fail("official TrustTunnelClient archive checksum mismatch")
        with tarfile.open(archive, "r:gz") as tar_file:
            member_name = f"trusttunnel_client-v{TRUSTTUNNEL_MACOS_VERSION}-macos-universal/trusttunnel_client"
            try:
                member = tar_file.getmember(member_name)
            except KeyError:
                fail("official TrustTunnelClient archive did not contain the helper")
            if not member.isfile():
                fail("official TrustTunnelClient helper archive member is invalid")
            source = tar_file.extractfile(member)
            if source is None:
                fail("official TrustTunnelClient helper could not be extracted")
            target = GO_MODULE_DIR / "trusttunnel_client"
            with source, open(target, "wb") as handle:
                shutil.copyfileobj(source, handle)
            target.chmod(0o755)
    helper = GO_MODULE_DIR / "trusttunnel_client"
    if not helper.exists():
        fail("official TrustTunnelClient helper is unavailable")
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    staged = SERVICES_DIR / "trusttunnel_client"
    shutil.copyfile(helper, staged)
    staged.chmod(0o755)
    log(f"Staged pinned TrustTunnelClient helper beside Intel macOS service: {staged}")


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
    if target_platform == "windows":
        # The service imports the bridge and loads Wintun at process startup.
        # Stage exactly that runtime closure for the public artifact and local CLI.
        install_wintun(skip_deps)
        install_windows_bridge(skip_deps)
    if target_platform == "linux":
        if target_arch != "amd64":
            fail("The pinned TrustTunnel Linux bridge currently supports amd64 only")
        install_linux_trusttunnel_bridge(skip_deps)
        linux_libcxx_runtime = install_linux_libcxx_runtime(skip_deps)
    else:
        linux_libcxx_runtime = None
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
        ldflags = "-buildid="
        if target_platform == "macos":
            # Keep the Intel package's declared macOS 11 floor valid for both
            # the gRPC service and the native operator CLI.
            ldflags += f" -linkmode=external -extldflags=-mmacosx-version-min={MACOS_MINIMUM_SYSTEM_VERSION}"
        if target_platform == "linux":
            bridge_search_path = f"-L{GO_MODULE_DIR}"
            runtime_search_path = f"-L{linux_libcxx_runtime}"
            # DT_RPATH is intentional here: the pinned bridge has indirect
            # libc++ dependencies, and DT_RUNPATH is not transitive.
            origin_runpath = "-Wl,--disable-new-dtags,-rpath,$ORIGIN"
            retain_runtime_dependencies = "-Wl,--no-as-needed"
            append_cgo_ldflags(
                env,
                bridge_search_path,
                runtime_search_path,
                origin_runpath,
                retain_runtime_dependencies,
            )
        elif target_platform == "macos":
            # go-go-tunnel's static bridge contains C++ and uses the macOS
            # SystemConfiguration APIs. cgo does not infer either dependency
            # from a static archive.
            append_cgo_ldflags(env, "-lc++", "-framework", "SystemConfiguration")
        run(
            [
                "go",
                "build",
                "-trimpath",
                f"-ldflags={ldflags}",
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
    if target_platform == "macos" and target_arch == "amd64":
        install_macos_amd64_trusttunnel_helper(skip_deps)
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
    else:
        version_name = os.environ.get("VERSION_NAME") or gradle_properties.get("versionName", "0.0.1")

    # Prefer CI run number (same as Android/iOS APPLE_BUILD_NUMBER) so desktop
    # stays unique when marketing VERSION is held constant.
    version_code = (
        os.environ.get("APPLE_BUILD_NUMBER")
        or os.environ.get("VERSION_CODE")
        or os.environ.get("ANDROID_VERSION_CODE")
    )
    if version_code is None and major is not None and minor is not None and maintenance is not None:
        version_code = str(int(major) * 1_000_000 + int(minor) * 1_000 + int(maintenance))
    if version_code is None:
        version_code = gradle_properties.get("versionCode", "1")

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


def emit_conveyor_config() -> None:
    """Print only Conveyor's generated HOCON on every supported host."""
    with contextlib.redirect_stdout(sys.stderr):
        install_jdk(skip_deps=False)
    command = [gradle_command(), "--no-daemon", "-q", "printConveyorConfig", *desktop_version_properties()]
    try:
        result = subprocess.run(
            command,
            cwd=str(KMP_DIR),
            env=os.environ.copy(),
            stdout=subprocess.PIPE,
            stderr=sys.stderr,
            text=True,
            check=False,
        )
    except FileNotFoundError as error:
        fail(f"Command was not found: {error.filename}")
    if result.returncode != 0:
        fail(f"Conveyor config generation failed with exit code {result.returncode}")
    sys.stdout.write(result.stdout)


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
    command = [conveyor, "-f", str(KMP_DIR / "conveyor.conf")]
    if passphrase:
        command.extend((f"--passphrase={passphrase}", "make", "site"))
    else:
        command.extend(("make", "site"))
    run(command)


def build_app(args: argparse.Namespace) -> None:
    platforms = selected_platforms(args.platform)
    if not args.skip_libs:
        for target_platform in platforms:
            build_service(
                target_platform,
                args.arch,
                args.skip_deps,
                args.skip_build,
                args.go_mod_tidy,
            )

    # The native operator CLI is a packaging input, not a JVM application
    # output.  A source/package build may deliberately reuse already-built
    # service binaries via --skip-libs, but it must still materialize the CLI
    # for the selected target before Conveyor resolves conveyor.conf.
    for target_platform in platforms:
        cli_target = service_target_path(target_platform).parent / CLI_NAMES[target_platform]
        if not cli_target.exists():
            build_cli(target_platform, args.arch)

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


def wait_for_socket(path: Path, timeout_seconds: int = 30) -> bool:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        try:
            if stat.S_ISSOCK(path.stat().st_mode):
                return True
        except FileNotFoundError:
            pass
        time.sleep(1)
    return False


def sudo_prefix() -> list[str]:
    if host_platform() == "windows":
        return []
    if hasattr(os, "geteuid") and os.geteuid() == 0:
        return []
    return ["sudo"]


def start_service(
    target_platform: str,
    port: int,
    control_socket: Path | None = None,
) -> tuple[subprocess.Popen[str], list[object]]:
    service = service_target_path(target_platform)
    if not service.exists():
        fail(f"Missing service binary: {service}")

    handles: list[object] = []
    if target_platform == "windows":
        stdout = open(ROOT_DIR / "grpcvpnserver.out", "w", encoding="utf-8")
        stderr = open(ROOT_DIR / "grpcvpnserver.err", "w", encoding="utf-8")
        command = [str(service), "-port", str(port)]
        environment = os.environ.copy()
    else:
        stdout = open(ROOT_DIR / "grpcvpnserver.log", "w", encoding="utf-8")
        stderr = subprocess.STDOUT
        if control_socket is None:
            fail("A private control socket path is required for Unix CLI tests")
        environment = os.environ.copy()
        environment["DOBBYVPN_CONTROL_SOCKET"] = str(control_socket)
        prefix = sudo_prefix()
        command = [*prefix]
        if prefix:
            command.extend(["env", f"DOBBYVPN_CONTROL_SOCKET={control_socket}"])
        command.extend([str(service), "-port", str(port)])
    handles.append(stdout)
    if hasattr(stderr, "close"):
        handles.append(stderr)

    log("Starting VPN control service")
    process = subprocess.Popen(
        command,
        cwd=str(ROOT_DIR),
        env=environment,
        stdout=stdout,
        stderr=stderr,
        text=True,
    )
    ready = wait_for_port(port) if target_platform == "windows" else wait_for_socket(control_socket)
    if ready:
        log("gRPC VPN service is ready")
        return process, handles

    stop_service(process)
    print_service_logs()
    fail("gRPC VPN service did not become ready")


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


def remove_control_socket_parent(control_socket: Path | None) -> None:
    if control_socket is None:
        return
    try:
        socket_mode = control_socket.lstat().st_mode
    except FileNotFoundError:
        socket_mode = None
    if socket_mode is not None:
        if not stat.S_ISSOCK(socket_mode):
            log("Refusing to remove a non-socket control path")
            return
        run([*sudo_prefix(), "unlink", str(control_socket)], check=False)
    run([*sudo_prefix(), "rmdir", str(control_socket.parent)], check=False)


def run_cli_check(config_arg: str, port: int, control_socket: Path | None = None) -> None:
    env = os.environ.copy()
    env["PORT"] = str(port)
    if control_socket is not None:
        env["DOBBYVPN_CONTROL_SOCKET"] = str(control_socket)
    target = SERVICES_DIR / CLI_NAMES[host_platform()]
    run([str(target), "check-config", config_arg], cwd=KMP_DIR, env=env)


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
        build_cli(target_platform, go_arch_from_machine())
        run_desktop_gradle(args.skip_deps)
    else:
        require_services(False, "current")
        if not (SERVICES_DIR / CLI_NAMES[target_platform]).exists():
            build_cli(target_platform, go_arch_from_machine())

    config_arg = prepare_config_arg(config)
    process: subprocess.Popen[str] | None = None
    handles: list[object] = []
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-cli-control-") as control_root:
        control_socket = (
            None
            if target_platform == "windows"
            else Path(control_root) / "service" / "control.sock"
        )
        try:
            process, handles = start_service(target_platform, args.port, control_socket)
            run_cli_check(config_arg, args.port, control_socket)
        finally:
            if process:
                stop_service(process)
            remove_control_socket_parent(control_socket)
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

    go_test_deps = subparsers.add_parser(
        "prepare-go-test-deps",
        help="Stage pinned Linux native dependencies and environment for Go tests.",
    )
    add_common_options(go_test_deps)
    go_test_deps.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before go mod download.")

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

    subparsers.add_parser(
        "conveyor-config",
        help="Emit generated Conveyor HOCON without platform-specific wrapper assumptions.",
    )

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
    elif args.command == "prepare-go-test-deps":
        prepare_go_test_dependencies(args.skip_deps, args.go_mod_tidy)
    elif args.command == "app":
        build_app(args)
    elif args.command == "cli-test":
        cli_test(args)
    elif args.command == "conveyor-config":
        emit_conveyor_config()
        return
    else:
        fail(f"Unknown command: {args.command}")

    log("Done")


if __name__ == "__main__":
    main()
