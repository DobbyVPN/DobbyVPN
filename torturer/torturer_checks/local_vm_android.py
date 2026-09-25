"""ReDroid/ADB candidate lifecycle used by :mod:`local_vm`.

The Android VM and its ADB daemon are persistent guest prerequisites.  This
module only installs the two APKs belonging to one run and records every
successful install before doing the next side effect; it never starts,
reboots, or tears down the shared ReDroid runtime.
"""

from __future__ import annotations

import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
from typing import Any

from .android_instrumentation import (
    ROUTING_RULE_CHAIN,
    parse_instrumentation_result,
    validate_complete_throwable_report,
)
from .screenshot_artifacts import (
    ScreenshotIntegrityError,
    assert_marker_matches,
    png_metadata,
)

_SERIAL = re.compile(r"^[A-Za-z0-9._:-]+$")
APP_PACKAGE = "com.dobby.vpn"
COMPANION_PACKAGE = "com.dobby.vpn.test"
_MAIN_ACTIVITY = "com.dobby.ui.MainActivity"
PROBE_ROOT_GLOB = "/data/local/tmp/dobbyvpn-probe-*"
_SCREENSHOT_ROOT = "/data/user/0/com.dobby.vpn/cache/dobbyvpn-rendered-screenshots/"
_SCREENSHOT_MARKER = re.compile(
    rb"^(?:DOBBY_UI_SCREENSHOT|INSTRUMENTATION_STATUS: stream=DOBBY_UI_SCREENSHOT) "
    rb"label=([A-Za-z0-9_-]+) "
    rb"path=(/data/user/0/com\.dobby\.vpn/cache/dobbyvpn-rendered-screenshots/"
    rb"[A-Za-z0-9_-]+\.png) bytes=([0-9]+) sha256=([0-9a-f]{64}) "
    rb"width=([1-9][0-9]*) height=([1-9][0-9]*)$",
    re.MULTILINE,
)
_LOCAL_REQUIRED_SCREENSHOT_LABELS = ("startup", "failure-state", "reopened")


def _error(message: str) -> Exception:
    from .local_vm import LocalVMError

    return LocalVMError(message)


def _run_logged(*args: Any, **kwargs: Any) -> subprocess.CompletedProcess[bytes]:
    from .local_vm import _run_logged as run_logged

    return run_logged(*args, **kwargs)


def _save_state(run_dir: Path, runtime: dict[str, Any], status: str = "starting") -> None:
    from .local_vm import _read_state, _write_json

    state = _read_state(run_dir)
    if state is None:
        state = {"platform": "android"}
    state["runtime"] = runtime
    state["status"] = status
    _write_json(run_dir / "platform.json", state)


def _apk(descriptor: dict[str, Any], name: str) -> Path:
    value = descriptor.get(name)
    if not isinstance(value, str):
        raise _error(f"Android APK path is missing: {name}")
    path = Path(value).resolve()
    if not path.is_file():
        raise _error(f"Android APK is missing: {name}")
    return path


def _adb_and_environment() -> tuple[str, str, dict[str, str]]:
    environment = os.environ.copy()
    socket_path = environment.get("ADB_SERVER_SOCKET")
    if not socket_path:
        raise _error("Android ADB server socket is not configured")
    serial = environment.get("ANDROID_SERIAL")
    if not serial or not _SERIAL.fullmatch(serial):
        raise _error("Android ADB serial is not configured or invalid")
    sdk_root = environment.get("ANDROID_SDK_ROOT") or environment.get("ANDROID_HOME")
    adb: Path | None = None
    if sdk_root:
        candidate = Path(sdk_root) / "platform-tools" / ("adb.exe" if os.name == "nt" else "adb")
        if candidate.is_file():
            adb = candidate.resolve()
    if adb is None:
        found = shutil.which("adb")
        if found:
            adb = Path(found).resolve()
    if adb is None or not adb.is_file():
        raise _error("Android adb is unavailable")
    return str(adb), serial, environment


