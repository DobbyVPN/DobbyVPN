#!/usr/bin/env python3
from __future__ import annotations

import argparse
import contextlib
import ctypes
from dataclasses import dataclass
import hashlib
import os
import platform
import plistlib
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

from bounded_process import (
    PROCESS_CLEANUP_GRACE_SECONDS,
    exception_output as _exception_output,
    output_text,
    process_group_options,
    run_bounded_capture as _run_bounded_capture,
    terminate_process_group,
)


ROOT_DIR = Path(__file__).resolve().parents[2]
GO_MODULE_DIR = ROOT_DIR / "go_module"
SERVICES_DIR = ROOT_DIR / "runtime" / "services"
LOCAL_BUILD_CACHE = os.environ.get("DOBBYVPN_LOCAL_BUILD_CACHE")
TOOLS_DIR = (
    Path(LOCAL_BUILD_CACHE) / "desktop-tools"
    if LOCAL_BUILD_CACHE
    else ROOT_DIR / ".local-tools" / "desktop-build"
)

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


@contextlib.contextmanager
def temporary_directory(prefix: str):
    path = Path(tempfile.mkdtemp(prefix=prefix))
    try:
        yield path
    except BaseException as primary:
        try:
            shutil.rmtree(path)
        except BaseException as cleanup_error:
            primary.add_note(
                f"temporary-directory cleanup failure: {type(cleanup_error).__name__}: {cleanup_error}"
            )
        raise
    else:
        shutil.rmtree(path)

SERVICE_NAMES = {
    "linux": "dobbyvpn-backend",
    "macos": "dobbyvpn-backend",
    "windows": "dobbyvpn-backend.exe",
}
CLI_NAMES = {
    "linux": "dobby-cli",
    "macos": "dobby-cli",
    "windows": "dobby-cli.exe",
}
MACOS_MINIMUM_SYSTEM_VERSION = "12.0"
PROBE_TIMEOUT_SECONDS = 30
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


def emit_process_diagnostic(prefix: str, output: str | bytes | None = None) -> None:
    """Emit a failed child-process diagnostic without discarding its output."""
    print(prefix, file=sys.stderr, flush=True)
    text = output_text(output)
    if text:
        sys.stderr.write(text)
        if not text.endswith("\n"):
            sys.stderr.write("\n")
        sys.stderr.flush()


def emit_process_streams(label: str, stdout: str | bytes | None, stderr: str | bytes | None) -> None:
    """Forward both complete child streams before a caller parses either one."""
    for stream_name, payload in (("stdout", stdout), ("stderr", stderr)):
        print(f"[{label} {stream_name} begin]", file=sys.stderr)
        text = output_text(payload)
        if text:
            sys.stderr.write(text)
            if not text.endswith("\n"):
                sys.stderr.write("\n")
        print(f"[{label} {stream_name} end]", file=sys.stderr)
    sys.stderr.flush()


def child_environment(
    command: list[str], env: dict[str, str] | None = None,
) -> dict[str, str]:
    child_env = os.environ.copy() if env is None else env.copy()
    executable = command[0].replace("\\", "/").rsplit("/", 1)[-1].lower() if command else ""
    if executable in {"go", "go.exe"}:
        child_env["GOTOOLCHAIN"] = "local"
    if host_platform() == "windows" and executable in {"go", "go.exe"}:
        # Some Windows Go compilers have stalled in asyncPreempt/badmcall and
        # recursive panic reporting, even during compile -V=full. Upstream:
        # golang/go#67108 and #79249. Scope this mitigation to Go tools, not
        # the VPN runtime, until a Windows qualification proves it unnecessary.
        child_env["GODEBUG"] = ",".join(
            filter(None, (child_env.get("GODEBUG"), "asyncpreemptoff=1"))
        )
    return child_env


