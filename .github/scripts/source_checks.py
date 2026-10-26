#!/usr/bin/env python3
"""Run the product's source checks locally or from GitHub Actions.

The caller selects the runner and prepares platform SDKs. This script owns the
commands and versions used by Go, Swift, Android lint, source scans, and
workflow validation. Downloaded check tools live in one temporary directory
and are removed when the command exits.
"""
from __future__ import annotations

import argparse
import hashlib
import os
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parents[2]
GO_MODULE = ROOT / "core"
SCRIPT_DIR = ROOT / ".github" / "scripts"
sys.path.insert(0, str(SCRIPT_DIR))
sys.path.insert(0, str(SCRIPT_DIR / "desktop"))
sys.path.insert(0, str(SCRIPT_DIR / "android"))
from bounded_process import (
    command_deadline_remaining,
    exception_output,
    run_finite_capture,
    run_streamed,
)
from dependency_fetch import download_file, run_dependency_command
from android_dependency_provenance import (
    ANDROID_BUILD_TOOLS,
    ANDROID_SDK_PLATFORM,
    JAVA_MAJOR,
)
from android_security_inventory import GROUPS, scanner_inputs, verify_scanner_report

# Release asset digests pin the downloaded executable archives. These versions
# intentionally match the existing CI tool versions, except Trivy and SwiftLint
# are pinned here because their former setup floated with the hosted runner.
TOOL_ASSETS = {
    "golangci-lint": {
        "version": "2.13.2",
        "url": "https://github.com/golangci/golangci-lint/releases/download/v2.13.2/golangci-lint-2.13.2-linux-amd64.tar.gz",
        "sha256": "2277d43b98ec0054280f2ac26b53268bae97682444678a59a657dd565da021d6",
        "archive": "tar.gz",
        "member": "golangci-lint",
        "platform": "linux",
    },
    "swiftlint": {
        "version": "0.65.1",
        "url": "https://github.com/realm/SwiftLint/releases/download/0.65.1/portable_swiftlint.zip",
        "sha256": "c1e429b0599cf1b516f369a2d9ec04eaf0e436f3c12b637df8851fa52ff694d0",
        "archive": "zip",
        "member": "swiftlint",
        "platform": "darwin",
    },
    "trivy": {
        "version": "0.71.2",
        "url": "https://github.com/aquasecurity/trivy/releases/download/v0.71.2/trivy_0.71.2_Linux-64bit.tar.gz",
        "sha256": "0510e71e2fd39bf863856d499c8dc19feb4e7336546394c502a8f5cc7ab27460",
        "archive": "tar.gz",
        "member": "trivy",
        "platform": "linux",
    },
    "trufflehog": {
        "version": "3.93.3",
        "url": "https://github.com/trufflesecurity/trufflehog/releases/download/v3.93.3/trufflehog_3.93.3_linux_amd64.tar.gz",
        "sha256": "62af52009a462a50421ca723424e41e0b3a1c8725d74b56de10e49d215ce8545",
        "archive": "tar.gz",
        "member": "trufflehog",
        "platform": "linux",
    },
    "actionlint": {
        "version": "1.7.12",
        "url": "https://github.com/rhysd/actionlint/releases/download/v1.7.12/actionlint_1.7.12_linux_amd64.tar.gz",
        "sha256": "8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8",
        "archive": "tar.gz",
        "member": "actionlint",
        "platform": "linux",
    },
}

NATIVE_GO_PACKAGES = [
    "./diagnostics",
    "./log",
    "./cmd/dobbyvpn",
    "./clientserver/controlplane",
    "./clientserver/executor",
    "./routing",
    "./sessionapi",
    "./sessionapi/mobilebinding",
    "./sessionapi/runtime/internal",
    "./tunnel/platform_engine",
    "./probe",
    "./sessionapi/runtime",
    "./tunnel/protected_dialer",
]


class CheckError(RuntimeError):
    pass


def log(message: str) -> None:
    print(f"[source-checks] {message}", flush=True)


def _forward(stream, payload: bytes | None) -> None:
    if not payload:
        return
    binary = getattr(stream, "buffer", None)
    if binary is not None:
        binary.write(payload)
        binary.flush()
    else:
        stream.write(payload.decode("utf-8", errors="backslashreplace"))
        stream.flush()


