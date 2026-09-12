#!/usr/bin/env python3
"""Create and verify strict Android reproducibility evidence."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
import zipfile

from android_dependency_provenance import GO_SOURCE_COMMIT, GO_VERSION


SCHEMA = 1
KIND = "dobbyvpn_android_reproducibility"
SHA256 = re.compile(r"^[0-9a-f]{64}$")
SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION_NAME = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
NATIVE_PATHS = (
    "lib/arm64-v8a/libgojni.so",
    "lib/x86_64/libgojni.so",
)
TOOLCHAIN = {
    "android_build_tools": "36.0.0",
    "android_ndk": "27.3.13750724",
    "go": f"go{GO_VERSION}",
    "go_source_commit": GO_SOURCE_COMMIT,
    "gomobile": "golang.org/x/mobile@v0.0.0-20260520154334-0e4426e1883d",
    "gradle": "8.13",
    "java": "17",
}
BUILD_ENVIRONMENT = {
    "go_flags": "-trimpath -buildvcs=false",
    "go_root": "/home/vagrant/build/srclib/go",
    "gopath": "/home/vagrant/go",
    "gradle_flags": "--no-build-cache --no-daemon --rerun-tasks",
    "gomobile_cache_isolation": "fresh_per_build",
    "source_root": "/home/vagrant/build/com.dobby.vpn",
}


class VerificationError(ValueError):
    pass


def _regular_file(path: Path, label: str) -> None:
    if not path.is_file():
        raise VerificationError(f"{label} does not exist")


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _native_records(apk: Path) -> list[dict[str, object]]:
    try:
        with zipfile.ZipFile(apk) as archive:
            names = [item.filename for item in archive.infolist()]
            records: list[dict[str, object]] = []
            for name in NATIVE_PATHS:
                if names.count(name) != 1:
                    raise VerificationError(f"APK must contain exactly one {name}")
                payload = archive.read(name)
                if not payload:
                    raise VerificationError(f"APK native library is empty: {name}")
                records.append(
                    {
                        "bytes": len(payload),
                        "path": name,
                        "sha256": hashlib.sha256(payload).hexdigest(),
                    }
                )
            return records
    except zipfile.BadZipFile as exc:
        raise VerificationError("APK is not a valid ZIP archive") from exc


def _is_v1_signature_member(name: str) -> bool:
    upper = name.upper()
    if upper == "META-INF/MANIFEST.MF":
        return True
    if not upper.startswith("META-INF/"):
        return False
    relative = upper.removeprefix("META-INF/")
    return "/" not in relative and relative.endswith((".SF", ".RSA", ".DSA", ".EC"))


def _logical_payload_records(apk: Path) -> list[dict[str, object]]:
    try:
        with zipfile.ZipFile(apk) as archive:
            records: list[dict[str, object]] = []
            for item in archive.infolist():
                if _is_v1_signature_member(item.filename):
                    continue
                payload = archive.read(item)
                records.append(
                    {
                        "bytes": len(payload),
                        "path": item.filename,
                        "sha256": hashlib.sha256(payload).hexdigest(),
                    }
                )
            return sorted(records, key=lambda record: str(record["path"]))
    except zipfile.BadZipFile as exc:
        raise VerificationError("APK is not a valid ZIP archive") from exc


def verify_signed_payload(unsigned_apk: Path, signed_apk: Path) -> None:
    _regular_file(unsigned_apk, "unsigned APK")
    _regular_file(signed_apk, "signed APK")
    if _logical_payload_records(unsigned_apk) != _logical_payload_records(signed_apk):
        raise VerificationError("signed APK payload differs from the verified unsigned APK")


def verify_publication_provenance(
    provenance_path: Path,
    unsigned_apk: Path,
    signed_apk: Path,
    source_sha: str,
    version_name: str,
    version_code: int,
    signer_certificate_sha256: str,
) -> None:
    """Verify the shared Android publication manifest and its two APKs."""
    _regular_file(provenance_path, "Android provenance")
    try:
        document = json.loads(provenance_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise VerificationError("Android provenance is not valid JSON") from error
    if not isinstance(document, dict):
        raise VerificationError("Android provenance must be a JSON object")
    if document.get("schema") != 1:
        raise VerificationError("Android provenance schema mismatch")
    if document.get("source_sha") != source_sha:
        raise VerificationError("Android provenance source mismatch")
    if document.get("version_name") != version_name:
        raise VerificationError("Android provenance version mismatch")
    if document.get("version_code") != version_code:
        raise VerificationError("Android provenance version code mismatch")
    if document.get("application_id") != "com.dobby.vpn":
        raise VerificationError("Android provenance application ID mismatch")
    if document.get("signer_certificate_sha256") != signer_certificate_sha256:
        raise VerificationError("Android signer certificate mismatch")
    if document.get("signed_payload_matches_unsigned") is not True:
        raise VerificationError("Android signed-payload binding is missing")

    verify_document(
        document.get("reproducibility"),
        unsigned_apk,
        source_sha,
        version_name,
        version_code,
    )
    verify_signed_payload(unsigned_apk, signed_apk)

    expected = {"signed": signed_apk, "unsigned": unsigned_apk}
    artifacts = document.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != len(expected):
        raise VerificationError("Android provenance artifact set mismatch")
    by_kind = {item.get("kind"): item for item in artifacts if isinstance(item, dict)}
    if set(by_kind) != set(expected):
        raise VerificationError("Android provenance artifact kinds mismatch")
    for kind, path in expected.items():
        item = by_kind[kind]
        if item.get("name") != path.name:
            raise VerificationError(f"Android provenance {kind} filename mismatch")
        if item.get("sha256") != _sha256(path):
            raise VerificationError(f"Android provenance {kind} digest mismatch")


def _validate_metadata(source_sha: str, version_name: str, version_code: int) -> None:
    if not SOURCE_SHA.fullmatch(source_sha):
        raise VerificationError("source SHA must be a lowercase 40-character Git SHA")
    if not VERSION_NAME.fullmatch(version_name):
        raise VerificationError("version name must use X.Y.Z format")
    if version_code < 1 or version_code > 2_100_000_000:
        raise VerificationError("version code is outside the accepted Android range")


def create_document(
    first_apk: Path,
    second_apk: Path,
    source_sha: str,
    version_name: str,
    version_code: int,
) -> dict[str, object]:
    _validate_metadata(source_sha, version_name, version_code)
    _regular_file(first_apk, "first APK")
    _regular_file(second_apk, "second APK")
    first_digest = _sha256(first_apk)
    second_digest = _sha256(second_apk)
    if first_digest != second_digest:
        raise VerificationError("independent unsigned APK builds are not byte-identical")
    first_native = _native_records(first_apk)
    second_native = _native_records(second_apk)
    if first_native != second_native:
        raise VerificationError("native APK payloads differ between independent builds")
    size = first_apk.stat().st_size
    return {
        "build_environment": dict(BUILD_ENVIRONMENT),
        "builds": [
            {"bytes": size, "id": "first", "sha256": first_digest},
            {"bytes": size, "id": "second", "sha256": second_digest},
        ],
        "identical": True,
        "kind": KIND,
        "native_libraries": first_native,
        "schema": SCHEMA,
        "source_sha": source_sha,
        "toolchain": dict(TOOLCHAIN),
        "version_code": version_code,
        "version_name": version_name,
    }


def verify_document(
    document: object,
    apk: Path,
    source_sha: str,
    version_name: str,
    version_code: int,
) -> None:
    _validate_metadata(source_sha, version_name, version_code)
    _regular_file(apk, "unsigned APK")
    if not isinstance(document, dict):
        raise VerificationError("reproducibility evidence must be a JSON object")
    expected_keys = {
        "build_environment",
        "builds",
        "identical",
        "kind",
        "native_libraries",
        "schema",
        "source_sha",
        "toolchain",
        "version_code",
        "version_name",
    }
    if set(document) != expected_keys:
        raise VerificationError("reproducibility evidence fields mismatch")
    if document["schema"] != SCHEMA or document["kind"] != KIND:
        raise VerificationError("reproducibility evidence schema or kind mismatch")
    if document["source_sha"] != source_sha:
        raise VerificationError("reproducibility source SHA mismatch")
    if document["version_name"] != version_name or document["version_code"] != version_code:
        raise VerificationError("reproducibility version mismatch")
    if document["toolchain"] != TOOLCHAIN:
        raise VerificationError("reproducibility toolchain mismatch")
    if document["build_environment"] != BUILD_ENVIRONMENT:
        raise VerificationError("reproducibility build environment mismatch")
    if document["identical"] is not True:
        raise VerificationError("reproducibility evidence does not assert exact equality")

    digest = _sha256(apk)
    size = apk.stat().st_size
    builds = document["builds"]
    if (
        not isinstance(builds, list)
        or len(builds) != 2
        or not all(isinstance(record, dict) for record in builds)
    ):
        raise VerificationError("reproducibility build record set mismatch")
    expected_builds = [
        {"bytes": size, "id": "first", "sha256": digest},
        {"bytes": size, "id": "second", "sha256": digest},
    ]
    if sorted(builds, key=lambda record: str(record.get("id"))) != expected_builds:
        raise VerificationError("reproducibility APK digest or size mismatch")
    if not SHA256.fullmatch(digest):
        raise VerificationError("reproducibility APK digest is invalid")
    native_libraries = document["native_libraries"]
    expected_native_libraries = _native_records(apk)
    if (
        not isinstance(native_libraries, list)
        or not all(isinstance(record, dict) for record in native_libraries)
        or sorted(native_libraries, key=lambda record: str(record.get("path")))
        != expected_native_libraries
    ):
        raise VerificationError("reproducibility native-library records mismatch")


def _write_json(path: Path, document: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create")
    create.add_argument("--first-apk", type=Path, required=True)
    create.add_argument("--second-apk", type=Path, required=True)
    create.add_argument("--output", type=Path, required=True)
    verify = commands.add_parser("verify-provenance")
    verify.add_argument("--apk", type=Path, required=True)
    verify.add_argument("--provenance", type=Path, required=True)
    signed = commands.add_parser("verify-signed-payload")
    signed.add_argument("--unsigned-apk", type=Path, required=True)
    signed.add_argument("--signed-apk", type=Path, required=True)
    publication = commands.add_parser("verify-publication")
    publication.add_argument("--unsigned-apk", type=Path, required=True)
    publication.add_argument("--signed-apk", type=Path, required=True)
    publication.add_argument("--provenance", type=Path, required=True)
    publication.add_argument("--signer-certificate-sha256", required=True)
    for command in (create, verify, publication):
        command.add_argument("--source-sha", required=True)
        command.add_argument("--version-name", required=True)
        command.add_argument("--version-code", type=int, required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "create":
            document = create_document(
                args.first_apk,
                args.second_apk,
                args.source_sha,
                args.version_name,
                args.version_code,
            )
            _write_json(args.output, document)
            print(f"Android unsigned APK reproducibility verified: {document['builds'][0]['sha256']}")
        elif args.command == "verify-provenance":
            _regular_file(args.provenance, "Android provenance")
            provenance = json.loads(args.provenance.read_text(encoding="utf-8"))
            if not isinstance(provenance, dict) or "reproducibility" not in provenance:
                raise VerificationError("Android provenance lacks reproducibility evidence")
            verify_document(
                provenance["reproducibility"],
                args.apk,
                args.source_sha,
                args.version_name,
                args.version_code,
            )
            print("Android reproducibility provenance validation passed")
        elif args.command == "verify-publication":
            verify_publication_provenance(
                args.provenance,
                args.unsigned_apk,
                args.signed_apk,
                args.source_sha,
                args.version_name,
                args.version_code,
                args.signer_certificate_sha256,
            )
            print("Android publication provenance validation passed")
        else:
            verify_signed_payload(args.unsigned_apk, args.signed_apk)
            print("Signed APK payload matches the verified unsigned APK")
        return 0
    except (OSError, json.JSONDecodeError, VerificationError) as exc:
        print(f"Android reproducibility verification failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