def run_bounded_capture(
    command: list[str],
    cwd: Path = ROOT_DIR,
    timeout_seconds: int = PROBE_TIMEOUT_SECONDS,
) -> subprocess.CompletedProcess[str]:
    return _run_bounded_capture(
        command,
        cwd=cwd,
        env=child_environment(command),
        timeout_seconds=timeout_seconds,
        cleanup_grace_seconds=PROCESS_CLEANUP_GRACE_SECONDS,
    )


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
    child_env = child_environment(command, env)
    try:
        result = subprocess.run(
            command,
            cwd=str(cwd),
            env=child_env,
            input=input_text,
            text=True,
        )
    except FileNotFoundError as error:
        fail(f"Command was not found: {error.filename}")
    if check and result.returncode != 0:
        fail(f"Command failed with exit code {result.returncode}: {printable}")
    return result


def run_capture(command: list[str], cwd: Path = ROOT_DIR) -> str | None:
    printable = " ".join(command)
    try:
        result = run_bounded_capture(command, cwd)
    except FileNotFoundError as error:
        emit_process_diagnostic(f"[!] Probe command was not found: {printable}: {error}")
        return None
    except subprocess.TimeoutExpired as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(
            f"[!] Probe timed out after {error.timeout}s: {printable}",
            stdout,
        )
        emit_process_diagnostic("[!] Probe stderr:", stderr)
        return None
    except OSError as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(
            f"[!] Probe communicate failed: {printable}: {error}",
            stdout,
        )
        emit_process_diagnostic("[!] Probe stderr:", stderr)
        raise
    if result.returncode != 0:
        emit_process_diagnostic(
            f"[!] Probe failed with exit code {result.returncode}: {printable}",
            result.stdout,
        )
        emit_process_diagnostic("[!] Probe stderr:", result.stderr)
        return None
    emit_process_streams(f"probe {printable}", result.stdout, result.stderr)
    # Some version probes (notably ``java -version``) write their successful
    # version banner to stderr rather than stdout.  Keep the complete stderr
    # visible above, but use it as the probe value when stdout is empty so a
    # valid tool is not misclassified as unavailable.
    # A successful probe can legally write a banner to stdout as whitespace
    # and its value to stderr (``java -version`` and a few vendor tools do
    # this). Test the rendered content, not truthiness of the raw stream, so
    # the useful stream is never lost.
    stdout = output_text(result.stdout)
    stderr = output_text(result.stderr)
    return (stdout.strip() or stderr.strip())


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
                "--connect-timeout",
                "60",
                "--max-time",
                "900",
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


def go_version() -> str:
    return (ROOT_DIR / ".go-version").read_text(encoding="utf-8").strip()


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None


def bootstrap_local_tools() -> None:
    version = go_version()
    prepend_path(TOOLS_DIR / f"go-{version}" / "bin")


def local_go_root() -> Path:
    return TOOLS_DIR / f"go-{go_version()}"


def go_root_is_complete(root: Path) -> bool:
    """Return whether a local Go tree contains its executable and stdlib."""
    executable = root / "bin" / ("go.exe" if host_platform() == "windows" else "go")
    return executable.is_file() and (root / "src" / "runtime").is_dir()


def configure_go_root(go_executable: Path) -> None:
    """Bind Go's runtime root to the installation that owns the executable.

    Go distributions extracted below ``.local-tools`` are relocatable, but
    the ``go`` launcher cannot infer ``GOROOT`` when its installation is not
    under a standard system prefix.  This is especially visible on Windows:
    the initial version probe may succeed only after the root is explicit,
    and later module commands can otherwise fail inside the Go runtime.
    """
    try:
        root = go_executable.resolve().parent.parent
    except OSError:
        return
    if (root / "bin").is_dir():
        set_env("GOROOT", str(root))


def configure_go_module_proxy() -> None:
    """Keep Go's normal module proxy when a stale blank setting is present.

    Go also reads the per-user ``go env`` file.  A previously persisted
    ``GOPROXY=`` therefore survives the runner's environment sanitization and
    makes ``go mod download`` fail with an empty proxy list.  Preserve every
    explicit non-empty policy (including ``off``), while making the default
    behavior explicit and equivalent to an untouched Go installation.
    """
    if not os.environ.get("GOPROXY", "").strip():
        set_env("GOPROXY", "https://proxy.golang.org,direct")