def _adb_call(adb: str, serial: str, arguments: list[str], *, run_dir: Path, logs: Path,
              label: str, timeout: float, environment: dict[str, str], check: bool = True) -> subprocess.CompletedProcess[bytes]:
    return _run_logged(
        [adb, "-s", serial, *arguments], cwd=run_dir, logs=logs, label=label,
        timeout=timeout, environment=environment, check=check,
    )


def _require_root(adb: str, serial: str, run_dir: Path, logs: Path, timeout: float,
                  environment: dict[str, str]) -> None:
    rooted = _adb_call(adb, serial, ["root"], run_dir=run_dir, logs=logs,
                       label="android-root", timeout=min(timeout, 30), environment=environment)
    if rooted.returncode != 0:
        raise _error("Android ADB root request failed")
    available = _adb_call(adb, serial, ["wait-for-device"], run_dir=run_dir, logs=logs,
                          label="android-wait", timeout=min(timeout, 30), environment=environment)
    if available.returncode != 0:
        raise _error("Android ADB device did not return after root")
    identity = _adb_call(adb, serial, ["shell", "id", "-u"], run_dir=run_dir, logs=logs,
                         label="android-identity", timeout=min(timeout, 15), environment=environment)
    if identity.returncode != 0 or identity.stdout.strip() != b"0":
        raise _error("Android ADB is not root")


def _verify_installed(adb: str, serial: str, package: str, run_dir: Path, logs: Path,
                      timeout: float, environment: dict[str, str]) -> None:
    result = _adb_call(adb, serial, ["shell", "pm", "path", package], run_dir=run_dir,
                       logs=logs, label=f"android-verify-{package.replace('.', '-')}",
                       timeout=min(timeout, 30), environment=environment)
    if not result.stdout.decode("utf-8", errors="replace").strip().startswith("package:"):
        raise _error(f"Android package verification failed: {package}")


def _owned_routing_cleanup_command() -> str:
    """Build the exact dedicated-chain stale-rule cleanup script."""

    return (
        "inventory=$(iptables -S) || exit $?; printf '%s\\n' \"$inventory\"; "
        f'while case "$inventory" in *"-A OUTPUT -j {ROUTING_RULE_CHAIN}"*) '
        "true;; *) false;; esac; do "
        f"iptables -D OUTPUT -j {ROUTING_RULE_CHAIN} || exit $?; "
        "inventory=$(iptables -S) || exit $?; printf '%s\\n' \"$inventory\"; done; "
        f'case "$inventory" in *"-N {ROUTING_RULE_CHAIN}"*) '
        f"iptables -F {ROUTING_RULE_CHAIN} || exit $?; "
        f"iptables -X {ROUTING_RULE_CHAIN} || exit $?;; esac; "
        "inventory=$(iptables -S) || exit $?; printf '%s\\n' \"$inventory\"; "
        f'case "$inventory" in *{ROUTING_RULE_CHAIN}*) '
        f'echo "Android qualification routing chain remains: '
        f'{ROUTING_RULE_CHAIN}" >&2; exit 1;; esac'
    )


def start(run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float) -> dict[str, Any]:
    app = _apk(descriptor, "app")
    companion = _apk(descriptor, "test_companion")
    run_dir = run_dir.resolve()
    adb, serial, environment = _adb_and_environment()
    runtime: dict[str, Any] = {
        "adb": adb,
        "serial": serial,
        "app_package": APP_PACKAGE,
        "companion_package": COMPANION_PACKAGE,
        "installed_packages": [],
    }
    # This must precede even get-state/root: cleanup can recover a setup that
    # fails after an APK install but before start() returns.
    _save_state(run_dir, runtime)
    logs.mkdir(parents=True, exist_ok=True)
    device = _adb_call(adb, serial, ["get-state"], run_dir=run_dir, logs=logs,
                       label="android-state", timeout=min(timeout, 15), environment=environment)
    if device.returncode != 0 or device.stdout.strip() != b"device":
        raise _error("Android ADB device is unavailable")
    _require_root(adb, serial, run_dir, logs, timeout, environment)
    _verify_installed(adb, serial, "android", run_dir=run_dir, logs=logs,
                      timeout=timeout, environment=environment)
    for label, package, apk in (("app", APP_PACKAGE, app), ("companion", COMPANION_PACKAGE, companion)):
        # Record ownership before install.  A killed ADB install can leave a
        # complete APK behind even though the command never returns.
        runtime["installed_packages"].append(package)
        _save_state(run_dir, runtime)
        _adb_call(
            adb, serial, ["install", "--no-incremental", "-r", "-t", str(apk)],
            run_dir=run_dir, logs=logs, label=f"android-install-{label}", timeout=timeout,
            environment=environment,
        )
        _verify_installed(adb, serial, package, run_dir, logs, timeout, environment)
    return runtime


