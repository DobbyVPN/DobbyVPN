#!/usr/bin/env python3
"""Exercise the real F-Droid updater and build against an unpublished candidate."""

from __future__ import annotations

import argparse
from contextlib import contextmanager
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import threading
import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from release.version_metadata import parse_version
from fdroid_candidate_metadata import prepare_metadata, finalize_metadata, _load_yaml
from verify_android_apk_source import verify_apk
from verify_android_reproducibility import verify_signed_payload, _validate_metadata

APP_ID = "com.dobby.vpn"


def stage_app_metadata(source_root: Path, destination: Path) -> None:
    """Seed the disposable fdroidserver workspace from this source's baseline."""
    source_metadata = source_root / ".github" / "fdroid" / f"{APP_ID}.yml"
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source_metadata, destination)


def run(command: list[str], cwd: Path, environment: dict[str, str]) -> None:
    print(f"Running: {command!r}", flush=True)
    subprocess.run(command, cwd=cwd, env=environment, check=True)


@contextmanager
def staged_version(work: Path, version_name: str, version_code: int):
    web_root = work / "https"
    web_root.mkdir()
    version = parse_version(version_name)
    if version.android_version_code != version_code:
        raise ValueError("candidate version code does not match VERSION-derived metadata")
    (web_root / "version.txt").write_text(version.update_document(), encoding="utf-8")
    certificate, key = work / "certificate.pem", work / "key.pem"
    run([
        "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
        "-keyout", str(key), "-out", str(certificate), "-days", "1",
        "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1",
    ], work, os.environ.copy())
    ca_bundle = work / "ca-bundle.pem"
    system_ca = ssl.get_default_verify_paths().cafile
    ca_bundle.write_bytes((Path(system_ca).read_bytes() if system_ca else b"") + certificate.read_bytes())
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(SimpleHTTPRequestHandler, directory=str(web_root)))
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(certificate, key)
    server.socket = context.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"https://127.0.0.1:{server.server_port}/version.txt", ca_bundle
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def emit_logs(work: Path) -> None:
    """Forward tool-created diagnostics before removing the disposable workspace."""
    logs = work / "logs"
    if not logs.exists():
        return
    for path in sorted(logs.rglob("*")):
        if path.is_file():
            print(f"F-Droid diagnostic: {path.relative_to(work)}", file=sys.stderr, flush=True)
            with path.open("rb") as stream:
                shutil.copyfileobj(stream, sys.stderr.buffer)
            sys.stderr.buffer.flush()