def find_go() -> Path | None:
    version = go_version()
    candidates: list[Path] = []
    if shutil.which("go"):
        candidates.append(Path(shutil.which("go") or ""))
    candidates.append(local_go_root() / "bin" / ("go.exe" if host_platform() == "windows" else "go"))

    for candidate in candidates:
        if candidate.exists():
            try:
                candidate_root = candidate.resolve().parent.parent
            except OSError:
                continue
            if not go_root_is_complete(candidate_root):
                continue
            configure_go_root(candidate)
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
    go_root = local_go_root()

    download(f"https://go.dev/dl/go{go_version()}.{goos}-{arch}.{suffix}", archive)
    with temporary_directory("dobby-go-") as extract_dir:
        if suffix == "zip":
            with zipfile.ZipFile(archive) as zip_file:
                zip_file.extractall(extract_dir)
        else:
            with tarfile.open(archive) as tar_file:
                tar_file.extractall(extract_dir)
        extracted_root = extract_dir / "go"
        if not go_root_is_complete(extracted_root):
            fail(f"Go archive does not contain a complete standard-library tree: {archive}")
        if go_root.exists():
            try:
                shutil.rmtree(go_root)
            except OSError as error:
                fail(f"cannot replace incomplete Go installation {go_root}: {error}")
        if go_root.exists():
            fail(f"cannot replace incomplete Go installation {go_root}: path remains after removal")
        go_root.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(extracted_root), go_root)

    configure_go_root(go_root / "bin" / ("go.exe" if current == "windows" else "go"))
    prepend_path(go_root / "bin")
    log(f"Installed Go {go_version()} into {go_root}")


def install_linux_packages(skip_deps: bool) -> None:
    if host_platform() != "linux":
        return
    required_commands = ["curl", "unzip", "zip", "git", "gcc", "g++", "pkg-config"]
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
            fail("gcc and g++ are required for the Linux Go backend")
    elif target_platform == "macos":
        if run_capture(["xcode-select", "-p"]):
            return
        if not skip_deps:
            run(["xcode-select", "--install"])
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
                "A working x86_64 MinGW gcc is required for the Windows Go backend "
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
    command = [str(executable), "-dumpmachine"]
    try:
        result = run_bounded_capture(command)
    except OSError as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(
            f"[!] Compiler probe could not start: {command}: {error}",
            stdout,
        )
        emit_process_diagnostic("[!] Compiler probe stderr:", stderr)
        return False, f"compiler=launch_failed errno={error.errno or 0}"
    except subprocess.TimeoutExpired as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(
            f"[!] Compiler probe timed out after {error.timeout}s: {' '.join(command)}",
            stdout,
        )
        emit_process_diagnostic("[!] Compiler probe stderr:", stderr)
        return False, f"compiler=timeout timeout_seconds={error.timeout}"
    if result.returncode != 0:
        emit_process_diagnostic(
            f"[!] Compiler probe failed with exit code {result.returncode}: {' '.join(command)}",
            result.stdout,
        )
        emit_process_diagnostic("[!] Compiler probe stderr:", result.stderr)
    target = result.stdout.strip()
    if result.returncode == 0 and target == "x86_64-w64-mingw32":
        return True, f"compiler=ready target={target}"
    return False, f"compiler=unusable exit_code={result.returncode} target={target}"


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
            fail("dobby_bridge.dll is required for the Windows Go backend")
        archive = TOOLS_DIR / "downloads" / f"{Path(release.asset_name).stem}-v{release.version}.zip"
        download(
            "https://github.com/DobbyVPN/go-go-tunnel/releases/download/"
            f"v{release.version}/{release.asset_name}",
            archive,
        )
        if sha256_file(archive) != release.archive_sha256:
            fail("Windows native bridge archive checksum mismatch")
        with zipfile.ZipFile(archive) as zip_file:
            member = next(
                (item for item in zip_file.infolist() if Path(item.filename).name == release.member_name),
                None,
            )
            if member is None:
                fail("Windows native bridge archive did not contain the expected bridge library")
            with zip_file.open(member) as source, open(bridge, "wb") as target:
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


