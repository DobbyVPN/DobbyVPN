"""Build test-only OS accessibility helpers during guest preparation."""
from pathlib import Path


def prepare_helper(platform: str, run_dir: Path, logs: Path, timeout: float) -> Path:
    from ..local_vm import _run_logged

    source = run_dir / "source" / "torturer" / "native_ui"
    output = run_dir / "work" / "native-ui-helper"
    output.mkdir(parents=True, exist_ok=True)
    if platform == "macos":
        helper = output / "native-ui"
        command = ["xcrun", "swiftc", str(source / "macos.swift"), "-o", str(helper)]
    elif platform == "windows":
        helper = output / "NativeUI.exe"
        command = [
            "dotnet", "publish", str(source / "windows" / "NativeUI.csproj"),
            "-c", "Release", "-r", "win-x64", "--self-contained", "true",
            "-o", str(output),
        ]
    else:
        raise ValueError(f"unsupported native UI platform: {platform}")
    _run_logged(command, cwd=source, logs=logs, label="native-ui-helper-build", timeout=timeout)
    if not helper.is_file():
        raise RuntimeError(f"native UI helper build did not produce {helper}")
    if platform == "windows":
        _run_logged([str(helper), "--diagnostics-test"], cwd=source, logs=logs,
                    label="native-diagnostics-tests", timeout=timeout)
    return helper