def preflight(args: argparse.Namespace, work: Path) -> None:
    environment = os.environ.copy()
    environment.update({
        "PATH": f"{args.fdroidserver}:{args.fdroidserver / 'examples'}:{environment.get('PATH', '')}",
        "PYTHONPATH": f"{args.fdroidserver}:{args.fdroidserver / 'examples'}",
        "PYTHONUNBUFFERED": "1",
        "GOTOOLCHAIN": "local",
        "GOPATH": "/home/vagrant/go",
        "NO_PROXY": "127.0.0.1,localhost",
        "no_proxy": "127.0.0.1,localhost",
    })
    environment.pop("CI", None)
    mirror = work / "candidate.git"
    run(["git", "clone", "--bare", "--no-hardlinks", str(args.source_root), str(mirror)], work, environment)
    run(["git", "--git-dir", str(mirror), "update-ref", "refs/heads/fdroid-candidate", args.source_sha], work, environment)
    run(["git", "--git-dir", str(mirror), "symbolic-ref", "HEAD", "refs/heads/fdroid-candidate"], work, environment)
    run(["git", "--git-dir", str(mirror), "update-ref", f"refs/tags/v{args.version_name}", args.source_sha], work, environment)
    metadata = work / "metadata" / f"{APP_ID}.yml"
    baseline = work / "baseline.yml"
    stage_app_metadata(args.source_root, metadata)
    (work / "srclibs").symlink_to(args.fdroiddata / "srclibs", target_is_directory=True)
    (work / "config").symlink_to(args.fdroiddata / "config", target_is_directory=True)
    sdk_path = environment.get("ANDROID_HOME", "/opt/android-sdk")
    (work / "config.yml").write_text(
        yaml.safe_dump({"sdk_path": sdk_path, "lint_licenses": ["BUSL-1.1"]}), encoding="utf-8",
    )
    with staged_version(work, args.version_name, args.version_code) as (version_url, ca_bundle):
        environment["SSL_CERT_FILE"] = str(ca_bundle)
        environment["REQUESTS_CA_BUNDLE"] = str(ca_bundle)
        prepare_metadata(metadata, baseline, mirror, version_url, args.version_name, args.version_code)
        run(["fdroid", "checkupdates", "--auto", "--allow-dirty", "--verbose", APP_ID], work, environment)
        # checkupdates can log an app failure and exit zero. Require its actual
        # appended record, rather than treating process success as an update.
        finalize_metadata(metadata, baseline, work / "build" / APP_ID, args.source_sha, args.version_name, args.version_code)
    run(["fdroid", "rewritemeta", APP_ID], work, environment)
    # Lint the submission URLs, while the updater and build use the isolated
    # mirror and staged HTTPS document. A file:// mirror is not a catalog URL.
    candidate_bytes = metadata.read_bytes()
    submission = _load_yaml(metadata, "candidate")
    live = _load_yaml(baseline, "baseline")
    print(yaml.safe_dump({
        "License": submission["License"],
        "AutoName": submission.get("AutoName"),
        "Builds": submission["Builds"][-1:],
        "AutoUpdateMode": submission["AutoUpdateMode"],
        "UpdateCheckMode": submission["UpdateCheckMode"],
        "CurrentVersion": submission["CurrentVersion"],
        "CurrentVersionCode": submission["CurrentVersionCode"],
    }, sort_keys=False), flush=True)
    submission["Repo"] = live["Repo"]
    submission["UpdateCheckData"] = live["UpdateCheckData"]
    metadata.write_text(yaml.safe_dump(submission, sort_keys=False), encoding="utf-8")
    try:
        run(["fdroid", "rewritemeta", APP_ID], work, environment)
        run(["fdroid", "lint", APP_ID], work, environment)
    finally:
        metadata.write_bytes(candidate_bytes)
    if args.update_only:
        print("F-Droid real HTTP update and candidate recipe validation passed", flush=True)
        return
    (work / "fdroiddata").symlink_to(args.fdroiddata, target_is_directory=True)
    run(["fdroid", "fetchsrclibs", f"{APP_ID}:{args.version_code}", "--verbose"], work, environment)
    (work / "fdroiddata").unlink()
    run([
        "fdroid", "build", "--verbose", "--test", "--refresh-scanner", "--on-server",
        "--no-tarball", f"{APP_ID}:{args.version_code}",
    ], work, environment)
    apk = work / "tmp" / f"{APP_ID}_{args.version_code}.apk"
    run(["fdroid", "scanner", "--exit-code", str(apk)], work, environment)
    verify_apk(args.apkanalyzer, apk, args.source_sha, args.repository, args.version_name, args.version_code)
    # This existing comparison ignores only signing records; it also compares
    # the complete payload of two unsigned APKs before publication.
    verify_signed_payload(args.reference_apk, apk)
    print("F-Droid updater, recipe, build, scanner, APK identity and production payload comparison passed", flush=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("source-root", "fdroiddata", "fdroidserver"):
        parser.add_argument(f"--{name}", type=Path, required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--version-name", required=True)
    parser.add_argument("--version-code", type=int, required=True)
    parser.add_argument("--repository", default="DobbyVPN/DobbyVPN")
    parser.add_argument("--reference-apk", type=Path)
    parser.add_argument("--apkanalyzer", default="apkanalyzer")
    parser.add_argument("--update-only", action="store_true")
    parser.add_argument("--work-root", type=Path)
    args = parser.parse_args()
    _validate_metadata(args.source_sha, args.version_name, args.version_code)
    for name in ("source_root", "fdroiddata", "fdroidserver", "reference_apk"):
        path = getattr(args, name)
        if path is not None:
            setattr(args, name, path.resolve(strict=True))
    if not args.update_only and args.reference_apk is None:
        parser.error("--reference-apk is required for the F-Droid build and payload comparison")
    def check(work: Path) -> None:
        try:
            preflight(args, work)
        finally:
            primary_error = sys.exc_info()[1]
            try:
                emit_logs(work)
            except OSError as error:
                print(f"F-Droid diagnostic collection failed: {error}", file=sys.stderr)
                if primary_error is None:
                    raise
    if args.work_root is not None:
        # The container owns disposal. Matching the production APK's build paths keeps
        # the F-Droid source-built Go toolchain and native payload reproducible.
        work = args.work_root.resolve()
        work.mkdir(parents=True, exist_ok=True)
        for name in ("candidate.git", "metadata", "baseline.yml", "config.yml", "config", "srclibs", "https", "build"):
            if (work / name).exists():
                parser.error(f"F-Droid workspace is not fresh: {work / name}")
        check(work)
    else:
        with tempfile.TemporaryDirectory(prefix="dobby-fdroid-candidate-") as temporary:
            check(Path(temporary))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