def configure_macos_deployment_target(
    target_platform: str, environment: dict[str, str]
) -> None:
    """Pin the deployment floor for every macOS Go/cgo build.

    ``-mmacosx-version-min`` only controls the final external link.  cgo
    compiles C/C++ objects before that link, so leaving the SDK default in
    place can produce objects targeting the runner's newer macOS release
    while the executable advertises the product's older deployment floor.
    Keep this invariant in the build helper so backend and CLI builds receive
    the same setting.
    """
    if target_platform != "macos":
        return
    environment["MACOSX_DEPLOYMENT_TARGET"] = MACOS_MINIMUM_SYSTEM_VERSION
    minimum_flag = f"-mmacosx-version-min={MACOS_MINIMUM_SYSTEM_VERSION}"
    for name in ("CGO_CFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"):
        existing = environment.get(name, "").strip()
        environment[name] = " ".join(part for part in (existing, minimum_flag) if part)


def verify_macos_deployment_target(target_platform: str, binary: Path) -> None:
    """Require the completed Mach-O binary to advertise the supported floor."""
    if target_platform != "macos":
        return
    metadata = run_capture(["xcrun", "vtool", "-show-build", str(binary)])
    if metadata is None:
        fail(f"Could not inspect the macOS deployment target: {binary}")
    platforms = re.findall(r"^\s*platform\s+(\S+)\s*$", metadata, re.MULTILINE)
    minimums = re.findall(r"^\s*minos\s+(\S+)\s*$", metadata, re.MULTILINE)
    expected = MACOS_MINIMUM_SYSTEM_VERSION
    if not platforms or any(platform != "MACOS" for platform in platforms):
        fail(f"macOS binary has invalid platform metadata: {binary}")
    if not minimums or any(version != expected for version in minimums):
        fail(f"macOS binary does not declare minimum system version {expected}: {binary}")
    log(f"Verified macOS {expected} deployment target: {binary}")


def install_linux_trusttunnel_bridge(skip_deps: bool) -> None:
    """Stage the checksum-pinned Linux bridge for linking and packaging."""
    if host_platform() != "linux":
        return

    release = BRIDGE_RELEASES["linux"]
    bridge = GO_MODULE_DIR / release.member_name
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        if skip_deps:
            fail("libdobby_bridge.so is required for the Linux Go backend")

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
            member = next(
                (item for item in zip_file.infolist() if Path(item.filename).name == bridge.name),
                None,
            )
            if member is None:
                fail("TrustTunnel Linux bridge archive did not contain the shared library")
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
        with temporary_directory("dobby-libcxx-") as extract_dir:
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
                match = next(extract_dir.rglob(f"{library}.so.1.0"), None)
                if match is None:
                    fail(f"LLVM runtime archive did not contain {library}")
                for name in (f"{library}.so", f"{library}.so.1"):
                    shutil.copyfile(match, runtime / name)
                    (runtime / name).chmod(0o755)
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


def ensure_build_dependencies(target_platform: str, skip_deps: bool) -> None:
    install_linux_packages(skip_deps)
    install_go(skip_deps)
    configure_go_module_proxy()
    ensure_compiler(target_platform, skip_deps)


def go_mod_download(run_tidy: bool) -> None:
    if run_tidy:
        run(["go", "mod", "tidy"], cwd=GO_MODULE_DIR)
    run(["go", "mod", "download"], cwd=GO_MODULE_DIR)


