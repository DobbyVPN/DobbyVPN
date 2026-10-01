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

from android_dependency_provenance import (
    ANDROID_BUILD_TOOLS,
    ANDROID_NDK,
    GO_SOURCE_COMMIT,
    GO_VERSION,
    GRADLE_VERSION,
    JAVA_MAJOR,
)


SCHEMA = 1
KIND = "dobbyvpn_android_reproducibility"
SHA256 = re.compile(r"^[0-9a-f]{64}$")
SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION_NAME = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
NATIVE_PATHS = (
    "lib/arm64-v8a/libdobby_vpn.so",
    "lib/x86_64/libdobby_vpn.so",
)
TOOLCHAIN = {
    "android_build_tools": ANDROID_BUILD_TOOLS,
    "android_ndk": ANDROID_NDK,
    "go": f"go{GO_VERSION}",
    "go_source_commit": GO_SOURCE_COMMIT,
    "kotlin": "2.2.0",
    "compose_bom": "androidx.compose:compose-bom:2025.12.00",
    "gradle": GRADLE_VERSION,
    "java": str(JAVA_MAJOR),
}
BUILD_ENVIRONMENT = {
    "go_flags": "-trimpath -buildvcs=false",
    "go_root": "/home/vagrant/build/srclib/go",
    "gopath": "/home/vagrant/go",
    "gradle_flags": "--no-build-cache --no-daemon --rerun-tasks",
    "go_cache_isolation": "fresh_per_build",
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
    expected_records = _logical_payload_records(unsigned_apk)
    actual_records = _logical_payload_records(signed_apk)
    if expected_records != actual_records:
        expected_by_path: dict[str, list[dict[str, object]]] = {}
        actual_by_path: dict[str, list[dict[str, object]]] = {}
        for record in expected_records:
            expected_by_path.setdefault(str(record["path"]), []).append(record)
        for record in actual_records:
            actual_by_path.setdefault(str(record["path"]), []).append(record)
        differences = [
            {
                "actual": actual_by_path.get(path, []),
                "expected": expected_by_path.get(path, []),
                "path": path,
            }
            for path in sorted(expected_by_path.keys() | actual_by_path.keys())
            if expected_by_path.get(path, []) != actual_by_path.get(path, [])
        ]
        raise VerificationError(
            "signed APK payload differs from the verified unsigned APK"
            f"\nDiffering payload records:\n{json.dumps(differences, indent=2, sort_keys=True)}"
        )


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
    if document.get("build_profile", "release") != "release":
        raise VerificationError("Android publication provenance must use the Release build profile")
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
    *,
    profile: str = "release",
    source_root: Path | None = None,
    go_root: Path | None = None,
    gopath: Path | None = None,
    gradle_source: str | None = None,
    go_binary_sha256: str | None = None,
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
    build_environment = dict(BUILD_ENVIRONMENT)
    toolchain = dict(TOOLCHAIN)
    if profile == "local-complete":
        required_paths = (source_root, go_root, gopath)
        if any(path is None or not path.is_absolute() for path in required_paths):
            raise VerificationError("local-complete profile requires absolute source, Go root, and GOPATH")
        if gradle_source not in {"wrapper_checksum", "external_verified_archive"}:
            raise VerificationError("local-complete profile requires a verified Gradle distribution source")
        if not isinstance(go_binary_sha256, str) or not SHA256.fullmatch(go_binary_sha256):
            raise VerificationError("local-complete profile requires the Go executable SHA-256")
        build_environment.update({
            "go_root": str(go_root),
            "gopath": str(gopath),
            "source_root": str(source_root),
            "gradle_distribution_source": gradle_source,
        })
        toolchain.update({
            "go_source_commit": None,
            "go_build_origin": "binary_executable",
            "go_binary_sha256": go_binary_sha256,
        })
    elif profile != "release":
        raise VerificationError(f"unsupported Android reproducibility profile: {profile}")

    document: dict[str, object] = {
        "build_environment": build_environment,
        "builds": [
            {"bytes": size, "id": "first", "sha256": first_digest},
            {"bytes": size, "id": "second", "sha256": second_digest},
        ],
        "identical": True,
        "kind": KIND,
        "native_libraries": first_native,
        "schema": SCHEMA,
        "source_sha": source_sha,
        "toolchain": toolchain,
        "version_code": version_code,
        "version_name": version_name,
    }
    if profile == "local-complete":
        document["profile"] = profile
    return document


def verify_document(
    document: object,
    apk: Path,
    source_sha: str,
    version_name: str,
    version_code: int,
    *,
    expected_profile: str = "release",
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
    observed_profile = document.get("profile", "release")
    if observed_profile != expected_profile:
        raise VerificationError(
            f"Android reproducibility profile mismatch: expected {expected_profile}, got {observed_profile}"
        )
    if expected_profile == "local-complete":
        expected_keys.add("profile")
    elif expected_profile != "release":
        raise VerificationError(f"unsupported expected Android reproducibility profile: {expected_profile}")
    if set(document) != expected_keys:
        raise VerificationError("reproducibility evidence fields mismatch")
    if document["schema"] != SCHEMA or document["kind"] != KIND:
        raise VerificationError("reproducibility evidence schema or kind mismatch")
    if document["source_sha"] != source_sha:
        raise VerificationError("reproducibility source SHA mismatch")
    if document["version_name"] != version_name or document["version_code"] != version_code:
        raise VerificationError("reproducibility version mismatch")
    toolchain = document["toolchain"]
    build_environment = document["build_environment"]
    if expected_profile == "release":
        if toolchain != TOOLCHAIN:
            raise VerificationError("reproducibility toolchain mismatch")
        if build_environment != BUILD_ENVIRONMENT:
            raise VerificationError("reproducibility build environment mismatch")
    else:
        if not isinstance(toolchain, dict):
            raise VerificationError("local-complete toolchain must be an object")
        expected_local_toolchain = dict(TOOLCHAIN)
        expected_local_toolchain.update({
            "go_source_commit": None,
            "go_build_origin": "binary_executable",
        })
        actual_local_toolchain = dict(toolchain)
        binary_digest = actual_local_toolchain.pop("go_binary_sha256", None)
        if (
            actual_local_toolchain != expected_local_toolchain
            or not isinstance(binary_digest, str)
            or not SHA256.fullmatch(binary_digest)
        ):
            raise VerificationError("local-complete Go/toolchain record is invalid")
        if not isinstance(build_environment, dict):
            raise VerificationError("local-complete build environment must be an object")
        expected_local_environment = dict(BUILD_ENVIRONMENT)
        expected_local_environment.update({
            "go_root": "",
            "gopath": "",
            "source_root": "",
            "gradle_distribution_source": "",
        })
        if build_environment.keys() != expected_local_environment.keys():
            raise VerificationError("local-complete build environment fields mismatch")
        for key in ("go_root", "gopath", "source_root"):
            value = build_environment.get(key)
            if not isinstance(value, str) or not Path(value).is_absolute():
                raise VerificationError(f"local-complete {key} must be an absolute path")
        if build_environment.get("gradle_distribution_source") not in {
            "wrapper_checksum", "external_verified_archive",
        }:
            raise VerificationError("local-complete Gradle distribution source is invalid")
        for key in ("go_flags", "gradle_flags", "go_cache_isolation"):
            if build_environment.get(key) != BUILD_ENVIRONMENT[key]:
                raise VerificationError(f"local-complete {key} mismatch")
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
    create.add_argument("--profile", choices=("release", "local-complete"), default="release")
    create.add_argument("--source-root", type=Path)
    create.add_argument("--go-root", type=Path)
    create.add_argument("--gopath", type=Path)
    create.add_argument("--gradle-source", choices=("wrapper_checksum", "external_verified_archive"))
    create.add_argument("--go-binary-sha256")
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
                profile=args.profile,
                source_root=args.source_root,
                go_root=args.go_root,
                gopath=args.gopath,
                gradle_source=args.gradle_source,
                go_binary_sha256=args.go_binary_sha256,
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
