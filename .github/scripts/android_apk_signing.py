#!/usr/bin/env python3
"""Sign Android APKs for qualification or publication.

The qualification key is generated for one invocation, used to sign the app
and instrumentation companion with the same identity, and deleted before the
command returns.  Publication reads the protected keystore from environment
variables and writes only the signed application APK.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
from typing import Sequence

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))
from verify_android_reproducibility import (  # noqa: E402
    verify_document,
    verify_signed_payload,
)

CERTIFICATE_SHA256 = re.compile(r"^[0-9a-fA-F]{64}$")
SIGNER_LINE = re.compile(
    rb"^Signer #[0-9]+ certificate SHA-256 digest: ([0-9a-fA-F:]+)$",
    re.MULTILINE,
)


class SigningError(ValueError):
    """An Android signing or certificate verification step failed."""


def find_android_tool(name: str, environment_name: str) -> Path:
    """Resolve the pinned Android signer or the selected JDK keytool."""
    if name == "apksigner":
        sdk_root = os.environ.get("ANDROID_SDK_ROOT") or os.environ.get("ANDROID_HOME")
        if sdk_root:
            candidate = Path(sdk_root) / "build-tools" / "36.0.0" / "apksigner"
            if candidate.is_file() and os.access(candidate, os.X_OK):
                return candidate.resolve()
        raise SigningError("pinned Android apksigner 36.0.0 is unavailable")

    configured = os.environ.get(environment_name)
    if configured:
        resolved = shutil.which(configured)
        candidate = Path(resolved) if resolved else Path(configured)
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return candidate.resolve()
        raise SigningError(f"{name} is unavailable")
    resolved = shutil.which(name)
    if resolved:
        return Path(resolved).resolve()
    java_home = os.environ.get("JAVA_HOME")
    if java_home:
        candidate = Path(java_home) / "bin" / name
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return candidate.resolve()
    raise SigningError(f"{name} is unavailable")


def _emit(stream, value: bytes) -> None:
    if value:
        stream.buffer.write(value)
        stream.buffer.flush()


def _run(command: Sequence[str], *, environment: dict[str, str], label: str) -> bytes:
    try:
        completed = subprocess.run(
            list(command),
            env=environment,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except OSError as error:
        raise SigningError(f"{label} could not start: {error}") from error
    _emit(sys.stdout, completed.stdout)
    _emit(sys.stderr, completed.stderr)
    if completed.returncode != 0:
        raise SigningError(f"{label} failed with exit code {completed.returncode}")
    return completed.stdout + completed.stderr


def _signer_digest(apksigner: Path, apk: Path, environment: dict[str, str]) -> str:
    output = _run(
        [str(apksigner), "verify", "--print-certs", str(apk)],
        environment=environment,
        label=f"APK signature verification for {apk.name}",
    )
    digests = [
        match.group(1).replace(b":", b"").decode("ascii").lower()
        for match in SIGNER_LINE.finditer(output)
    ]
    if len(digests) != 1 or not CERTIFICATE_SHA256.fullmatch(digests[0]):
        raise SigningError(f"{apk.name} must have exactly one SHA-256 signer certificate")
    return digests[0]


def _sign(
    apksigner: Path,
    keystore: Path,
    alias: str,
    store_password_env: str,
    key_password_env: str,
    unsigned: Path,
    signed: Path,
    environment: dict[str, str],
) -> None:
    if not unsigned.is_file():
        raise SigningError(f"unsigned APK is missing: {unsigned}")
    if unsigned.resolve() == signed.resolve():
        raise SigningError("signed APK output must differ from unsigned APK input")
    signed.parent.mkdir(parents=True, exist_ok=True)
    _run(
        [
            str(apksigner), "sign",
            "--ks", str(keystore),
            "--ks-key-alias", alias,
            "--ks-pass", f"env:{store_password_env}",
            "--key-pass", f"env:{key_password_env}",
            "--out", str(signed),
            str(unsigned),
        ],
        environment=environment,
        label=f"APK signing for {unsigned.name}",
    )
    if not signed.is_file():
        raise SigningError(f"APK signer did not create its output: {signed}")
    verify_signed_payload(unsigned, signed)
    print(f"Signed APK payload verified: {signed.name}")


def sign_test_pair(
    unsigned_app: Path,
    unsigned_companion: Path,
    signed_app: Path,
    signed_companion: Path,
    *,
    apksigner: Path,
    keytool: Path,
) -> str:
    """Create a temporary signer, sign both APKs, and verify their payloads."""
    if not keytool.is_file() or not apksigner.is_file():
        raise SigningError("keytool and the pinned apksigner must be executable files")
    output_paths = (signed_app.resolve(), signed_companion.resolve())
    if output_paths[0] == output_paths[1]:
        raise SigningError("application and companion outputs must be different files")

    password = secrets.token_urlsafe(32)
    environment = os.environ.copy()
    store_password_env = "DOBBYVPN_TEMP_ANDROID_KEYSTORE_PASSWORD"
    key_password_env = "DOBBYVPN_TEMP_ANDROID_KEY_PASSWORD"
    environment[store_password_env] = password
    environment[key_password_env] = password
    try:
        with tempfile.TemporaryDirectory(prefix="dobbyvpn-android-test-key-") as temporary:
            keystore = Path(temporary) / "qualification.jks"
            _run(
                [
                    str(keytool), "-genkeypair", "-noprompt", "-storetype", "JKS",
                    "-keystore", str(keystore), "-alias", "dobbyvpn-qualification",
                    "-keyalg", "RSA", "-keysize", "2048", "-validity", "1",
                    "-dname", "CN=DobbyVPN temporary Android qualification",
                    "-storepass:env", store_password_env,
                    "-keypass:env", key_password_env,
                ],
                environment=environment,
                label="temporary Android qualification key generation",
            )
            _sign(
                apksigner, keystore, "dobbyvpn-qualification",
                store_password_env, key_password_env,
                unsigned_app, signed_app, environment,
            )
            _sign(
                apksigner, keystore, "dobbyvpn-qualification",
                store_password_env, key_password_env,
                unsigned_companion, signed_companion, environment,
            )
            app_digest = _signer_digest(apksigner, signed_app, environment)
            companion_digest = _signer_digest(apksigner, signed_companion, environment)
            if app_digest != companion_digest:
                raise SigningError("application and test companion signer certificates differ")
            return app_digest
    finally:
        environment.pop(store_password_env, None)
        environment.pop(key_password_env, None)
        password = ""


def sign_release_apk(
    unsigned_apk: Path,
    signed_apk: Path,
    *,
    apksigner: Path,
    keystore_base64_env: str,
    alias_env: str,
    store_password_env: str,
    key_password_env: str,
    expected_certificate_sha256: str,
) -> str:
    """Sign one retained Release APK with the production Android identity."""
    expected = expected_certificate_sha256.replace(":", "").lower()
    if not CERTIFICATE_SHA256.fullmatch(expected):
        raise SigningError("expected signer certificate must be a SHA-256 digest")
    if not apksigner.is_file():
        raise SigningError("pinned Android apksigner is unavailable")
    for variable in (keystore_base64_env, alias_env, store_password_env, key_password_env):
        if not os.environ.get(variable):
            raise SigningError(f"required signing environment variable is missing: {variable}")
    try:
        keystore_bytes = base64.b64decode(os.environ[keystore_base64_env], validate=True)
    except (binascii.Error, ValueError) as error:
        raise SigningError("production Android keystore is not valid base64") from error
    if not keystore_bytes:
        raise SigningError("production Android keystore is empty")

    environment = os.environ.copy()
    try:
        with tempfile.TemporaryDirectory(prefix="dobbyvpn-android-release-key-") as temporary:
            keystore = Path(temporary) / "release.jks"
            keystore.write_bytes(keystore_bytes)
            keystore.chmod(0o600)
            _sign(
                apksigner,
                keystore,
                os.environ[alias_env],
                store_password_env,
                key_password_env,
                unsigned_apk,
                signed_apk,
                environment,
            )
            digest = _signer_digest(apksigner, signed_apk, environment)
            if digest != expected:
                raise SigningError("signed APK certificate does not match the expected release signer")
            return digest
    finally:
        keystore_bytes = b""


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _artifact_record(kind: str, path: Path) -> dict[str, str]:
    if not path.is_file():
        raise SigningError(f"APK artifact is missing: {path}")
    return {"kind": kind, "name": path.name, "sha256": _sha256(path)}


def finalize_provenance(
    provenance: Path,
    unsigned_apk: Path,
    signed_apk: Path,
    test_companion: Path,
    *,
    source_sha: str,
    version_name: str,
    version_code: int,
    expected_certificate_sha256: str,
    apksigner: Path,
) -> str:
    """Validate Release build records, bind the signed APK, and write final provenance."""
    expected = expected_certificate_sha256.replace(":", "").lower()
    if not CERTIFICATE_SHA256.fullmatch(expected):
        raise SigningError("expected signer certificate must be a SHA-256 digest")
    if not apksigner.is_file():
        raise SigningError("pinned Android apksigner is unavailable")
    if not provenance.is_file():
        raise SigningError(f"Android provenance is missing: {provenance}")
    try:
        document = json.loads(provenance.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise SigningError("Android build provenance is not valid JSON") from error
    if not isinstance(document, dict):
        raise SigningError("Android build provenance must be a JSON object")
    if (
        document.get("schema") != 1
        or document.get("source_sha") != source_sha
        or document.get("version_name") != version_name
        or document.get("version_code") != version_code
        or document.get("application_id") != "com.dobby.vpn"
    ):
        raise SigningError("Android build provenance metadata does not match the selected Release")

    verify_document(
        document.get("reproducibility"),
        unsigned_apk,
        source_sha,
        version_name,
        version_code,
    )
    if not test_companion.is_file():
        raise SigningError("unsigned Android test companion is missing")
    companion_sha = _sha256(test_companion)
    companion = document.get("test_companion")
    if (
        not isinstance(companion, dict)
        or companion.get("name") != test_companion.name
        or companion.get("sha256") != companion_sha
        or companion.get("bytes") != test_companion.stat().st_size
        or companion.get("application_id") != "com.dobby.vpn.test"
        or companion.get("source_sha") != source_sha
    ):
        raise SigningError("unsigned Android test companion does not match build provenance")

    expected_artifacts = {
        "unsigned": _artifact_record("unsigned", unsigned_apk),
        "test_companion_unsigned": _artifact_record("test_companion_unsigned", test_companion),
    }
    artifacts = document.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != len(expected_artifacts):
        raise SigningError("Android build provenance artifact set mismatch")
    by_kind = {
        artifact.get("kind"): artifact
        for artifact in artifacts
        if isinstance(artifact, dict)
    }
    if set(by_kind) != set(expected_artifacts):
        raise SigningError("Android build provenance artifact kinds mismatch")
    for kind, expected_record in expected_artifacts.items():
        record = by_kind[kind]
        if any(record.get(key) != value for key, value in expected_record.items()):
            raise SigningError(f"Android build provenance {kind} artifact does not match its APK")

    driver = document.get("build_driver")
    if not isinstance(driver, dict) or any(
        driver.get(key) != value
        for key, value in {
            "source_sha": source_sha,
            "version_name": version_name,
            "version_code": version_code,
            "package": "com.dobby.vpn",
            "signing_classification": "unsigned",
            "signer_certificate_sha256": None,
        }.items()
    ):
        raise SigningError("Android build-driver metadata does not match the selected Release")
    driver_artifact = driver.get("artifact")
    driver_companion = driver.get("test_companion")
    for actual, expected_record, label in (
        (driver_artifact, expected_artifacts["unsigned"], "application"),
        (driver_companion, expected_artifacts["test_companion_unsigned"], "test companion"),
    ):
        if (
            not isinstance(actual, dict)
            or actual.get("name") != expected_record["name"]
            or actual.get("sha256") != expected_record["sha256"]
            or actual.get("bytes")
            != (unsigned_apk if label == "application" else test_companion).stat().st_size
        ):
            raise SigningError(f"Android build-driver {label} does not match the selected APK")
    if driver_companion.get("signing_classification") != "unsigned":
        raise SigningError("Android build-driver test companion is not recorded as unsigned")

    verify_signed_payload(unsigned_apk, signed_apk)
    signer_digest = _signer_digest(apksigner, signed_apk, os.environ.copy())
    if signer_digest != expected:
        raise SigningError("signed APK certificate does not match the expected release signer")

    document["signer_certificate_sha256"] = signer_digest
    document["signed_payload_matches_unsigned"] = True
    document["artifacts"] = [
        _artifact_record("signed", signed_apk),
        _artifact_record("unsigned", unsigned_apk),
    ]
    temporary = provenance.with_name(f".{provenance.name}.tmp")
    try:
        temporary.write_text(
            json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )
        temporary.replace(provenance)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise
    return signer_digest


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    test = commands.add_parser("sign-test")
    test.add_argument("--unsigned-app", type=Path, required=True)
    test.add_argument("--unsigned-companion", type=Path, required=True)
    test.add_argument("--signed-app", type=Path, required=True)
    test.add_argument("--signed-companion", type=Path, required=True)
    test.add_argument("--apksigner", type=Path, required=True)
    test.add_argument("--keytool", type=Path, required=True)
    release = commands.add_parser("sign-release")
    release.add_argument("--unsigned-apk", type=Path, required=True)
    release.add_argument("--signed-apk", type=Path, required=True)
    release.add_argument("--apksigner", type=Path, required=True)
    release.add_argument("--keystore-base64-env", default="KEYSTORE_FILE")
    release.add_argument("--alias-env", default="KEY_ALIAS")
    release.add_argument("--store-password-env", default="KEYSTORE_PASSWORD")
    release.add_argument("--key-password-env", default="KEY_PASSWORD")
    release.add_argument(
        "--expected-certificate-sha256",
        default=os.environ.get("EXPECTED_ANDROID_SIGNER_SHA256", ""),
    )
    finalize = commands.add_parser("finalize-provenance")
    finalize.add_argument("--provenance", type=Path, required=True)
    finalize.add_argument("--unsigned-apk", type=Path, required=True)
    finalize.add_argument("--signed-apk", type=Path, required=True)
    finalize.add_argument("--test-companion", type=Path, required=True)
    finalize.add_argument("--source-sha", required=True)
    finalize.add_argument("--version-name", required=True)
    finalize.add_argument("--version-code", type=int, required=True)
    finalize.add_argument("--apksigner", type=Path, required=True)
    finalize.add_argument(
        "--expected-certificate-sha256",
        default=os.environ.get("EXPECTED_ANDROID_SIGNER_SHA256", ""),
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "sign-test":
            digest = sign_test_pair(
                args.unsigned_app,
                args.unsigned_companion,
                args.signed_app,
                args.signed_companion,
                apksigner=args.apksigner,
                keytool=args.keytool,
            )
            print(f"Temporary Android test signer SHA-256: {digest}")
        elif args.command == "sign-release":
            digest = sign_release_apk(
                args.unsigned_apk,
                args.signed_apk,
                apksigner=args.apksigner,
                keystore_base64_env=args.keystore_base64_env,
                alias_env=args.alias_env,
                store_password_env=args.store_password_env,
                key_password_env=args.key_password_env,
                expected_certificate_sha256=args.expected_certificate_sha256,
            )
            print(f"Android release signer SHA-256: {digest}")
        else:
            digest = finalize_provenance(
                args.provenance,
                args.unsigned_apk,
                args.signed_apk,
                args.test_companion,
                source_sha=args.source_sha,
                version_name=args.version_name,
                version_code=args.version_code,
                expected_certificate_sha256=args.expected_certificate_sha256,
                apksigner=args.apksigner,
            )
            print(f"Android publication provenance finalized; signer SHA-256: {digest}")
        return 0
    except (OSError, SigningError, ValueError) as error:
        print(f"Android APK signing failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