def prepare_go_test_dependencies(skip_deps: bool, run_go_mod_tidy: bool) -> None:
    """Materialize the exact native closure required by Linux Go checks.

    The pinned go-go-tunnel module embeds its Linux cgo search path in the
    module cache. The pinned bridge and libc++ runtimes
    are therefore staged in one place and added through
    CGO_LDFLAGS/LD_LIBRARY_PATH before a check starts. This keeps hosted CI on
    the same dependency contract as the desktop service build without
    compiling a service as a side effect.
    """
    if host_platform() != "linux":
        fail("prepare-go-test-deps is supported only on Linux CI runners")

    ensure_build_dependencies("linux", skip_deps)
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
    architecture = default_service_arch(target_platform)
    return service_target_path_for_arch(target_platform, architecture)


def service_target_path_for_arch(target_platform: str, architecture: str) -> Path:
    return SERVICES_DIR / f"{target_platform}-{architecture}" / SERVICE_NAMES[target_platform]


def stage_windows_runtime(target: Path) -> None:
    """Place the Windows DLLs beside the backend executable that loads them."""
    target.parent.mkdir(parents=True, exist_ok=True)
    for name in ("dobby_bridge.dll", "wintun.dll"):
        source = SERVICES_DIR / name
        if source.is_symlink() or not source.is_file():
            fail(f"required Windows backend runtime is unavailable: {source}")
        destination = target.parent / name
        if source.resolve() != destination.resolve():
            shutil.copyfile(source, destination)
        log(f"Staged {name} beside Windows backend: {destination}")


def build_cli(target_platform: str, arch: str | None = None) -> Path:
    """Build the native operator CLI without invoking the JVM launcher."""
    target_arch = arch or default_service_arch(target_platform)
    output = GO_MODULE_DIR / CLI_NAMES[target_platform]
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": GOOS_BY_PLATFORM[target_platform], "GOARCH": target_arch})
    configure_macos_deployment_target(target_platform, env)
    ldflags = "-buildid="
    run(
        ["go", "build", "-trimpath", f"-ldflags={ldflags}", "-o", output.name, "./cmd/dobbyvpn/"],
        cwd=GO_MODULE_DIR,
        env=env,
    )
    verify_macos_deployment_target(target_platform, output)
    target = service_target_path_for_arch(target_platform, target_arch).with_name(CLI_NAMES[target_platform])
    target.parent.mkdir(parents=True, exist_ok=True)
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
    *,
    build_tags: tuple[str, ...] = (),
    output_path: Path | None = None,
    runtime_dir: Path | None = None,
) -> Path:
    target_arch = arch or default_service_arch(target_platform)
    ensure_build_dependencies(target_platform, skip_deps)
    if target_platform == "windows":
        # The service imports the bridge and loads Wintun at process startup.
        # Stage exactly that runtime closure for the public artifact and local CLI.
        install_wintun(skip_deps)
        install_windows_bridge(skip_deps)
    if target_platform == "linux":
        if target_arch != "amd64":
            fail("The pinned TrustTunnel Linux bridge currently supports amd64 only")
        if runtime_dir is None:
            install_linux_trusttunnel_bridge(skip_deps)
            linux_libcxx_runtime = install_linux_libcxx_runtime(skip_deps)
        else:
            linux_libcxx_runtime = runtime_dir
    else:
        linux_libcxx_runtime = None
    go_mod_download(run_go_mod_tidy)

    output = output_path.resolve() if output_path is not None else service_output_path(target_platform)
    if output_path is not None:
        output.parent.mkdir(parents=True, exist_ok=True)
    if skip_build and output.exists():
        log(f"Reusing existing {output.name}")
    else:
        log(f"Building {target_platform} Go backend for {target_arch}")
        env = os.environ.copy()
        env.update(
            {
                "CGO_ENABLED": "1",
                "GOOS": GOOS_BY_PLATFORM[target_platform],
                "GOARCH": target_arch,
            }
        )
        configure_macos_deployment_target(target_platform, env)
        ldflags = "-buildid="
        if target_platform == "macos":
            # Keep the package's declared macOS 12 floor valid for both
            # the Go backend and the native operator CLI.
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
        command = ["go", "build", "-trimpath"]
        if build_tags:
            command.append(f"-tags={','.join(build_tags)}")
        command.extend(
            [
                f"-ldflags={ldflags}",
                "-o",
                os.fspath(output),
                "./desktop_exports/",
            ]
        )
        run(
            command,
            cwd=GO_MODULE_DIR,
            env=env,
        )

    verify_macos_deployment_target(target_platform, output)

    if output_path is not None:
        target = output
    else:
        target = service_target_path_for_arch(target_platform, target_arch)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(output, target)
    if target_platform == "windows":
        stage_windows_runtime(target)
    if target_platform != "windows":
        target.chmod(target.stat().st_mode | 0o111)
    if output_path is None and target_platform == "macos" and target_arch == "amd64":
        install_macos_amd64_trusttunnel_helper(skip_deps)
    log(f"Copied {output.name} to {target}")
    return target