def run_ui(run_dir: Path, runtime: dict[str, Any], logs: Path,
           timeout: float) -> subprocess.CompletedProcess[bytes]:
    """Drive the installed release UI through Android's real input path."""
    adb_value = runtime.get("adb")
    serial = runtime.get("serial")
    if not isinstance(adb_value, str) or not isinstance(serial, str):
        raise _error("Android UI runtime is incomplete")
    if not _SERIAL.fullmatch(serial):
        raise _error("Android UI serial is invalid")
    environment = os.environ.copy()
    if not environment.get("ADB_SERVER_SOCKET"):
        raise _error("Android ADB server socket is not configured")
    # Android instrumentation normally executes the runner in the target
    # application's process.  Do this cold-start cleanup from the controller,
    # before the runner exists; issuing am force-stop from NativeUiInstrumentedTest
    # could terminate the test process together with the stale app
    # NativeActivity surface.
    _adb_call(
        adb_value,
        serial,
        ["shell", "am", "force-stop", APP_PACKAGE],
        run_dir=run_dir,
        logs=logs,
        label="android-native-ui-cold-start",
        timeout=min(timeout, 30),
        environment=environment,
    )
    reporter_test = _adb_call(
        adb_value,
        serial,
        [
            "shell", "am", "instrument", "-w", "-r",
            "-e", "class", "com.dobby.CompleteThrowableReporterTest",
            "com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner",
        ],
        run_dir=run_dir,
        logs=logs,
        label="android-complete-throwable-self-test",
        timeout=min(timeout, 60),
        environment=environment,
        check=False,
    )
    parsed_reporter_test = parse_instrumentation_result(
        returncode=reporter_test.returncode,
        stdout=reporter_test.stdout,
        stderr=reporter_test.stderr,
    )
    if not parsed_reporter_test.succeeded:
        raise _error("Android complete throwable reporter self-test failed")
    try:
        validate_complete_throwable_report(reporter_test.stdout)
    except ValueError as error:
        raise _error(f"Android complete throwable reporter output invalid: {error}") from error
    app_start = _adb_call(
        adb_value,
        serial,
        ["shell", "am", "start", "-W", "-n", f"{APP_PACKAGE}/{_MAIN_ACTIVITY}"],
        run_dir=run_dir,
        logs=logs,
        label="android-native-ui-app-start",
        timeout=min(timeout, 30),
        environment=environment,
    )
    if b"Status: ok" not in app_start.stdout or b"Complete" not in app_start.stdout:
        raise _error("Android native UI app did not start in the foreground")
    result = _adb_call(
        adb_value,
        serial,
        [
            "shell", "am", "instrument", "-w", "-r",
            "-e", "class", "com.dobby.NativeUiInstrumentedTest",
            "com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner",
        ],
        run_dir=run_dir,
        logs=logs,
        label="android-native-ui",
        timeout=min(timeout, 300),
        environment=environment,
        check=False,
    )
    parsed = parse_instrumentation_result(
        returncode=result.returncode,
        stdout=result.stdout,
        stderr=result.stderr,
    )
    collection_error: BaseException | None = None
    try:
        _collect_rendered_screenshots(
            adb_value,
            serial,
            result.stdout,
            succeeded=parsed.succeeded,
            run_dir=run_dir,
            logs=logs,
            timeout=min(timeout, 30),
            environment=environment,
        )
    except BaseException as error:
        collection_error = error
    if parsed.succeeded:
        if collection_error is not None:
            raise collection_error
        return result
    # Preserve the original instrumentation result as primary. A required
    # screenshot collection failure is appended as secondary diagnostics so
    # it cannot hide the product assertion that caused the test to fail.
    stderr = result.stderr or b""
    if collection_error is not None:
        stderr += (
            b"\nANDROID_UI_SCREENSHOT_COLLECTION_FAILED: "
            + str(collection_error).encode("utf-8", errors="backslashreplace")
            + b"\n"
        )
    return subprocess.CompletedProcess(
        result.args, result.returncode or 1, result.stdout, stderr
    )