def run(command: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None) -> None:
    log("$ " + " ".join(command))
    try:
        timeout = command_deadline_remaining(env)
        if timeout is None:
            result = subprocess.run(command, cwd=cwd, env=env, check=False)
        else:
            result = run_streamed(command, timeout_seconds=timeout, cwd=cwd, env=env)
    except FileNotFoundError as error:
        raise CheckError(f"required command is missing: {error.filename}") from error
    if result.returncode:
        raise SystemExit(result.returncode)


def run_dependency_fetch(
    command: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None,
) -> None:
    """Retry only a standalone dependency download or resolution command."""
    log("$ " + " ".join(command))
    result = run_dependency_command(command, cwd=cwd, env=env)
    if result.returncode:
        raise SystemExit(result.returncode)


def go_environment(go_executable: Path) -> dict[str, str]:
    import desktop_build

    return desktop_build.child_environment([str(go_executable)])


def capture(
    command: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None,
    emit_stdout: bool = True,
) -> str:
    log("$ " + " ".join(command))
    try:
        timeout = command_deadline_remaining(env)
        if timeout is None:
            result = subprocess.run(
                command,
                cwd=cwd,
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
            )
            stdout = result.stdout
            stderr = result.stderr
        else:
            result = run_finite_capture(
                command, cwd=cwd, env=env, timeout_seconds=timeout,
            )
            stdout = result.stdout or b""
            stderr = result.stderr or b""
            if emit_stdout:
                _forward(sys.stdout, stdout)
            _forward(sys.stderr, stderr)
    except FileNotFoundError as error:
        raise CheckError(f"required command is missing: {error.filename}") from error
    except subprocess.TimeoutExpired as error:
        stdout_bytes, stderr_bytes = exception_output(error)
        _forward(sys.stdout, stdout_bytes)
        _forward(sys.stderr, stderr_bytes)
        raise
    if stdout and emit_stdout and timeout is None:
        sys.stdout.write(stdout)
        sys.stdout.flush()
    if stderr and timeout is None:
        sys.stderr.write(stderr)
        sys.stderr.flush()
    if result.returncode:
        raise SystemExit(result.returncode)
    if isinstance(stdout, bytes):
        import locale

        return stdout.decode(locale.getpreferredencoding(False) or "utf-8")
    return stdout


def host_os() -> str:
    system = platform.system().lower()
    return {"linux": "linux", "darwin": "darwin", "windows": "windows"}.get(system, system)


def require_platform(expected: str) -> None:
    actual = host_os()
    if actual != expected:
        raise CheckError(f"this check requires {expected}; current host is {actual}")


def require_command(name: str, purpose: str) -> str:
    command = shutil.which(name)
    if not command:
        raise CheckError(f"{purpose} requires `{name}` on PATH")
    return command


def download(url: str, destination: Path, expected_sha256: str) -> None:
    log(f"Downloading pinned tool asset: {url}")
    try:
        download_file(url, destination, expected_sha256=expected_sha256, default_timeout=120)
    except Exception as error:
        raise CheckError(f"could not download {url}: {error}") from error


def verify_sha256(path: Path, expected: str) -> None:
    with path.open("rb") as archive:
        digest = hashlib.file_digest(archive, "sha256").hexdigest()
    if digest != expected:
        raise CheckError(f"SHA-256 mismatch for {path.name}: expected {expected}, got {digest}")


def safe_archive_path(root: Path, name: str) -> Path:
    relative = PurePosixPath(name)
    if relative.is_absolute() or ".." in relative.parts:
        raise CheckError(f"unsafe path in tool archive: {name}")
    destination = root.joinpath(*relative.parts)
    if root.resolve() not in destination.resolve().parents and destination.resolve() != root.resolve():
        raise CheckError(f"tool archive member escapes temporary directory: {name}")
    return destination