def read_version() -> str:
    """Read the one repository-owned desktop version source."""
    value = (ROOT_DIR / "VERSION").read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", value):
        fail(f"VERSION must contain a numeric x.y.z value, got {value!r}")
    return value


def required_service_platforms(require_all: bool, platform_value: str) -> list[str]:
    if require_all:
        return ["linux", "macos", "windows"]
    platforms = selected_platforms(platform_value)
    if platforms == ["linux", "macos", "windows"]:
        return platforms
    return platforms


def required_service_paths(require_all: bool, platform_value: str) -> list[Path]:
    if require_all:
        return [
            service_target_path_for_arch("linux", "amd64"),
            service_target_path_for_arch("windows", "amd64"),
            service_target_path_for_arch("macos", "arm64"),
            service_target_path_for_arch("macos", "amd64"),
        ]
    return [service_target_path(platform) for platform in selected_platforms(platform_value)]


def require_services(require_all: bool, platform_value: str) -> None:
    missing = []
    for target in required_service_paths(require_all, platform_value):
        if not target.exists():
            missing.append(str(target))
            continue
        if target.suffix != ".exe":
            target.chmod(target.stat().st_mode | 0o111)
    if missing:
        fail("Missing service binaries:\n" + "\n".join(missing))


def run_native_package() -> None:
    """Assemble release archives without a JVM or third-party packager."""
    major = os.environ.get("APP_MAJOR_VERSION")
    minor = os.environ.get("APP_MINOR_VERSION")
    maintenance = os.environ.get("APP_MAINTENANCE_VERSION")
    if major is not None and minor is not None and maintenance is not None:
        version = f"{major}.{minor}.{maintenance}"
    else:
        version = os.environ.get("VERSION_NAME") or read_version()
    run(
        [
            sys.executable,
            str(ROOT_DIR / ".github" / "scripts" / "package_desktop.py"),
            "--version",
            version,
            "--staging-root",
            str(SERVICES_DIR),
            "--output",
            str(ROOT_DIR / "output"),
        ],
        cwd=ROOT_DIR,
    )


def native_ui_target_path(target_platform: str, target_arch: str | None = None) -> Path:
    architecture = target_arch or default_service_arch(target_platform)
    if target_platform == "windows":
        return SERVICES_DIR / "windows-amd64" / "frontend"
    if target_platform == "macos":
        return SERVICES_DIR / f"macos-{architecture}" / "DobbyVPNMacApp"
    raise ValueError(f"{target_platform} has no desktop UI")


def required_native_ui_paths() -> list[Path]:
    return [
        SERVICES_DIR / "windows-amd64" / "frontend" / "DobbyVPN.exe",
        SERVICES_DIR / "macos-arm64" / "DobbyVPNMacApp",
        SERVICES_DIR / "macos-amd64" / "DobbyVPNMacApp",
    ]


def require_native_ui() -> None:
    missing = [path for path in required_native_ui_paths() if not path.is_file()]
    if missing:
        fail("Missing native desktop frontends:\n" + "\n".join(str(path) for path in missing))
    for path in required_native_ui_paths():
        if path.suffix != ".exe":
            path.chmod(path.stat().st_mode | 0o111)