def _collect_rendered_screenshots(
    adb: str,
    serial: str,
    instrumentation_stdout: bytes,
    *,
    succeeded: bool = True,
    run_dir: Path,
    logs: Path,
    timeout: float,
    environment: dict[str, str],
) -> None:
    """Pull required captured UI frames and validate complete PNG integrity."""

    matches = list(_SCREENSHOT_MARKER.finditer(instrumentation_stdout))
    if not matches:
        raise _error(
            "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: instrumentation emitted no required screenshot markers"
        )
    destination = logs / "screenshots" / "android"
    destination.mkdir(mode=0o700, parents=True, exist_ok=True)
    labels = [match.group(1).decode("ascii") for match in matches]
    if succeeded:
        if tuple(labels) != _LOCAL_REQUIRED_SCREENSHOT_LABELS:
            raise _error(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: required local "
                "milestones must be startup, failure-state, reopened in order"
            )
    else:
        # TestWatcher adds one final failure frame. It may run before any
        # success milestone, so accept only an ordered prefix followed by one
        # failure classification; any duplicate/conflicting marker is unsafe.
        if labels.count("failure") != 1 or labels[-1] != "failure":
            raise _error(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: failed local run "
                "must end with exactly one failure milestone"
            )
        if tuple(labels[:-1]) != _LOCAL_REQUIRED_SCREENSHOT_LABELS[: len(labels) - 1]:
            raise _error(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: local failure "
                "milestones are out of order"
            )
    seen: dict[str, tuple[str, int, str, int, int]] = {}
    for match in matches:
        label = match.group(1).decode("ascii")
        remote = match.group(2).decode("ascii")
        expected_bytes = int(match.group(3))
        expected_sha256 = match.group(4).decode("ascii")
        expected_width = int(match.group(5))
        expected_height = int(match.group(6))
        if (
            not remote.startswith(_SCREENSHOT_ROOT)
            or Path(remote).name != f"{label}.png"
        ):
            raise _error(
                f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: invalid screenshot path for {label}"
            )
        tuple_value = (
            label,
            expected_bytes,
            expected_sha256,
            expected_width,
            expected_height,
        )
        prior = seen.get(remote)
        if prior is not None:
            if prior != tuple_value:
                raise _error(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: duplicate "
                    f"screenshot marker conflicts for {remote}"
                )
            raise _error(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: duplicate "
                f"screenshot marker for {remote}"
            )
        seen[remote] = tuple_value
        local = destination / f"{label}.png"
        pulled = _adb_call(
            adb,
            serial,
            ["pull", remote, str(local)],
            run_dir=run_dir,
            logs=logs,
            label=f"android-screenshot-{label}",
            timeout=max(1.0, timeout),
            environment=environment,
            check=False,
        )
        if pulled.returncode != 0:
            raise _error(
                f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: pull failed for {label}"
            )
        try:
            metadata = png_metadata(local)
            assert_marker_matches(
                metadata,
                bytes_count=expected_bytes,
                sha256_value=expected_sha256,
                width=expected_width,
                height=expected_height,
            )
        except (ScreenshotIntegrityError, OSError) as error:
            try:
                local.unlink(missing_ok=True)
            except OSError as cleanup_error:
                raise _error(
                    f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: invalid {label}; "
                    f"cleanup also failed: {cleanup_error}"
                ) from error
            raise _error(
                f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: invalid {label}: {error}"
            ) from error


