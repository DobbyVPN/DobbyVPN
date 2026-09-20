"""Validate the native Go runtime XCFramework used by iOS packaging.

This is build-owned code.  The package script invokes it directly, while the
functional iOS Simulator helper loads this same module for its preflight
assertions.  Keep the validator independent of the test package so packaging
also works from a product checkout without the Torturer helpers installed.
"""

from __future__ import annotations

import argparse
from pathlib import Path
import plistlib
import subprocess
import sys
from typing import Protocol, Sequence


DEFAULT_VALIDATION_TIMEOUT_SECONDS = 30
_SUPPORTED_ARCHITECTURES = frozenset(("arm64", "amd64"))
_XCODE_ARCHITECTURES = {"arm64": "arm64", "amd64": "x86_64"}


class IOSRuntimeFrameworkError(RuntimeError):
    """The supplied XCFramework is not safe and usable for the requested target."""


class _CommandResult(Protocol):
    returncode: int
    stdout: str
    stderr: str


class RuntimeFrameworkCommandRunner(Protocol):
    def run(
        self,
        command: Sequence[str],
        *,
        timeout_seconds: float | None = None,
    ) -> _CommandResult:
        """Run one shell-free validation command."""


class _SubprocessCommandRunner:
    """Small CLI runner for the validation command used by shell packaging."""

    def run(
        self,
        command: Sequence[str],
        *,
        timeout_seconds: float | None = None,
    ) -> subprocess.CompletedProcess[str]:
        timeout = (
            timeout_seconds
            if timeout_seconds is not None
            else DEFAULT_VALIDATION_TIMEOUT_SECONDS
        )
        try:
            return subprocess.run(
                list(command),
                check=False,
                capture_output=True,
                text=True,
                timeout=timeout,
            )
        except subprocess.TimeoutExpired as error:
            raise IOSRuntimeFrameworkError(
                f"command timed out after {timeout:g}s: {' '.join(command)}"
            ) from error
        except OSError as error:
            raise IOSRuntimeFrameworkError(
                f"command could not start: {' '.join(command)}: {error}"
            ) from error


def validate_runtime_framework(
    runtime_framework: Path,
    architecture: str,
    *,
    platform_variant: str,
    runner: RuntimeFrameworkCommandRunner | None = None,
    timeout_seconds: float = DEFAULT_VALIDATION_TIMEOUT_SECONDS,
) -> Path:
    """Validate XCFramework metadata, containment, and the real Mach-O slice.

    ``platform_variant`` is ``simulator`` for an iOS Simulator slice and
    ``device`` for the physical-iOS slice.  The same implementation is used
    by the Python functional contract and by the standalone package script.
    """
    if architecture not in _SUPPORTED_ARCHITECTURES:
        raise IOSRuntimeFrameworkError(
            "Simulator/device architecture must be arm64 or amd64"
        )
    if platform_variant not in {"simulator", "device"}:
        raise IOSRuntimeFrameworkError(
            f"unsupported iOS runtime platform variant: {platform_variant}"
        )
    if platform_variant == "device" and architecture != "arm64":
        raise IOSRuntimeFrameworkError(
            "physical iOS runtime validation requires the arm64 architecture"
        )

    try:
        framework = Path(runtime_framework).expanduser().resolve(strict=True)
    except (OSError, RuntimeError, ValueError) as error:
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework path could not be resolved: "
            f"{runtime_framework}: {error}"
        ) from error
    if not framework.is_dir():
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework is unavailable: {framework}"
        )
    info_plist = _resolve_framework_path(
        framework / "Info.plist", framework, "XCFramework metadata"
    )
    if not info_plist.is_file():
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework metadata is unavailable: {info_plist}"
        )
    try:
        with info_plist.open("rb") as source:
            metadata = plistlib.load(source)
    except (OSError, plistlib.InvalidFileException, ValueError) as error:
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework metadata is unreadable: "
            f"{info_plist}: {error}"
        ) from error
    if not isinstance(metadata, dict):
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework metadata is not a dictionary: {info_plist}"
        )
    libraries = metadata.get("AvailableLibraries")
    if not isinstance(libraries, list):
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework metadata has no AvailableLibraries: {info_plist}"
        )

    required_architecture = _XCODE_ARCHITECTURES[architecture]
    for library in libraries:
        if not isinstance(library, dict):
            continue
        if library.get("SupportedPlatform") != "ios":
            continue
        declared_variant = library.get("SupportedPlatformVariant")
        if platform_variant == "simulator":
            if declared_variant != "simulator":
                continue
        elif declared_variant not in (None, "", "device"):
            continue
        supported_architectures = library.get("SupportedArchitectures")
        if not isinstance(supported_architectures, list):
            continue
        if required_architecture not in supported_architectures:
            continue
        library_identifier = library.get("LibraryIdentifier")
        library_path = library.get("LibraryPath")
        if (
            not isinstance(library_identifier, str)
            or not library_identifier
            or not isinstance(library_path, str)
            or not library_path
        ):
            continue
        # xcodebuild records LibraryPath relative to the per-slice
        # LibraryIdentifier directory. Keep a root-relative fallback for
        # older hand-produced archives, but validate both paths strictly.
        _validate_relative_framework_component(library_identifier, "LibraryIdentifier")
        _validate_relative_framework_component(library_path, "LibraryPath")
        candidates = (
            framework / library_identifier / library_path,
            framework / library_path,
        )
        for slice_path in candidates:
            slice_path = _resolve_framework_path(
                slice_path, framework, "XCFramework library slice", allow_missing=True
            )
            if not slice_path.is_dir() or slice_path.suffix != ".framework":
                continue
            binary = _resolve_framework_path(
                slice_path / slice_path.stem,
                slice_path,
                "XCFramework framework binary",
                allow_missing=True,
            )
            if not binary.is_file():
                continue
            _validate_runtime_binary(
                binary,
                required_architecture,
                runner=runner,
                timeout_seconds=timeout_seconds,
            )
            return framework

    target = "iOS Simulator" if platform_variant == "simulator" else "physical iOS"
    raise IOSRuntimeFrameworkError(
        f"iOS runtime XCFramework has no usable {target} slice for "
        f"{required_architecture}: {framework}"
    )