def build_native_ui(target_platform: str, arch: str | None, output: Path) -> Path:
    if target_platform not in {"windows", "macos"}:
        fail("native desktop UI builds are supported only on Windows and macOS")
    if target_platform != host_platform():
        fail("native desktop UI must be built on its target platform")
    output = output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    version = os.environ.get("VERSION_NAME") or read_version()
    commit = os.environ.get("GITHUB_SHA") or run_capture(["git", "rev-parse", "HEAD"]) or "N/A"
    if target_platform == "windows":
        project = ROOT_DIR / "windows_module" / "DobbyVPN.Windows" / "DobbyVPN.Windows.csproj"
        run([
            "dotnet", "publish", str(project), "--configuration", "Release",
            "--runtime", "win-x64", "--self-contained", "true",
            f"-p:Version={version}", f"-p:SourceCommit={commit}",
            "--output", str(output),
        ], cwd=ROOT_DIR)
        executable = output / "DobbyVPN.exe"
        if not executable.is_file():
            fail(f"WinUI publish did not produce {executable}")
        return executable

    environment = os.environ.copy()
    environment["MACOSX_DEPLOYMENT_TARGET"] = MACOS_MINIMUM_SYSTEM_VERSION
    run(["swift", "build", "--package-path", str(ROOT_DIR / "swift_module"), "--configuration", "release", "--product", "DobbyVPNMacApp"], cwd=ROOT_DIR, env=environment)
    binary = ROOT_DIR / "swift_module" / ".build" / "release" / "DobbyVPNMacApp"
    if not binary.is_file():
        fail(f"Swift build did not produce {binary}")
    if output.suffix == ".app":
        executable = output / "Contents" / "MacOS" / "DobbyVPNMacApp"
        executable.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(binary, executable)
        executable.chmod(executable.stat().st_mode | 0o111)
        info = {
            "CFBundleDisplayName": "Dobby VPN",
            "CFBundleExecutable": "DobbyVPNMacApp",
            "CFBundleIdentifier": "vpn.dobby.desktop",
            "CFBundleName": "Dobby VPN",
            "CFBundlePackageType": "APPL",
            "CFBundleShortVersionString": version,
            "CFBundleVersion": version,
            "DobbySourceCommit": commit,
            "LSMinimumSystemVersion": MACOS_MINIMUM_SYSTEM_VERSION,
        }
        with (output / "Contents" / "Info.plist").open("wb") as handle:
            plistlib.dump(info, handle, sort_keys=True)
    else:
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(binary, output)
        output.chmod(output.stat().st_mode | 0o111)
    return output


def build_app(args: argparse.Namespace) -> None:
    platforms = selected_platforms(args.platform)
    if not args.skip_libs:
        for target_platform in platforms:
            build_service(target_platform, args.arch, args.skip_deps, args.skip_build, args.go_mod_tidy)
            build_cli(target_platform, args.arch)
            if target_platform in {"windows", "macos"}:
                staged_ui = native_ui_target_path(target_platform, args.arch)
                if target_platform == "windows":
                    build_native_ui(target_platform, args.arch, staged_ui)
                else:
                    build_native_ui(target_platform, args.arch, staged_ui)

    if args.require_all_services:
        require_services(True, args.platform)
        required_cli = [
            SERVICES_DIR / "linux-amd64" / "dobby-cli",
            SERVICES_DIR / "windows-amd64" / "dobby-cli.exe",
            SERVICES_DIR / "macos-arm64" / "dobby-cli",
            SERVICES_DIR / "macos-amd64" / "dobby-cli",
        ]
        missing = [path for path in required_cli if not path.is_file()]
        if missing:
            fail("Missing desktop CLI binaries:\n" + "\n".join(str(path) for path in missing))
        for path in required_cli:
            if path.suffix != ".exe":
                path.chmod(path.stat().st_mode | 0o111)
        require_native_ui()
    if args.package:
        run_native_package()