def extract_executable(archive: Path, extract_dir: Path, kind: str, basename: str) -> Path:
    if kind == "tar.gz":
        with tarfile.open(archive, "r:gz") as tar:
            candidates = [member for member in tar.getmembers() if PurePosixPath(member.name).name == basename and member.isfile()]
            if not candidates:
                raise CheckError(f"{archive.name} does not contain {basename}")
            member = candidates[0]
            destination = safe_archive_path(extract_dir, member.name)
            destination.parent.mkdir(parents=True, exist_ok=True)
            source = tar.extractfile(member)
            if source is None:
                raise CheckError(f"could not read {member.name} from {archive.name}")
            with source, destination.open("wb") as output:
                shutil.copyfileobj(source, output)
            destination.chmod(member.mode & 0o777)
            return destination
    if kind == "zip":
        with zipfile.ZipFile(archive) as zipped:
            candidates = [name for name in zipped.namelist() if PurePosixPath(name).name == basename]
            if not candidates:
                raise CheckError(f"{archive.name} does not contain {basename}")
            member = candidates[0]
            destination = safe_archive_path(extract_dir, member)
            destination.parent.mkdir(parents=True, exist_ok=True)
            with zipped.open(member) as source, destination.open("wb") as output:
                shutil.copyfileobj(source, output)
            destination.chmod(0o755)
            return destination
    raise CheckError(f"unsupported tool archive type: {kind}")


class Tools:
    def __init__(self, temp_root: Path):
        self.root = temp_root

    def get(self, name: str) -> Path:
        spec = TOOL_ASSETS[name]
        require_platform(spec["platform"])
        archive = self.root / f"{name}.{spec['archive']}"
        download(spec["url"], archive, spec["sha256"])
        verify_sha256(archive, spec["sha256"])
        executable = extract_executable(archive, self.root / f"{name}-unpacked", spec["archive"], spec["member"])
        if not executable.is_file():
            raise CheckError(f"pinned {name} executable was not extracted")
        return executable


def ensure_go() -> Path:
    """Use desktop_build's pinned Go preparation and keep its exact path."""
    import desktop_build

    return desktop_build.prepare_go(skip_deps=False)


def prepare_linux_go_test_dependencies() -> Path:
    require_platform("linux")
    import desktop_build

    return desktop_build.prepare_go_test_dependencies(skip_deps=False, run_go_mod_tidy=False)


def prepare_go_source_checks() -> Path:
    import desktop_build

    if host_os() == "linux":
        return prepare_linux_go_test_dependencies()
    go = desktop_build.prepare_toolchain(host_os(), skip_deps=False)
    desktop_build.go_mod_download(go, run_tidy=False)
    return go


def prepare_native_runtime_check() -> Path:
    import desktop_build

    if host_os() == "linux" and desktop_build.go_arch_from_machine() == "amd64":
        return prepare_linux_go_test_dependencies()
    go = desktop_build.prepare_toolchain(host_os(), skip_deps=False)
    desktop_build.go_mod_download(go, run_tidy=False)
    if host_os() == "windows":
        desktop_build.install_windows_bridge(skip_deps=False)
        desktop_build.install_wintun(skip_deps=False)
    return go


def go_build() -> None:
    """Compile product packages with the native toolchain for this runner."""
    import desktop_build

    go = prepare_native_runtime_check()
    environment = go_environment(go)
    packages = ["./..."]
    flags: list[str] = []
    if host_os() == "darwin":
        desktop_build.configure_macos_deployment_target("macos", environment)
        desktop_build.configure_macos_bridge_link(environment)
        flags.append("-ldflags=-linkmode=external -extldflags=-lc++")
    elif host_os() == "windows":
        environment["PATH"] = str(GO_MODULE) + os.pathsep + environment.get("PATH", "")
    elif host_os() == "linux" and desktop_build.go_arch_from_machine() != "amd64":
        packages = NATIVE_GO_PACKAGES.copy()
        packages.remove("./clientserver/executor")
        log("Executor build is unavailable on Linux ARM64: the pinned TrustTunnel bridge supports Linux AMD64 only")

    run([str(go), "build", *flags, *packages], cwd=GO_MODULE, env=environment)


def lint_go(tools: Tools) -> None:
    require_platform("linux")
    go = prepare_go_source_checks()
    linter = tools.get("golangci-lint")
    capture([str(linter), "version"])
    run(
        [str(linter), "run", "--output.text.path", "stdout", "--config", ".golangci.yml", "./..."],
        cwd=GO_MODULE,
        env=go_environment(go),
    )