def cleanup(run_dir: Path, runtime: dict[str, Any], logs: Path, timeout: float) -> None:
    """Remove only packages successfully installed by this run."""

    run_dir = run_dir.resolve()
    logs.mkdir(parents=True, exist_ok=True)
    adb_value = runtime.get("adb")
    serial = runtime.get("serial")
    if not isinstance(adb_value, str) or not isinstance(serial, str):
        return
    if not _SERIAL.fullmatch(serial):
        raise _error("Android cleanup serial is invalid")
    environment = os.environ.copy()
    if not environment.get("ADB_SERVER_SOCKET"):
        raise _error("Android ADB server socket is not configured")
    packages = runtime.get("installed_packages", [])
    if not isinstance(packages, list):
        raise _error("Android cleanup package state is invalid")
    errors: list[str] = []
    state = _adb_call(adb_value, serial, ["get-state"], run_dir=run_dir, logs=logs,
                      label="android-cleanup-state", timeout=min(timeout, 15), environment=environment)
    if state.returncode != 0 or state.stdout.strip() != b"device":
        raise _error("Android cleanup ADB device is unavailable")
    # Recover the exact qualification-owned chain if a killed run left it.
    try:
        _adb_call(
            adb_value,
            serial,
            ["shell", "sh", "-c", shlex.quote(_owned_routing_cleanup_command())],
            run_dir=run_dir,
            logs=logs,
            label="android-cleanup-routing",
            timeout=min(timeout, 30),
            environment=environment,
        )
    except Exception as error:
        errors.append(
            f"android-cleanup-routing: {type(error).__name__}: {error}"
        )
    # The standalone UID-2000 network probe must stage dex files outside the
    # app sandbox. Remove only the qualification-owned prefix so a killed run
    # cannot leave executable material in the shared device temp directory.
    probe_cleanup = (
        f"for path in {PROBE_ROOT_GLOB}; do "
        "case \"$path\" in "
        "/data/local/tmp/dobbyvpn-probe-*) "
        "[ -d \"$path\" ] || continue; rm -rf -- \"$path\" || exit $? ;; "
        "esac; "
        "done"
    )
    try:
        _adb_call(
            adb_value,
            serial,
            ["shell", "sh", "-c", shlex.quote(probe_cleanup)],
            run_dir=run_dir,
            logs=logs,
            label="android-cleanup-network-probe",
            timeout=min(timeout, 30),
            environment=environment,
        )
    except Exception as error:
        # Keep package/process teardown running and report this independent
        # cleanup failure together with any later failure.
        errors.append(
            f"android-cleanup-network-probe: {type(error).__name__}: {error}"
        )
    for package in packages:
        if not isinstance(package, str) or package not in {APP_PACKAGE, COMPANION_PACKAGE}:
            errors.append(f"invalid owned Android package: {package!r}")
            continue
        package_label = package.replace('.', '-')
        try:
            present = _adb_call(
                adb_value, serial, ["shell", "pm", "list", "packages", package],
                run_dir=run_dir, logs=logs,
                label=f"android-cleanup-probe-{package_label}",
                timeout=min(timeout, 30), environment=environment, check=True,
            )
        except Exception as error:
            # A failed ownership probe must not prevent the companion package
            # from receiving its own stop/uninstall attempt. Keep the exact
            # command stream in the shared log and aggregate this error.
            errors.append(
                f"{package} probe: {type(error).__name__}: {error}"
            )
            continue
        if f"package:{package}".encode() not in present.stdout.splitlines():
            continue  # Already absent is a clean, idempotent teardown.
        try:
            _adb_call(adb_value, serial, ["shell", "am", "force-stop", package], run_dir=run_dir,
                      logs=logs, label=f"android-cleanup-stop-{package_label}", timeout=timeout,
                      environment=environment)
        except Exception as error:
            errors.append(f"{package} force-stop: {type(error).__name__}: {error}")
        try:
            _adb_call(adb_value, serial, ["uninstall", package], run_dir=run_dir, logs=logs,
                      label=f"android-cleanup-uninstall-{package_label}", timeout=timeout,
                      environment=environment)
        except Exception as error:
            errors.append(f"{package} uninstall: {type(error).__name__}: {error}")
    if errors:
        raise _error("; ".join(errors))
