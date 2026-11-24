"""Install or stage the Go mobile commands pinned by the current core module."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


MODULE = "golang.org/x/mobile"
TOOLS = ("gomobile", "gobind")


def go_json(*arguments: str) -> dict:
    command = ["go", *arguments]
    print("$ " + " ".join(command), flush=True)
    result = subprocess.run(command, stdout=subprocess.PIPE)
    sys.stdout.buffer.write(result.stdout)
    sys.stdout.buffer.flush()
    result.check_returncode()
    return json.loads(result.stdout)


def pinned_version() -> str:
    manifest = go_json("mod", "edit", "-json", "go.mod")
    requirements = manifest.get("Require") or []
    versions = [item["Version"] for item in requirements if item["Path"] == MODULE]
    if len(versions) != 1:
        raise ValueError(f"go.mod must require exactly one version of {MODULE}")
    version = versions[0]
    resolved = go_json("list", "-mod=readonly", "-m", "-json", MODULE)
    if resolved.get("Version") != version or resolved.get("Replace"):
        raise ValueError(f"go.mod requires {MODULE}@{version}, resolved module: {resolved}")
    print(f"Go mobile tools: {MODULE}@{version}", flush=True)
    return version


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    commands.add_parser("install")
    stage = commands.add_parser("stage")
    stage.add_argument("directory", type=Path)
    for name in TOOLS:
        stage.add_argument(name, type=Path)
    args = parser.parse_args()
    version = pinned_version()
    if args.action == "install":
        fetch = Path(__file__).resolve().parents[1] / "dependency_fetch.py"
        packages = [f"{MODULE}/cmd/{name}@{version}" for name in TOOLS]
        subprocess.run([sys.executable, str(fetch), "retry", "--", "go", "install", *packages], check=True)
        return

    # gomobile discovers gobind by name on PATH. Give it the verified pair
    # even when overrides have different directories or executable names.
    args.directory.mkdir()
    for name in TOOLS:
        binary = getattr(args, name).resolve()
        if not binary.is_file() or not os.access(binary, os.X_OK):
            raise ValueError(f"missing executable {name} at {binary}, expected {MODULE}@{version}")
        # Verify the private copy so a later install cannot replace it mid-build.
        staged = args.directory / name
        shutil.copy(binary, staged)
        info = go_json("version", "-m", "-json", str(staged))
        module = info.get("Main") or {}
        if (
            info.get("Path") != f"{MODULE}/cmd/{name}" or module.get("Path") != MODULE
            or module.get("Version") != version or module.get("Replace")
        ):
            raise ValueError(f"{binary} must be built from {MODULE}/cmd/{name}@{version}")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        print(error, file=sys.stderr)
        raise SystemExit(error.returncode)
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(2)