def build_test_seams_service(args: argparse.Namespace) -> None:
    """Build a private Linux hardening service without changing release inputs."""
    if args.platform != "linux":
        fail("The build-local health seam is supported only for Linux hardening")
    output = Path(args.output)
    runtime_dir = Path(args.runtime_dir)
    build_service(
        "linux",
        args.arch or "amd64",
        args.skip_deps,
        False,
        args.go_mod_tidy,
        build_tags=("dobbyvpn_test_seams",),
        output_path=output,
        runtime_dir=runtime_dir,
    )


def add_common_options(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--skip-deps", action="store_true", help="Do not install missing local dependencies.")
    parser.add_argument("--skip-build", action="store_true", help="Reuse existing build outputs when possible.")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build the shared Go backend and native desktop frontend artifacts."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    libs = subparsers.add_parser("libs", help="Build the shared Go backend and operator CLI.")
    add_common_options(libs)
    libs.add_argument("--platform", default="current", help="current, linux, macos, windows, ubuntu, or all.")
    libs.add_argument("--arch", help="Override GOARCH for the service build.")
    libs.add_argument(
        "--with-cli",
        action="store_true",
        help="Also build the native operator CLI for each selected platform.",
    )
    libs.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before go mod download.")

    go_test_deps = subparsers.add_parser(
        "prepare-go-test-deps",
        help="Stage pinned Linux native dependencies and environment for Go tests.",
    )
    add_common_options(go_test_deps)
    go_test_deps.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before go mod download.")

    app = subparsers.add_parser("app", help="Build native desktop inputs and optional package archives.")
    add_common_options(app)
    app.add_argument(
        "--platform",
        default="current",
        help="Service platform to build/copy when --skip-libs is not set.",
    )
    app.add_argument("--arch", help="Override GOARCH for service builds.")
    app.add_argument("--skip-libs", action="store_true", help="Use existing runtime/services binaries.")
    app.add_argument("--require-all-services", action="store_true", help="Require Linux, macOS, and Windows services.")
    app.add_argument("--package", action="store_true", help="Assemble native desktop archives after staging inputs.")
    app.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before service builds.")

    native_ui = subparsers.add_parser("native-ui", help="Build the native Windows or macOS frontend on its target host.")
    native_ui.add_argument("--platform", default="current", help="Current native platform only.")
    native_ui.add_argument("--arch", help="Target architecture for the native frontend.")
    native_ui.add_argument("--output", type=Path, required=True, help="Output directory on Windows or .app bundle path on macOS.")

    test_seams = subparsers.add_parser(
        "test-seams-service",
        help="Build the private Linux hardening service with explicit test seams.",
    )
    test_seams.add_argument("--skip-deps", action="store_true", help="Do not install missing local dependencies.")
    test_seams.add_argument("--platform", default="linux")
    test_seams.add_argument("--arch", default="amd64")
    test_seams.add_argument("--output", required=True, help="Absent absolute service output path.")
    test_seams.add_argument("--runtime-dir", required=True, help="Installed Linux runtime library directory.")
    test_seams.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before the service build.")

    return parser.parse_args()


def main() -> None:
    if not GO_MODULE_DIR.is_dir() or not (ROOT_DIR / "VERSION").is_file():
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
            if args.with_cli:
                build_cli(target_platform, args.arch)
    elif args.command == "prepare-go-test-deps":
        prepare_go_test_dependencies(args.skip_deps, args.go_mod_tidy)
    elif args.command == "app":
        build_app(args)
    elif args.command == "native-ui":
        platform = host_platform() if args.platform == "current" else normalize_platform(args.platform)
        build_native_ui(platform, args.arch, args.output)
    elif args.command == "test-seams-service":
        build_test_seams_service(args)
    else:
        fail(f"Unknown command: {args.command}")

    log("Done")


if __name__ == "__main__":
    main()