def require_android_sdk() -> Path:
    candidates = [os.environ.get("ANDROID_SDK_ROOT"), os.environ.get("ANDROID_HOME")]
    local_properties = ROOT / "ui" / "android" / "local.properties"
    if local_properties.is_file():
        match = re.search(r"^sdk\.dir=(.+)$", local_properties.read_text(encoding="utf-8"), re.MULTILINE)
        if match:
            candidates.append(match.group(1).replace("\\:", ":").replace("\\\\", "\\"))
    sdk = next((Path(value).expanduser() for value in candidates if value and value.strip()), None)
    if sdk is None or not sdk.is_dir():
        raise CheckError("Android Lint requires ANDROID_SDK_ROOT/ANDROID_HOME or ui/android/local.properties")
    platform_dir = sdk / "platforms" / ANDROID_SDK_PLATFORM
    if not platform_dir.is_dir():
        raise CheckError(f"Android Lint requires Android SDK platform {ANDROID_SDK_PLATFORM} at {platform_dir}")
    return sdk


def android_check_tools() -> tuple[Path, Path]:
    require_platform("linux")
    go = ensure_go()
    java = require_command("java", "Android Lint")
    try:
        probe = subprocess.run([java, "-version"], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except OSError as error:
        raise CheckError(f"could not inspect Java version: {error}") from error
    java_output = probe.stderr + probe.stdout
    if probe.returncode or not re.search(rf'"{JAVA_MAJOR}(?:\.|\")', java_output):
        raise CheckError(f"Android Lint requires JDK {JAVA_MAJOR}; detected {java_output.strip() or '<no version output>'}")
    sdk = require_android_sdk()
    build_tools = sdk / "build-tools" / ANDROID_BUILD_TOOLS
    if not build_tools.is_dir():
        raise CheckError(f"Android Lint requires build-tools {ANDROID_BUILD_TOOLS} at {build_tools}")
    gradle = ROOT / "ui" / "android" / "gradlew"
    if not gradle.is_file():
        raise CheckError(f"Android Gradle wrapper is missing: {gradle}")
    return gradle, go


def android_security(tools: Tools, *, output_dir: Path | None = None, require_tests: bool = False) -> None:
    gradle, go = android_check_tools()
    directory = (output_dir or tools.root / "android-security").resolve()
    directory.mkdir(parents=True, exist_ok=True)
    inventory = directory / "resolved-gradle.json"
    report = directory / "trivy.json"
    # Only this check's known outputs are replaced. A previous successful scan
    # cannot satisfy a failed resolution or missing scanner result.
    for path in (inventory, report, *(directory / f"{group}.gradle.lockfile" for group in GROUPS)):
        path.unlink(missing_ok=True)
    resolver = SCRIPT_DIR / "android" / "resolve_dependencies.gradle"
    run_dependency_fetch(
        [
            str(gradle), f"-PdobbyGoBinary={go}", "--init-script", str(resolver),
            f"-PdobbySecurityInventory={inventory}", "resolveDobbyDependencies", "--no-daemon", "--stacktrace",
        ],
        cwd=ROOT / "ui" / "android",
    )
    try:
        expected = scanner_inputs(inventory, directory, require_tests=require_tests)
    except (OSError, ValueError) as error:
        raise CheckError(f"invalid resolved Android dependency inventory: {error}") from error
    _forward(sys.stdout, inventory.read_bytes())
    trivy = tools.get("trivy")
    try:
        run([
            str(trivy), "fs", "--cache-dir", str(tools.root / "trivy-cache"),
            "--severity", "HIGH,CRITICAL", "--ignore-unfixed", "--exit-code", "1",
            "--include-dev-deps", "--list-all-pkgs", "--format", "json", "--output", str(report),
            str(directory),
        ])
    finally:
        if report.is_file():
            _forward(sys.stdout, report.read_bytes())
    try:
        verify_scanner_report(report, expected)
    except (OSError, ValueError) as error:
        raise CheckError(f"incomplete Android dependency scan: {error}") from error
    log(f"Android dependency scan verified all resolved packages in {len(expected)} groups")


ANDROID_LINT_REPORT_NAMES = ("lint-results-release.txt", "lint-results-release.sarif")


def _android_lint_report_paths(android_root: Path) -> tuple[Path, Path]:
    return (
        android_root / "app" / "build" / "intermediates" / "lint_intermediate_text_report"
        / "release" / "lintReportRelease" / ANDROID_LINT_REPORT_NAMES[0],
        android_root / "app" / "build" / "reports" / ANDROID_LINT_REPORT_NAMES[1],
    )


def _clear_android_lint_reports(report_paths: tuple[Path, ...]) -> None:
    for path in dict.fromkeys(path.resolve() for path in report_paths):
        try:
            path.unlink(missing_ok=True)
        except OSError as error:
            raise CheckError(f"could not remove stale Android lint report {path}: {error}") from error


def _collect_android_lint_reports(
    report_paths: tuple[Path, ...],
    output_directory: Path,
    primary_error: BaseException | None,
) -> None:
    text_report = report_paths[0]
    collection_errors: list[Exception] = []

    try:
        has_text_report = text_report.is_file()
    except OSError as error:
        has_text_report = False
        collection_errors.append(error)
    if has_text_report:
        try:
            _forward(sys.stdout, text_report.read_bytes())
        except Exception as error:
            collection_errors.append(error)
    elif not collection_errors:
        log("Android lint text report unavailable: Gradle did not generate lint-results-release.txt")

    for report in report_paths:
        try:
            if not report.is_file():
                continue
            destination = output_directory / report.name
            if report.resolve() == destination.resolve():
                continue
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(report, destination)
        except Exception as error:
            collection_errors.append(error)

    if not collection_errors:
        return

    messages = [
        f"Android lint report collection failed: {type(error).__name__}: {error}"
        for error in collection_errors
    ]
    for message in messages:
        try:
            print(f"[source-checks] ERROR: {message}", file=sys.stderr, flush=True)
        except Exception:
            pass
        if primary_error is not None:
            try:
                primary_error.add_note(message)
            except Exception:
                pass
    if primary_error is None:
        raise CheckError("; ".join(messages)) from collection_errors[0]


def lint_android(tools: Tools, *, output_dir: Path | None = None, require_tests: bool = False) -> None:
    output_directory = (output_dir or tools.root / "android-security").resolve()
    android_root = ROOT / "ui" / "android"
    report_paths = _android_lint_report_paths(android_root)
    try:
        output_directory.mkdir(parents=True, exist_ok=True)
    except OSError as error:
        raise CheckError(f"could not create Android check output directory {output_directory}: {error}") from error
    _clear_android_lint_reports((*report_paths, *(output_directory / path.name for path in report_paths)))
    android_security(tools, output_dir=output_dir, require_tests=require_tests)
    gradle, go = android_check_tools()
    primary_error: BaseException | None = None
    try:
        run([str(gradle), f"-PdobbyGoBinary={go}", ":app:lintRelease", "--no-daemon", "--stacktrace"], cwd=ROOT / "ui" / "android")
    except BaseException as error:
        primary_error = error
        raise
    finally:
        _collect_android_lint_reports(report_paths, output_directory, primary_error)


def lint_swift(tools: Tools) -> None:
    require_platform("darwin")
    swiftlint = tools.get("swiftlint")
    run([str(swiftlint), "lint", "--config", ".swiftlint.yml"], cwd=ROOT / "ui" / "apple")


def trivy_scan(
    tools: Tools,
    targets: tuple[Path, ...] = (
        GO_MODULE, SCRIPT_DIR / "requirements-native-ui.txt",
        SCRIPT_DIR / "android" / "requirements-fdroid.txt",
    ),
) -> None:
    require_platform("linux")
    trivy = tools.get("trivy")
    cache_dir = tools.root / "trivy-cache"
    for target in targets:
        run(
            [
                str(trivy), "fs", "--cache-dir", str(cache_dir),
                "--severity", "HIGH,CRITICAL", "--ignore-unfixed",
                "--exit-code", "1", "--file-patterns", r"pip:requirements-(native-ui|fdroid)\.txt",
                str(target),
            ],
            cwd=ROOT,
        )


def resolve_git_repository(requested: Path | None) -> Path:
    candidate = requested.resolve() if requested else ROOT
    if not (candidate / ".git").exists():
        raise CheckError(
            "TruffleHog scans Git history and needs a checkout with .git; "
            "pass --git-repository PATH from the controller checkout"
        )
    output = capture(["git", "rev-parse", "--show-toplevel"], cwd=candidate).strip()
    if not output:
        raise CheckError(f"could not find Git repository root at {candidate}")
    return Path(output).resolve()


def trufflehog_scan(tools: Tools, git_repository: Path | None) -> None:
    require_platform("linux")
    trufflehog = tools.get("trufflehog")
    repository = resolve_git_repository(git_repository)
    run(
        [str(trufflehog), "--no-update", "git", f"file://{repository}", "--only-verified", "--results=verified,unknown"],
        cwd=repository,
    )


def actionlint(tools: Tools) -> None:
    require_platform("linux")
    actionlint_bin = tools.get("actionlint")
    run([str(actionlint_bin), "-shellcheck=", "-color"], cwd=ROOT)


def golangci_cache_paths() -> list[Path]:
    xdg_cache = os.environ.get("XDG_CACHE_HOME")
    if xdg_cache:
        return [Path(xdg_cache) / "golangci-lint"]
    if host_os() == "darwin":
        return [Path.home() / "Library" / "Caches" / "golangci-lint"]
    if host_os() == "windows":
        local_app_data = os.environ.get("LOCALAPPDATA")
        return [Path(local_app_data) / "golangci-lint"] if local_app_data else []
    return [Path.home() / ".cache" / "golangci-lint"]


def clean_caches() -> None:
    import desktop_build

    go_executable = desktop_build.find_go()
    if go_executable is not None:
        run(
            [str(go_executable), "clean", "-cache", "-testcache"],
            cwd=GO_MODULE,
            env=go_environment(go_executable),
        )
    else:
        log("Go is unavailable; there is no Go build/test cache to clean")

    for cache in golangci_cache_paths():
        if cache.is_dir():
            log(f"Removing golangci-lint cache: {cache}")
            shutil.rmtree(cache)
        else:
            log(f"No golangci-lint cache at {cache}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "check",
        choices=(
            "go-build", "lint-go", "lint-android", "android-security", "lint-swift", "security",
            "actionlint", "cache-clean",
        ),
    )
    parser.add_argument("--git-repository", type=Path, help="Checkout whose full history TruffleHog scans")
    parser.add_argument("--output-dir", type=Path, help="Retain Android dependency inventories and scanner reports")
    parser.add_argument("--require-test-dependencies", action="store_true", help="Require resolved unit/instrumentation test inventories")
    args = parser.parse_args()
    if args.require_test_dependencies and args.check not in {"android-security", "lint-android"}:
        parser.error("--require-test-dependencies requires an Android source check")
    if args.output_dir is not None and args.check not in {"android-security", "lint-android"}:
        parser.error("--output-dir requires an Android source check")
    return args


def main() -> int:
    args = parse_args()
    needs_tools = args.check in {"lint-go", "lint-android", "android-security", "lint-swift", "security", "actionlint"}
    temporary_tools: tempfile.TemporaryDirectory[str] | None = None
    try:
        tools: Tools | None = None
        if needs_tools:
            temporary_tools = tempfile.TemporaryDirectory(prefix="dobbyvpn-source-check-tools-")
            tools = Tools(Path(temporary_tools.name))
        if args.check == "go-build":
            go_build()
        elif args.check == "lint-go":
            assert tools is not None
            lint_go(tools)
        elif args.check == "lint-android":
            assert tools is not None
            lint_android(tools, output_dir=args.output_dir, require_tests=args.require_test_dependencies)
        elif args.check == "android-security":
            assert tools is not None
            android_security(tools, output_dir=args.output_dir, require_tests=args.require_test_dependencies)
        elif args.check == "lint-swift":
            assert tools is not None
            lint_swift(tools)
        elif args.check == "security":
            assert tools is not None
            trivy_scan(tools)
            trufflehog_scan(tools, args.git_repository)
        elif args.check == "actionlint":
            assert tools is not None
            actionlint(tools)
        elif args.check == "cache-clean":
            clean_caches()
        else:
            raise CheckError(f"unknown source check: {args.check}")
    except CheckError as error:
        print(f"[source-checks] ERROR: {error}", file=sys.stderr)
        return 2
    finally:
        if temporary_tools is not None:
            temporary_tools.cleanup()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
