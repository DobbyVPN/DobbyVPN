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
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parents[2]
GO_MODULE = ROOT / "core"
SCRIPT_DIR = ROOT / ".github" / "scripts"
sys.path.insert(0, str(SCRIPT_DIR / "desktop"))

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
    "./probe",
    "./sessionapi/runtime",
    "./tunnel/protected_dialer",
]


class CheckError(RuntimeError):
    pass


def log(message: str) -> None:
    print(f"[source-checks] {message}", flush=True)


def run(command: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None) -> None:
    log("$ " + " ".join(command))
    try:
        result = subprocess.run(command, cwd=cwd, env=env, check=False)
    except FileNotFoundError as error:
        raise CheckError(f"required command is missing: {error.filename}") from error
    if result.returncode:
        raise SystemExit(result.returncode)


def go_environment(go_executable: Path) -> dict[str, str]:
    import desktop_build

    return desktop_build.child_environment([str(go_executable)])


def capture(
    command: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None,
) -> str:
    log("$ " + " ".join(command))
    try:
        result = subprocess.run(
            command,
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except FileNotFoundError as error:
        raise CheckError(f"required command is missing: {error.filename}") from error
    if result.stdout:
        sys.stdout.write(result.stdout)
        sys.stdout.flush()
    if result.stderr:
        sys.stderr.write(result.stderr)
        sys.stderr.flush()
    if result.returncode:
        raise SystemExit(result.returncode)
    return result.stdout


def host_os() -> str:
    system = platform.system().lower()
    return {"linux": "linux", "darwin": "darwin", "windows": "windows"}.get(system, system)


def require_platform(expected: str) -> None:
    actual = host_os()
    if actual != expected:
        raise CheckError(f"this check requires {expected}; current host is {actual}")


def python_tests() -> None:
    """Run the product functional-support and source-check unit suites."""
    requirements = SCRIPT_DIR / "requirements-native-ui.txt"
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-python-tests-") as package_dir:
        run(
            [
                sys.executable, "-m", "pip", "install",
                "--disable-pip-version-check", "--only-binary=:all:", "--no-deps",
                "--target", package_dir, "--requirement", str(requirements),
            ]
        )
        environment = os.environ.copy()
        python_paths = [package_dir, str(ROOT / "torturer")]
        existing = environment.get("PYTHONPATH")
        if existing:
            python_paths.append(existing)
        environment["PYTHONPATH"] = os.pathsep.join(python_paths)
        run(
            [
                sys.executable, "-m", "unittest", "discover",
                "-s", "torturer/tests", "-p", "test_*.py",
            ],
            env=environment,
        )
        for suite in ("android", "release"):
            run(
                [
                    sys.executable, "-m", "unittest", "discover",
                    "-s", str(SCRIPT_DIR / suite), "-p", "test_*.py",
                ],
                env=environment,
            )


def require_command(name: str, purpose: str) -> str:
    command = shutil.which(name)
    if not command:
        raise CheckError(f"{purpose} requires `{name}` on PATH")
    return command


def download(url: str, destination: Path) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": "DobbyVPN-source-checks"})
    log(f"Downloading pinned tool asset: {url}")
    try:
        with urllib.request.urlopen(request, timeout=120) as response, destination.open("wb") as output:
            shutil.copyfileobj(response, output)
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
        download(spec["url"], archive)
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

    go = desktop_build.prepare_toolchain(host_os(), skip_deps=False)
    desktop_build.go_mod_download(go, run_tidy=False)
    return go


def coverage_dir(args: argparse.Namespace, name: str) -> tuple[Path, tempfile.TemporaryDirectory[str] | None]:
    if args.output_dir:
        output = args.output_dir.resolve()
        output.mkdir(parents=True, exist_ok=True)
        return output, None
    runner_temp = os.environ.get("RUNNER_TEMP")
    if runner_temp:
        output = Path(runner_temp) / f"dobbyvpn-{name}-coverage"
        output.mkdir(parents=True, exist_ok=True)
        return output, None
    temporary = tempfile.TemporaryDirectory(prefix=f"dobbyvpn-{name}-coverage-")
    return Path(temporary.name), temporary


def write_step_summary(title: str, text: str) -> None:
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        with open(summary_path, "a", encoding="utf-8") as summary:
            summary.write(f"## {title}\n\n```text\n{text.rstrip()}\n```\n")


def go_unit(args: argparse.Namespace) -> None:
    require_platform("linux")
    go = prepare_go_source_checks()
    output, temporary = coverage_dir(args, "go")
    try:
        profile = output / "coverage.out"
        run(
            [
                str(go), "test", "-p", "1", "-v", "-tags=ci", "-covermode=atomic",
                "-coverpkg=./...", f"-coverprofile={profile}", "./...",
            ],
            cwd=GO_MODULE,
            env=go_environment(go),
        )
        report = capture(
            [str(go), "tool", "cover", f"-func={profile}"],
            cwd=GO_MODULE,
            env=go_environment(go),
        )
        write_step_summary("Unified Go coverage", report)
    finally:
        if temporary is not None:
            temporary.cleanup()


def go_race() -> None:
    require_platform("linux")
    go = prepare_go_source_checks()
    run(
        [str(go), "test", "-p", "1", "-v", "-race", "./routing/...", "./sessionapi/...", "./tunnel/..."],
        cwd=GO_MODULE,
        env=go_environment(go),
    )


def go_native_runtime() -> None:
    go = prepare_native_runtime_check()
    run([str(go), "test", "-v", "-race", *NATIVE_GO_PACKAGES], cwd=GO_MODULE, env=go_environment(go))


def go_tests(args: argparse.Namespace) -> None:
    require_platform("linux")
    go = prepare_go_source_checks()
    output, temporary = coverage_dir(args, "go")
    try:
        profile = output / "coverage.out"
        run(
            [
                str(go), "test", "-p", "1", "-v", "-tags=ci", "-covermode=atomic",
                "-coverpkg=./...", f"-coverprofile={profile}", "./...",
            ],
            cwd=GO_MODULE,
            env=go_environment(go),
        )
        report = capture(
            [str(go), "tool", "cover", f"-func={profile}"],
            cwd=GO_MODULE,
            env=go_environment(go),
        )
        write_step_summary("Unified Go coverage", report)
        run(
            [str(go), "test", "-p", "1", "-v", "-race", "./routing/...", "./sessionapi/...", "./tunnel/..."],
            cwd=GO_MODULE,
            env=go_environment(go),
        )
    finally:
        if temporary is not None:
            temporary.cleanup()


def swift_unit(args: argparse.Namespace) -> None:
    require_platform("darwin")
    swift = require_command("swift", "Swift lifecycle unit tests")
    xcrun = require_command("xcrun", "Swift lifecycle coverage export")
    output, temporary = coverage_dir(args, "swift")
    try:
        run([swift, "test", "-v", "--enable-code-coverage", "--package-path", str(ROOT / "ui" / "apple")])
        bin_path = capture([swift, "build", "--show-bin-path", "--package-path", str(ROOT / "ui" / "apple")]).strip()
        build_path = Path(bin_path)
        profiles = list(build_path.glob("**/codecov/default.profdata"))
        tests = [path for path in build_path.glob("**/*.xctest/Contents/MacOS/*PackageTests") if os.access(path, os.X_OK)]
        if len(profiles) != 1 or len(tests) != 1:
            raise CheckError(
                f"expected one Swift coverage profile and test executable, found {len(profiles)} and {len(tests)}"
            )
        lcov = output / "swift-lifecycle-core.lcov"
        summary = output / "swift-lifecycle-core-coverage.md"
        coverage = output / "swift-lifecycle-core.profdata"
        shutil.copyfile(profiles[0], coverage)
        report = capture(
            [xcrun, "llvm-cov", "export", str(tests[0]), f"-instr-profile={profiles[0]}", "-format=lcov"]
        )
        lcov.write_text(report, encoding="utf-8")
        run(
            [
                sys.executable,
                str(SCRIPT_DIR / "apple" / "check_swift_coverage.py"),
                "--lcov", str(lcov),
                "--source-root", str(ROOT / "ui" / "apple" / "ios" / "integration"),
                "--summary", str(summary),
            ]
        )
        write_step_summary("Swift lifecycle-core coverage", summary.read_text(encoding="utf-8"))
    finally:
        if temporary is not None:
            temporary.cleanup()


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
    ndk = sdk / "ndk" / "28.1.13356709"
    if not ndk.is_dir():
        raise CheckError(f"Android Lint requires NDK 28.1.13356709 at {ndk}")
    platform_dir = sdk / "platforms" / "android-35"
    if not platform_dir.is_dir():
        raise CheckError(f"Android Lint requires Android SDK platform 35 at {platform_dir}")
    return sdk


def lint_android() -> None:
    require_platform("linux")
    go = ensure_go()
    java = require_command("java", "Android Lint")
    try:
        probe = subprocess.run([java, "-version"], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except OSError as error:
        raise CheckError(f"could not inspect Java version: {error}") from error
    java_output = probe.stderr + probe.stdout
    if probe.returncode or not re.search(r'"17(?:\.|\")', java_output):
        raise CheckError(f"Android Lint requires JDK 17; detected {java_output.strip() or '<no version output>'}")
    sdk = require_android_sdk()
    build_tools = sdk / "build-tools" / "36.0.0"
    if not build_tools.is_dir():
        raise CheckError(f"Android Lint requires build-tools 36.0.0 at {build_tools}")
    gradle = ROOT / "ui" / "android" / "gradlew"
    if not gradle.is_file():
        raise CheckError(f"Android Gradle wrapper is missing: {gradle}")
    run([str(gradle), f"-PdobbyGoBinary={go}", ":app:lintRelease", "--no-daemon", "--stacktrace"], cwd=ROOT / "ui" / "android")


def lint_swift(tools: Tools) -> None:
    require_platform("darwin")
    swiftlint = tools.get("swiftlint")
    run([str(swiftlint), "lint", "--config", ".swiftlint.yml"], cwd=ROOT / "ui" / "apple")


def trivy_scan(tools: Tools) -> None:
    require_platform("linux")
    trivy = tools.get("trivy")
    cache_dir = tools.root / "trivy-cache"
    ignore = ROOT / ".trivyignore"
    for target in (ROOT / "core", ROOT / "ui" / "android"):
        run(
            [
                str(trivy), "fs", "--cache-dir", str(cache_dir),
                "--severity", "HIGH,CRITICAL", "--ignore-unfixed",
                "--exit-code", "1", "--ignorefile", str(ignore), str(target),
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
        [str(trufflehog), "git", f"file://{repository}", "--only-verified", "--results=verified,unknown"],
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
            "python-tests", "go-tests", "go-unit", "go-race", "go-native-runtime",
            "swift-unit", "lint-go", "lint-android", "lint-swift", "security",
            "actionlint", "cache-clean",
        ),
    )
    parser.add_argument("--output-dir", type=Path, help="Keep coverage outputs in this directory")
    parser.add_argument("--git-repository", type=Path, help="Checkout whose full history TruffleHog scans")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    needs_tools = args.check in {"lint-go", "lint-swift", "security", "actionlint"}
    temporary_tools: tempfile.TemporaryDirectory[str] | None = None
    try:
        tools: Tools | None = None
        if needs_tools:
            temporary_tools = tempfile.TemporaryDirectory(prefix="dobbyvpn-source-check-tools-")
            tools = Tools(Path(temporary_tools.name))
        if args.check == "go-unit":
            go_unit(args)
        elif args.check == "go-race":
            go_race()
        elif args.check == "go-tests":
            go_tests(args)
        elif args.check == "go-native-runtime":
            go_native_runtime()
        elif args.check == "swift-unit":
            swift_unit(args)
        elif args.check == "python-tests":
            python_tests()
        elif args.check == "lint-go":
            assert tools is not None
            lint_go(tools)
        elif args.check == "lint-android":
            lint_android()
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