def _resolve_framework_path(
    path: Path,
    root: Path,
    description: str,
    *,
    allow_missing: bool = False,
) -> Path:
    """Resolve an archive member and require it to stay below ``root``."""
    try:
        resolved = path.resolve(strict=not allow_missing)
    except (OSError, RuntimeError, ValueError) as error:
        raise IOSRuntimeFrameworkError(
            f"{description} could not be resolved: {path}: {error}"
        ) from error
    try:
        resolved.relative_to(root)
    except ValueError as error:
        raise IOSRuntimeFrameworkError(
            f"{description} escapes the supplied XCFramework: {path}"
        ) from error
    return resolved


def _validate_relative_framework_component(value: str, field: str) -> None:
    """Reject metadata components that can redirect outside their slice."""
    component = Path(value)
    if (
        component.is_absolute()
        or "\\" in value
        or any(part in {"", ".", ".."} for part in component.parts)
    ):
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework {field} is not a safe relative path: {value!r}"
        )


def _validate_runtime_binary(
    binary: Path,
    required_architecture: str,
    *,
    runner: RuntimeFrameworkCommandRunner | None,
    timeout_seconds: float,
) -> None:
    """Require the real framework binary to contain the requested Mach-O arch."""
    command = ["xcrun", "lipo", "-archs", str(binary)]
    command_runner = runner or _SubprocessCommandRunner()
    try:
        result = command_runner.run(command, timeout_seconds=timeout_seconds)
    except IOSRuntimeFrameworkError as error:
        raise IOSRuntimeFrameworkError(
            f"iOS runtime XCFramework binary architecture probe failed for "
            f"{binary}: {error}"
        ) from error
    if result.returncode:
        detail = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
        raise IOSRuntimeFrameworkError(
            "iOS runtime XCFramework binary is not a readable Mach-O file "
            f"({binary}, exit code {result.returncode})"
            + (f":\n{detail}" if detail else "")
        )
    architectures = set(result.stdout.split())
    if required_architecture not in architectures:
        reported = " ".join(sorted(architectures)) or "none"
        raise IOSRuntimeFrameworkError(
            "iOS runtime XCFramework binary does not contain the required "
            f"{required_architecture} architecture: {binary} "
            f"(reported: {reported})"
        )


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Validate a DobbyVPN iOS runtime XCFramework slice."
    )
    parser.add_argument("target", choices=("ios", "iossimulator"))
    parser.add_argument("runtime_framework", type=Path)
    parser.add_argument(
        "architecture",
        nargs="?",
        choices=tuple(sorted(_SUPPORTED_ARCHITECTURES)),
        help="Simulator architecture; physical iOS is always arm64.",
    )
    args = parser.parse_args(argv)
    if args.target == "ios":
        architecture = args.architecture or "arm64"
        platform_variant = "device"
    else:
        if args.architecture is None:
            parser.error("iossimulator requires arm64 or amd64 architecture")
        architecture = args.architecture
        platform_variant = "simulator"
    try:
        framework = validate_runtime_framework(
            args.runtime_framework,
            architecture,
            platform_variant=platform_variant,
        )
    except IOSRuntimeFrameworkError as error:
        print(f"iOS runtime XCFramework validation failed: {error}", file=sys.stderr)
        return 2
    print(
        "iOS runtime XCFramework validated: "
        f"target={args.target} architecture={_XCODE_ARCHITECTURES[architecture]} "
        f"path={framework}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
