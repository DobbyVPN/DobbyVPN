#!/usr/bin/env bash
set -euo pipefail

# This is a deliberately thin public-source entry point.  The Android build
# contract remains in kmp_module's Gradle project; this file only supplies the
# reproducible task invocation and records the exact bytes it produced.  The
# private runner may provide GRADLE_BIN and GIT_BIN after verifying their
# request-bound closure. The trusted Android workflow may also provide a
# content-verified Gradle archive/root pair for legacy sources whose wrapper
# lacks a checksum. Outside those cases, the checked-in Gradle wrapper remains
# the default.

source_root=''
output=''
manifest=''
first_output=''
reproducibility=''
dependency_manifest=''
dependency_spec=''
dependency_closure=''
dependency_helper=''
source_verifier=''
reproducibility_verifier=''
source_repository='DobbyVPN/DobbyVPN'
tool_closure_manifest=${DOBBYVPN_ANDROID_TOOL_CLOSURE:-}
trusted_helper_root=''
trusted_helper_sha=''
trusted_helper_validation_only=0
dependency_helper_explicit=''
dependency_spec_explicit=''
source_verifier_explicit=''
reproducibility_verifier_explicit=''
gradle_archive=''
gradle_root=''
closure_allowlist_file=''

cleanup_closure_allowlist() {
  if [[ -n "$closure_allowlist_file" && -e "$closure_allowlist_file" ]]; then
    rm -f -- "$closure_allowlist_file"
  fi
}
trap cleanup_closure_allowlist EXIT
while (($#)); do
  case "$1" in
    --source-root)
      (($# >= 2)) || { echo 'missing --source-root value' >&2; exit 2; }
      source_root=$2
      shift 2
      ;;
    --output)
      (($# >= 2)) || { echo 'missing --output value' >&2; exit 2; }
      output=$2
      shift 2
      ;;
    --manifest)
      (($# >= 2)) || { echo 'missing --manifest value' >&2; exit 2; }
      manifest=$2
      shift 2
      ;;
    --first-output)
      (($# >= 2)) || { echo 'missing --first-output value' >&2; exit 2; }
      first_output=$2
      shift 2
      ;;
    --reproducibility)
      (($# >= 2)) || { echo 'missing --reproducibility value' >&2; exit 2; }
      reproducibility=$2
      shift 2
      ;;
    --dependency-manifest)
      (($# >= 2)) || { echo 'missing --dependency-manifest value' >&2; exit 2; }
      dependency_manifest=$2
      shift 2
      ;;
    --dependency-helper)
      (($# >= 2)) || { echo 'missing --dependency-helper value' >&2; exit 2; }
      dependency_helper=$2
      dependency_helper_explicit=$2
      shift 2
      ;;
    --dependency-spec)
      (($# >= 2)) || { echo 'missing --dependency-spec value' >&2; exit 2; }
      dependency_spec=$2
      dependency_spec_explicit=$2
      shift 2
      ;;
    --dependency-closure)
      (($# >= 2)) || { echo 'missing --dependency-closure value' >&2; exit 2; }
      dependency_closure=$2
      shift 2
      ;;
    --source-verifier)
      (($# >= 2)) || { echo 'missing --source-verifier value' >&2; exit 2; }
      source_verifier=$2
      source_verifier_explicit=$2
      shift 2
      ;;
    --reproducibility-verifier)
      (($# >= 2)) || { echo 'missing --reproducibility-verifier value' >&2; exit 2; }
      reproducibility_verifier=$2
      reproducibility_verifier_explicit=$2
      shift 2
      ;;
    --gradle-archive)
      (($# >= 2)) || { echo 'missing --gradle-archive value' >&2; exit 2; }
      gradle_archive=$2
      shift 2
      ;;
    --gradle-root)
      (($# >= 2)) || { echo 'missing --gradle-root value' >&2; exit 2; }
      gradle_root=$2
      shift 2
      ;;
    --trusted-helper-root)
      (($# >= 2)) || { echo 'missing --trusted-helper-root value' >&2; exit 2; }
      trusted_helper_root=$2
      shift 2
      ;;
    --trusted-helper-sha)
      (($# >= 2)) || { echo 'missing --trusted-helper-sha value' >&2; exit 2; }
      trusted_helper_sha=$2
      shift 2
      ;;
    --validate-helper-contract)
      trusted_helper_validation_only=1
      shift
      ;;
    --source-repository)
      (($# >= 2)) || { echo 'missing --source-repository value' >&2; exit 2; }
      source_repository=$2
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ "$trusted_helper_validation_only" == 1 ]]; then
  [[ -n "$source_root" && -n "$trusted_helper_root" && -n "$trusted_helper_sha" ]] || {
    echo '--source-root, --trusted-helper-root, and --trusted-helper-sha are required for contract validation' >&2
    exit 2
  }
else
  [[ -n "$source_root" && -n "$output" && -n "$manifest" ]] || {
    echo '--source-root, --output, and --manifest are required' >&2
    exit 2
  }
fi
[[ -d "$source_root" && ! -L "$source_root" ]] || {
  echo 'source root must be a non-symlink directory' >&2
  exit 2
}
source_root=$(cd -- "$source_root" && pwd -P)
first_output=${first_output:-"$source_root/.android-build/first.apk"}
reproducibility=${reproducibility:-"$source_root/runtime/android-reproducibility.json"}
dependency_manifest=${dependency_manifest:-"$source_root/runtime/android-dependency-provenance.json"}
dependency_spec=${dependency_spec:-"${DOBBYVPN_ANDROID_DEPENDENCY_SPEC:-$source_root/.github/android/dependency-spec.json}"}
dependency_closure=${dependency_closure:-"${DOBBYVPN_ANDROID_DEPENDENCY_CLOSURE:-}"}
dependency_helper=${dependency_helper:-"$source_root/.github/scripts/android_dependency_provenance.py"}
source_verifier=${source_verifier:-"$source_root/.github/scripts/verify_android_apk_source.py"}
reproducibility_verifier=${reproducibility_verifier:-"$source_root/.github/scripts/verify_android_reproducibility.py"}
closure_mode=0
if [[ -n "$dependency_closure" ]]; then
  closure_mode=1
fi

git_bin=${GIT_BIN:-git}
gradle_bin=${GRADLE_BIN:-"$source_root/kmp_module/gradlew"}

# Destination paths are a deliberately narrow contract: they must be absolute,
# distinct, below the canonical source checkout, free of dot-dot components,
# and must not traverse an existing symlink.  Validate both before and after
# creating parent directories so a lexical alias cannot escape the checkout.
validate_destinations() {
  SOURCE_ROOT="$source_root" OUTPUT="$output" MANIFEST="$manifest" FIRST_OUTPUT="$first_output" REPRODUCIBILITY="$reproducibility" DEPENDENCY_MANIFEST="$dependency_manifest" python3 - <<'PY'
import os
from pathlib import Path

source = Path(os.environ["SOURCE_ROOT"])
destinations = [
    ("output", Path(os.environ["OUTPUT"])),
    ("manifest", Path(os.environ["MANIFEST"])),
    ("first output", Path(os.environ["FIRST_OUTPUT"])),
    ("reproducibility", Path(os.environ["REPRODUCIBILITY"])),
    ("dependency manifest", Path(os.environ["DEPENDENCY_MANIFEST"])),
]
if any(not path.is_absolute() for _, path in destinations):
    raise SystemExit("all output paths must be absolute paths")

normalized = []
for label, path in destinations:
    try:
        relative = path.relative_to(source)
    except ValueError:
        raise SystemExit(f"{label} must be below source root")
    if not relative.parts or any(part in (".", "..") for part in relative.parts):
        raise SystemExit(f"{label} must not contain dot components")
    current = source
    for part in relative.parts:
        current /= part
        if current.is_symlink():
            raise SystemExit(f"{label} must not traverse a symlink")
    if path.exists() or path.is_symlink():
        raise SystemExit(f"{label} path is already occupied")
    normalized.append(path)

if len(set(normalized)) != len(normalized):
    raise SystemExit("all output paths must be distinct")
PY
}

validate_source_checkout() {
  local root=$1
  local expected_commit=${2:-}
  local observed_commit
  observed_commit=$("$git_bin" -C "$root" rev-parse --verify HEAD^{commit})
  [[ "$observed_commit" =~ ^[0-9a-f]{40}$ ]] || {
    echo "source checkout did not yield a canonical Git commit: $root" >&2
    exit 2
  }
  if [[ -n "$expected_commit" && "$observed_commit" != "$expected_commit" ]]; then
    echo "source checkout commit changed during the build: expected $expected_commit got $observed_commit" >&2
    exit 2
  fi
  if ! "$git_bin" -C "$root" diff --quiet --no-ext-diff HEAD --; then
    echo "source checkout has tracked worktree modifications: $root" >&2
    exit 2
  fi
  if ! "$git_bin" -C "$root" diff --cached --quiet --no-ext-diff HEAD --; then
    echo "source checkout has staged modifications: $root" >&2
    exit 2
  fi
  if [[ -n $("$git_bin" -C "$root" ls-files --others --exclude-standard) ]]; then
    echo "source checkout has unexpected untracked files: $root" >&2
    exit 2
  fi
  if [[ -n $("$git_bin" -C "$root" status --porcelain=v2 --ignored | awk '$1 == "!" || $1 == "!!" {print; exit}') ]]; then
    echo "source checkout contains unexpected ignored build state: $root" >&2
    exit 2
  fi
}

validate_source_checkout_with_closure() {
  local root=$1
  local allowlist=$2
  local closure=$3
  SOURCE_ROOT="$root" GIT_BIN="$git_bin" CLOSURE_ALLOWLIST="$allowlist" CLOSURE_PATH="$closure" python3 - <<'PY'
import os
import stat
import subprocess
from pathlib import Path

source = Path(os.environ["SOURCE_ROOT"]).resolve(strict=True)
git_bin = os.environ["GIT_BIN"]
allowlist_path = Path(os.environ["CLOSURE_ALLOWLIST"])
closure_path = Path(os.environ["CLOSURE_PATH"]).resolve(strict=True)
try:
    closure_directory = closure_path.parent.relative_to(source)
except ValueError as error:
    raise SystemExit("dependency closure is outside the exact source root") from error
closure_directory = source / closure_directory
allowed = set()
for line in allowlist_path.read_text(encoding="utf-8").splitlines():
    relative = Path(line)
    if not line or "\x00" in line or relative.is_absolute() or relative.as_posix() != line or any(part in {"", ".", ".."} for part in relative.parts):
        raise SystemExit("closure allowlist contains a non-canonical path")
    allowed.add(line)

def git_paths(*args: str) -> set[str]:
    completed = subprocess.run(
        [git_bin, "-C", str(source), "ls-files", "-z", *args],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if completed.returncode:
        raise SystemExit(completed.stderr.decode("utf-8", "replace"))
    raw = completed.stdout
    if raw and not raw.endswith(b"\x00"):
        raise SystemExit("Git returned a non-NUL-terminated path list")
    return {item.decode("utf-8") for item in raw.rstrip(b"\x00").split(b"\x00") if item}

if subprocess.run([git_bin, "-C", str(source), "diff", "--quiet", "--no-ext-diff", "HEAD", "--"], check=False).returncode:
    raise SystemExit("source checkout has tracked worktree modifications")
if subprocess.run([git_bin, "-C", str(source), "diff", "--cached", "--quiet", "--no-ext-diff", "HEAD", "--"], check=False).returncode:
    raise SystemExit("source checkout has staged modifications")
untracked = git_paths("--others", "--exclude-standard")
ignored = git_paths("--others", "--ignored", "--exclude-standard")
unexpected = sorted((untracked | ignored) - allowed)
if unexpected:
    raise SystemExit("source checkout contains undeclared untracked/ignored build state: " + ", ".join(unexpected[:5]))

for relative in sorted(untracked | ignored):
    if relative not in allowed:
        continue
    path = source / relative
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode) or (info.st_mode & 0o077):
        raise SystemExit("owner-staged closure files must be regular owner-only files: " + relative)
    current = path.parent
    while current != closure_directory:
        info = current.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode) or (info.st_mode & 0o077):
            raise SystemExit("owner-staged closure directories must be real owner-only directories: " + str(current))
        current = current.parent
info = closure_directory.lstat()
if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode) or (info.st_mode & 0o077):
    raise SystemExit("owner-staged closure directory must be a real owner-only directory")
PY
}

trusted_helper_file() {
  local path=$1
  local current=$trusted_helper_root
  local relative=${path#"$trusted_helper_root/"}
  local part
  IFS=/ read -ra parts <<< "$relative"
  for part in "${parts[@]}"; do
    current="$current/$part"
    [[ ! -L "$current" ]] || {
      echo "trusted helper path traverses a symlink: $path" >&2
      exit 2
    }
  done
  [[ -f "$path" ]] || {
    echo "trusted helper file is missing: $path" >&2
    exit 2
  }
  if [[ "$path" == */android_build_driver.sh && ! -x "$path" ]]; then
    echo "trusted Android build driver is not executable: $path" >&2
    exit 2
  fi
}

validate_trusted_helper_root() {
  [[ -n "$trusted_helper_root" && -n "$trusted_helper_sha" ]] || {
    echo 'trusted helper root and full trusted helper SHA are required together' >&2
    exit 2
  }
  [[ "$trusted_helper_sha" =~ ^[0-9a-f]{40}$ ]] || {
    echo 'trusted helper SHA must be a full lowercase Git commit identity' >&2
    exit 2
  }
  [[ -d "$trusted_helper_root" && ! -L "$trusted_helper_root" ]] || {
    echo 'trusted helper root must be a non-symlink directory' >&2
    exit 2
  }
  trusted_helper_root=$(cd -- "$trusted_helper_root" && pwd -P)
  local observed_sha
  observed_sha=$("$git_bin" -C "$trusted_helper_root" rev-parse --verify HEAD^{commit})
  [[ "$observed_sha" == "$trusted_helper_sha" ]] || {
    echo "trusted helper checkout SHA mismatch: expected $trusted_helper_sha got $observed_sha" >&2
    exit 2
  }
  validate_source_checkout "$trusted_helper_root" "$trusted_helper_sha"
  local expected_helper
  for expected_helper in \
    "$trusted_helper_root/.github/scripts/android_build_driver.sh" \
    "$trusted_helper_root/.github/scripts/android_dependency_provenance.py" \
    "$trusted_helper_root/.github/scripts/public_output.py" \
    "$trusted_helper_root/.github/scripts/verify_android_apk_source.py" \
    "$trusted_helper_root/.github/scripts/verify_android_reproducibility.py" \
    "$trusted_helper_root/.github/android/dependency-spec.json"; do
    trusted_helper_file "$expected_helper"
  done
  [[ -z "$dependency_helper_explicit" || "$dependency_helper_explicit" == "$trusted_helper_root/.github/scripts/android_dependency_provenance.py" ]] || {
    echo 'explicit dependency helper is not the trusted helper checkout' >&2
    exit 2
  }
  [[ -z "$dependency_spec_explicit" || "$dependency_spec_explicit" == "$trusted_helper_root/.github/android/dependency-spec.json" ]] || {
    echo 'explicit dependency specification is not the trusted helper checkout' >&2
    exit 2
  }
  [[ -z "$source_verifier_explicit" || "$source_verifier_explicit" == "$trusted_helper_root/.github/scripts/verify_android_apk_source.py" ]] || {
    echo 'explicit APK source verifier is not the trusted helper checkout' >&2
    exit 2
  }
  [[ -z "$reproducibility_verifier_explicit" || "$reproducibility_verifier_explicit" == "$trusted_helper_root/.github/scripts/verify_android_reproducibility.py" ]] || {
    echo 'explicit reproducibility verifier is not the trusted helper checkout' >&2
    exit 2
  }
  dependency_helper="$trusted_helper_root/.github/scripts/android_dependency_provenance.py"
  dependency_spec="$trusted_helper_root/.github/android/dependency-spec.json"
  source_verifier="$trusted_helper_root/.github/scripts/verify_android_apk_source.py"
  reproducibility_verifier="$trusted_helper_root/.github/scripts/verify_android_reproducibility.py"
}

if [[ "$closure_mode" == 0 ]]; then
  validate_source_checkout "$source_root"
fi
if [[ -n "$trusted_helper_root" || -n "$trusted_helper_sha" ]]; then
  [[ "$closure_mode" == 0 ]] || {
    echo 'trusted helper checkout is not compatible with an owner dependency closure' >&2
    exit 2
  }
  validate_trusted_helper_root
fi

if [[ "$trusted_helper_validation_only" == 1 ]]; then
  echo "trusted helper contract passed source_root=$source_root trusted_helper_sha=$trusted_helper_sha"
  exit 0
fi

evidence_dir=${DOBBYVPN_BUILD_EVIDENCE_DIR:-}
stdout_original=''
stderr_original=''
start_evidence_capture() {
  [[ "$evidence_dir" = /* && ! -L "$evidence_dir" ]] || {
    echo 'Android build evidence directory must be an absolute non-symlink path' >&2
    exit 2
  }
  mkdir -p -- "$evidence_dir"
  chmod 700 -- "$evidence_dir"
  # Use fresh O_EXCL paths for each run so a second invocation cannot replace
  # an earlier complete original. tee may truncate only these newly-created
  # empty files, never an existing evidence record.
  stdout_original="$(mktemp "$evidence_dir/stdout.original.XXXXXX.log")"
  stderr_original="$(mktemp "$evidence_dir/stderr.original.XXXXXX.log")"
  chmod 600 "$stdout_original" "$stderr_original"
  exec > >(tee -- "$stdout_original") \
    2> >(tee -- "$stderr_original" >&2)
}

validate_destinations

sync_path() {
  python3 - "$1" <<'PY'
import os
import sys
from pathlib import Path

path = Path(sys.argv[1])
if path.is_symlink() or not path.is_file():
    raise SystemExit(f"cannot durably record non-regular path: {path}")
with path.open("rb") as stream:
    os.fsync(stream.fileno())
flags = getattr(os, "O_DIRECTORY", 0)
descriptor = os.open(path.parent, os.O_RDONLY | flags)
try:
    os.fsync(descriptor)
finally:
    os.close(descriptor)
PY
}

validate_tool_closure() {
  [[ "${DOBBYVPN_REQUIRE_TOOL_CLOSURE:-0}" == 1 ]] || return 0
  [[ -n "$tool_closure_manifest" ]] || {
    echo 'request-bound tool closure manifest is required' >&2
    exit 2
  }
  TOOL_CLOSURE_MANIFEST="$tool_closure_manifest" python3 - <<'PY'
import hashlib
import json
import os
import stat
from pathlib import Path

manifest_path = Path(os.environ["TOOL_CLOSURE_MANIFEST"])
if manifest_path.is_symlink() or not manifest_path.is_file():
    raise SystemExit("tool closure manifest must be a regular file")
try:
    document = json.loads(manifest_path.read_text(encoding="utf-8"))
except (OSError, UnicodeError, json.JSONDecodeError) as error:
    raise SystemExit("tool closure manifest is not valid JSON") from error
if not isinstance(document, dict) or document.get("schema") != 1:
    raise SystemExit("tool closure manifest has an unsupported schema")
closure = document.get("tool_closure")
if not isinstance(closure, dict):
    raise SystemExit("tool closure manifest has no tool_closure object")

def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()

def tree(root: Path) -> str:
    if root.is_symlink() or not root.is_dir():
        raise SystemExit(f"tool closure root is not a real directory: {root}")
    entries = []
    for path in sorted(root.rglob("*"), key=lambda item: item.relative_to(root).as_posix()):
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            raise SystemExit(f"tool closure contains a symlink: {root / relative}")
        if path.is_dir():
            entries.append({"path": relative, "type": "directory"})
        elif path.is_file():
            entries.append({"path": relative, "type": "file", "size_bytes": path.stat().st_size, "sha256": digest(path)})
        else:
            raise SystemExit(f"tool closure contains a non-regular entry: {root / relative}")
    encoded = (json.dumps({"schema": 1, "entries": entries}, sort_keys=True, separators=(",", ":")) + "\n").encode()
    return hashlib.sha256(encoded).hexdigest()

roots = {
    "java_home": "java_home_tree_sha256",
    "gradle_root": "gradle_tree_sha256",
    "sdk_root": "sdk_tree_sha256",
    "ndk_root": "ndk_tree_sha256",
    "go_root": "go_root_tree_sha256",
    "go_path": "go_path_tree_sha256",
}
for path_key, digest_key in roots.items():
    path_value = closure.get(path_key)
    expected = closure.get(digest_key)
    if not isinstance(path_value, str) or not Path(path_value).is_absolute() or ".." in Path(path_value).parts:
        raise SystemExit(f"tool closure path is unsafe: {path_key}")
    if not isinstance(expected, str) or len(expected) != 64:
        raise SystemExit(f"tool closure tree identity is invalid: {digest_key}")
    if tree(Path(path_value)) != expected:
        raise SystemExit(f"tool closure tree digest mismatch: {path_key}")

executables = {
    "java_path": "java_sha256",
    "gradle_path": "gradle_sha256",
    "go_bin_path": "go_sha256",
    "gomobile_path": "gomobile_sha256",
    "gobind_path": "gobind_sha256",
    "apksigner_path": "apksigner_sha256",
    "apkanalyzer_path": "apkanalyzer_sha256",
}
for path_key, digest_key in executables.items():
    path_value = closure.get(path_key)
    expected = closure.get(digest_key)
    if not isinstance(path_value, str) or not Path(path_value).is_absolute() or ".." in Path(path_value).parts:
        raise SystemExit(f"tool executable path is unsafe: {path_key}")
    path = Path(path_value)
    if path.is_symlink() or not path.is_file() or not (path.stat().st_mode & stat.S_IXUSR):
        raise SystemExit(f"tool executable is invalid: {path_key}")
    parent_root = {
        "java_path": "java_home",
        "gradle_path": "gradle_root",
        "go_bin_path": "go_root",
        "gomobile_path": "go_path",
        "gobind_path": "go_path",
        "apksigner_path": "sdk_root",
        "apkanalyzer_path": "sdk_root",
    }[path_key]
    try:
        path.relative_to(Path(closure[parent_root]))
    except (KeyError, ValueError) as error:
        raise SystemExit(f"tool executable is outside its pinned root: {path_key}") from error
    if not isinstance(expected, str) or digest(path) != expected:
        raise SystemExit(f"tool executable digest mismatch: {path_key}")
PY
}

source_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit})
source_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree})
[[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_tree" =~ ^[0-9a-f]{40}$ ]] || {
  echo 'source checkout did not yield canonical Git identities' >&2
  exit 2
}
if [[ -z "$trusted_helper_root" ]]; then
  helper_paths=("$dependency_helper" "$source_verifier" "$reproducibility_verifier")
  if [[ "$closure_mode" == 0 ]]; then
    helper_paths+=("$dependency_spec")
  fi
  for helper in "${helper_paths[@]}"; do
    case "$helper" in
      "$source_root"/*) [[ -f "$helper" && ! -L "$helper" ]] || { echo "source helper is missing or symlinked: $helper" >&2; exit 2; } ;;
      *) echo "source helper must be inside the exact source checkout: $helper" >&2; exit 2 ;;
    esac
  done
fi
helper_contract_args=()
if [[ -n "$trusted_helper_root" ]]; then
  helper_contract_args=(--trusted-helper-root "$trusted_helper_root" --trusted-helper-sha "$trusted_helper_sha")
fi
if [[ "$closure_mode" == 1 ]]; then
  [[ -z "$dependency_spec_explicit" ]] || {
    echo 'dependency specification cannot be combined with an owner dependency closure' >&2
    exit 2
  }
  [[ "$dependency_closure" = /* && ! -L "$dependency_closure" && -f "$dependency_closure" ]] || {
    echo 'owner dependency closure must be an absolute regular non-symlink file' >&2
    exit 2
  }
  closure_allowlist_file=$(mktemp "${TMPDIR:-/tmp}/dobbyvpn-android-closure-allowlist.XXXXXX")
  chmod 600 "$closure_allowlist_file"
  python3 "$dependency_helper" --source-root "$source_root" \
    --closure-evidence "$dependency_closure" --print-staged-paths > "$closure_allowlist_file"
  validate_source_checkout_with_closure "$source_root" "$closure_allowlist_file" "$dependency_closure"
fi
if [[ -n "$evidence_dir" ]]; then
  start_evidence_capture
fi
validate_tool_closure
gradle_proof_args=()
if [[ -n "$gradle_archive" || -n "$gradle_root" ]]; then
  [[ -n "$gradle_archive" && -n "$gradle_root" ]] || {
    echo 'external Gradle archive and root proof must be supplied together' >&2
    exit 2
  }
  [[ "$gradle_archive" = /* && "$gradle_root" = /* ]] || {
    echo 'external Gradle archive and root proof paths must be absolute' >&2
    exit 2
  }
  [[ "$closure_mode" == 0 ]] || {
    echo 'external Gradle proof is not accepted with an owner dependency closure' >&2
    exit 2
  }
  python3 "$dependency_helper" --spec "$dependency_spec" "${helper_contract_args[@]}" \
    --verify-gradle-distribution --gradle-archive "$gradle_archive" --gradle-root "$gradle_root"
  gradle_bin=${GRADLE_BIN:-"$gradle_root/bin/gradle"}
  [[ "$gradle_bin" == "$gradle_root/bin/gradle" ]] || {
    echo 'GRADLE_BIN must be the bin/gradle from the externally verified root' >&2
    exit 2
  }
  gradle_proof_args=(--gradle-archive "$gradle_archive" --gradle-root "$gradle_root")
fi
[[ -x "$gradle_bin" ]] || { echo "Gradle entry point is not executable: $gradle_bin" >&2; exit 2; }

version_name=$(sed -n 's/^versionName=//p' "$source_root/kmp_module/gradle.properties")
version_code=$(sed -n 's/^versionCode=//p' "$source_root/kmp_module/gradle.properties")
[[ "$version_name" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$version_code" =~ ^[1-9][0-9]*$ ]] || {
  echo 'Android version properties are missing or invalid' >&2
  exit 2
}

go_bin=${GO_BIN:-"$(command -v go || true)"}
go_root=${GOROOT:-}
go_path=${GOPATH:-}
gomobile_bin=${GOMOBILE:-}
gobind_bin=${GOBIND:-}
[[ -n "$go_bin" && -x "$go_bin" ]] || { echo 'exact Go executable is required' >&2; exit 2; }
[[ -n "$go_root" && -d "$go_root" ]] || { echo 'GOROOT is required for the pinned Android build' >&2; exit 2; }
[[ -n "$go_path" && -d "$go_path" ]] || { echo 'GOPATH is required for the pinned Android build' >&2; exit 2; }
gomobile_bin=${gomobile_bin:-"$go_path/bin/gomobile"}
gobind_bin=${gobind_bin:-"$go_path/bin/gobind"}
[[ "$gomobile_bin" == "$go_path/bin/gomobile" && "$gobind_bin" == "$go_path/bin/gobind" ]] || {
  echo 'GOMOBILE and GOBIND must resolve to the pinned GOPATH/bin tools' >&2
  exit 2
}
export GOROOT="$go_root"
export GOPATH="$go_path"
export GOMOBILE="$gomobile_bin"
export GOBIND="$gobind_bin"
export GOFLAGS='-trimpath -buildvcs=false'
expected_go_version="go$(tr -d '[:space:]' < "$source_root/.go-version")"
[[ "$($go_bin env GOVERSION)" == "$expected_go_version" ]] || { echo 'Go version does not match .go-version' >&2; exit 2; }
[[ "$($go_bin env GOROOT)" == "$GOROOT" && "$($go_bin env GOPATH)" == "$GOPATH" ]] || {
  echo 'Go environment does not match the owner-pinned closure' >&2
  exit 2
}
helper_contract_args=()
if [[ -n "$trusted_helper_root" ]]; then
  helper_contract_args=(--trusted-helper-root "$trusted_helper_root" --trusted-helper-sha "$trusted_helper_sha")
fi
java_bin=${JAVA_BIN:-"$(command -v java || true)"}
[[ -n "$java_bin" && -x "$java_bin" ]] || { echo 'Java executable is required' >&2; exit 2; }
java_version_output=$("$java_bin" -version 2>&1 | tee /dev/stderr)
java_version=''
java_version_pattern='version[[:space:]]+"([^"]+)"'
while IFS= read -r java_line; do
  if [[ "$java_line" =~ $java_version_pattern ]]; then
    java_version=${BASH_REMATCH[1]}
    break
  fi
done <<< "$java_version_output"
[[ -n "$java_version" && ( "$java_version" == '17' || "$java_version" == 17.* ) ]] || {
  echo "Java runtime must have major version 17; observed $java_version" >&2
  exit 2
}
if [[ "$closure_mode" == 1 ]]; then
  mobile_pin=$(python3 "$dependency_helper" --closure-evidence "$dependency_closure" --print-mobile-version)
else
  mobile_pin=$(python3 "$dependency_helper" --spec "$dependency_spec" "${helper_contract_args[@]}" --print-mobile-version)
fi
mobile_module=${mobile_pin%@*}
mobile_version=${mobile_pin#*@}
[[ "$mobile_module" == 'golang.org/x/mobile' && "$mobile_version" == 'v0.0.0-20260520154334-0e4426e1883d' ]] || {
  echo 'dependency specification yielded an unexpected x/mobile pin' >&2
  exit 2
}
tool_metadata_matches_pin() {
  local tool=$1
  # tee preserves every metadata byte on stderr; grep is only a derived
  # predicate and is never the diagnostic record.
  "$go_bin" version -m "$tool" | tee /dev/stderr | \
    grep -F "$mobile_module" | grep -F "$mobile_version" >/dev/null
}
ensure_mobile_tool() {
  local name=$1
  local path="$go_path/bin/$name"
  if [[ "$closure_mode" == 1 ]]; then
    [[ -x "$path" ]] || { echo "owner-pinned $name is missing at $path" >&2; exit 2; }
    tool_metadata_matches_pin "$path" || {
      echo "owner-pinned $name is not built from $mobile_module@$mobile_version" >&2
      exit 2
    }
    return 0
  fi
  if [[ ! -x "$path" ]] || ! tool_metadata_matches_pin "$path"; then
    echo "building pinned $name from $mobile_module@$mobile_version" >&2
    GOBIN="$go_path/bin" "$go_bin" install "$mobile_module/cmd/$name@$mobile_version"
  fi
  [[ -x "$path" ]] || { echo "pinned $name was not produced at $path" >&2; exit 2; }
  tool_metadata_matches_pin "$path" || {
    echo "tool module metadata is not the pinned $mobile_module revision: $path" >&2
    exit 2
  }
}
ensure_mobile_tool gomobile
ensure_mobile_tool gobind
[[ -n "${ANDROID_SDK_ROOT:-}" && -x "$ANDROID_SDK_ROOT/build-tools/36.0.0/apksigner" ]] || {
  echo 'Android SDK/build-tools 36.0.0 apksigner is required' >&2
  exit 2
}
[[ -n "${ANDROID_NDK_HOME:-}" && -f "$ANDROID_NDK_HOME/source.properties" ]] || {
  echo 'Android NDK 27.3.13750724 is required' >&2
  exit 2
}
grep -F 'Pkg.Revision = 27.3.13750724' "$ANDROID_NDK_HOME/source.properties" >/dev/null || {
  echo 'Android NDK revision is not 27.3.13750724' >&2
  exit 2
}
gradle_version=$("$gradle_bin" --version --no-daemon | tee /dev/stderr | awk '/^Gradle / && !seen {version=$2; seen=1} END {if (seen) print version}')
[[ "$gradle_version" == '8.13' ]] || { echo 'Gradle version is not 8.13' >&2; exit 2; }

gradle_offline=${DOBBYVPN_GRADLE_OFFLINE:-0}
if [[ "$closure_mode" == 1 || "${DOBBYVPN_REQUIRE_TOOL_CLOSURE:-0}" == 1 ]]; then
  [[ "$gradle_offline" == 1 ]] || { echo 'request-bound source builds must use Gradle offline mode' >&2; exit 2; }
  [[ -n "${GRADLE_USER_HOME:-}" && -d "$GRADLE_USER_HOME" && ! -L "$GRADLE_USER_HOME" ]] || {
    echo 'isolated Gradle user home is required for request-bound source builds' >&2
    exit 2
  }
fi

build_cache=${DOBBYVPN_GOMOBILE_GOCACHE:-"$source_root/.android-build/go-cache"}
build_tmp=${DOBBYVPN_GOMOBILE_GOTMPDIR:-"$source_root/.android-build/go-tmp"}
mkdir -p "$build_cache" "$build_tmp" "$(dirname -- "$first_output")" "$(dirname -- "$output")"
# Recheck after parent creation.  The first validation deliberately refuses
# occupied outputs; this second check closes the documented mkdir window and
# refuses an injected symlink before any build bytes are written.
validate_destinations

write_dependency_manifest() {
  if [[ "$closure_mode" == 1 ]]; then
    python3 "$dependency_helper" --source-root "$source_root" --source-commit "$source_commit" \
      --source-tree "$source_tree" --closure-evidence "$dependency_closure" \
      --output "$dependency_manifest"
  else
    python3 "$dependency_helper" --source-root "$source_root" --source-commit "$source_commit" \
      --source-tree "$source_tree" --spec "$dependency_spec" "${helper_contract_args[@]}" \
      --java-version "$java_version" "${gradle_proof_args[@]}" --output "$dependency_manifest"
  fi
}

verify_dependency_manifest() {
  if [[ "$closure_mode" == 1 ]]; then
    python3 "$dependency_helper" --verify-manifest --source-root "$source_root" \
      --source-commit "$source_commit" --source-tree "$source_tree" \
      --closure-evidence "$dependency_closure" --manifest "$dependency_manifest"
  else
    python3 "$dependency_helper" --verify-manifest --source-root "$source_root" \
      --source-commit "$source_commit" --source-tree "$source_tree" --spec "$dependency_spec" \
      "${helper_contract_args[@]}" --java-version "$java_version" "${gradle_proof_args[@]}" \
      --manifest "$dependency_manifest"
  fi
}

materialize_dependency_cache() {
  [[ "$closure_mode" == 1 ]] || return 0
  [[ -n "${GRADLE_USER_HOME:-}" && "$GRADLE_USER_HOME" = /* && ! -L "$GRADLE_USER_HOME" && -d "$GRADLE_USER_HOME" ]] || {
    echo 'owner dependency closure requires an empty real GRADLE_USER_HOME' >&2
    exit 2
  }
  SOURCE_ROOT="$source_root" CLOSURE="$dependency_closure" GRADLE_HOME="$GRADLE_USER_HOME" python3 - <<'PY'
import hashlib
import json
import os
import stat
from pathlib import Path

source = Path(os.environ["SOURCE_ROOT"]).resolve(strict=True)
closure_path = Path(os.environ["CLOSURE"])
gradle_home = Path(os.environ["GRADLE_HOME"])
if closure_path.is_symlink() or not closure_path.is_file():
    raise SystemExit("owner dependency closure is not a regular file")
if gradle_home.is_symlink() or not gradle_home.is_dir():
    raise SystemExit("GRADLE_USER_HOME must be a real directory")
try:
    closure_relative = closure_path.resolve(strict=True).relative_to(source)
except (OSError, ValueError) as error:
    raise SystemExit("owner dependency closure must be beneath the exact source root") from error
try:
    document = json.loads(closure_path.read_text(encoding="utf-8"))
except (OSError, UnicodeError, json.JSONDecodeError) as error:
    raise SystemExit("owner dependency closure is not valid JSON") from error
if not isinstance(document, dict) or document.get("schema") != 1:
    raise SystemExit("owner dependency closure has an unsupported schema")

def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()

def safe_relative(value: object, label: str) -> Path:
    if not isinstance(value, str) or not value or "\x00" in value or "\n" in value or "\\" in value:
        raise SystemExit(f"{label} is not a canonical relative path")
    path = Path(value)
    if path.is_absolute() or path.as_posix() != value or any(part in {"", ".", ".."} for part in path.parts):
        raise SystemExit(f"{label} is not a canonical relative path")
    return path

def source_file(path: Path, label: str, expected_sha: str, expected_size: int) -> None:
    current = path.parent
    while current != source:
        if current.is_symlink() or not current.is_dir():
            raise SystemExit(f"{label} traverses an invalid directory")
        current = current.parent
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode) or (info.st_mode & 0o077):
        raise SystemExit(f"{label} must be a regular owner-only file")
    if info.st_size != expected_size or digest(path) != expected_sha:
        raise SystemExit(f"{label} bytes do not match the closure identity")

def destination_file(path: Path, source_path: Path, label: str) -> None:
    current = gradle_home
    try:
        relative = path.relative_to(gradle_home)
    except ValueError as error:
        raise SystemExit(f"{label} escapes GRADLE_USER_HOME") from error
    for part in relative.parts[:-1]:
        current /= part
        if current.exists() and (current.is_symlink() or not current.is_dir()):
            raise SystemExit(f"{label} destination traverses an invalid directory")
        current.mkdir(mode=0o700, exist_ok=True)
        current.chmod(0o700)
    if path.exists() or path.is_symlink():
        raise SystemExit(f"{label} destination is already occupied")
    with source_path.open("rb") as source_stream, path.open("xb") as destination_stream:
        while True:
            chunk = source_stream.read(1024 * 1024)
            if not chunk:
                break
            destination_stream.write(chunk)
        destination_stream.flush()
        os.fchmod(destination_stream.fileno(), 0o600)
        os.fsync(destination_stream.fileno())

closure_directory = source / closure_relative.parent
artifact_root = closure_directory / safe_relative(document.get("artifact_root"), "artifact_root")
cache_root = closure_directory / safe_relative(document.get("cache_root"), "cache_root")
counts = {"artifacts": 0, "cache": 0}
bytes_copied = {"artifacts": 0, "cache": 0}
for item in document.get("resolved_artifacts", []):
    if not isinstance(item, dict):
        raise SystemExit("resolved artifact entry is not an object")
    relative = safe_relative(item.get("path"), "resolved artifact path")
    sha = item.get("sha256")
    size = item.get("size_bytes")
    if not isinstance(sha, str) or not isinstance(size, int) or isinstance(size, bool) or size <= 0:
        raise SystemExit("resolved artifact identity is invalid")
    source_path = artifact_root / relative
    source_file(source_path, "resolved artifact", sha, size)
    destination_file(gradle_home / "caches/modules-2/files-2.1" / relative, source_path, "resolved artifact")
    counts["artifacts"] += 1
    bytes_copied["artifacts"] += size
for item in document.get("cache_entries", []):
    if not isinstance(item, dict):
        raise SystemExit("Gradle cache entry is not an object")
    relative = safe_relative(item.get("path"), "Gradle cache path")
    sha = item.get("sha256")
    size = item.get("size_bytes")
    if not isinstance(sha, str) or not isinstance(size, int) or isinstance(size, bool) or size < 0:
        raise SystemExit("Gradle cache identity is invalid")
    source_path = cache_root / relative
    source_file(source_path, "Gradle cache entry", sha, size)
    destination_file(gradle_home / "caches/modules-2" / relative, source_path, "Gradle cache entry")
    counts["cache"] += 1
    bytes_copied["cache"] += size
print(f"dependency_cache_materialized artifacts={counts['artifacts']} artifact_bytes={bytes_copied['artifacts']} cache_entries={counts['cache']} cache_bytes={bytes_copied['cache']}")
PY
}

write_dependency_manifest
materialize_dependency_cache

gradle_flags=(--no-build-cache --no-daemon --rerun-tasks --stacktrace)
if [[ "$gradle_offline" == 1 ]]; then
  gradle_flags+=(--offline)
fi

run_unsigned_build() {
  local cache=$1
  local tmp=$2
  local destination=$3
  export DOBBYVPN_GOMOBILE_GOCACHE="$cache"
  export DOBBYVPN_GOMOBILE_GOTMPDIR="$tmp"
  mkdir -p "$cache" "$tmp"
  (
    cd -- "$source_root"
    "$gradle_bin" -p kmp_module :app:assembleRelease \
      "${gradle_flags[@]}" \
      -PprojectRepositoryCommit="$source_commit" \
      -PprojectRepositoryCommitLink="https://github.com/DobbyVPN/DobbyVPN/tree/$source_commit" \
      -Pandroid.injected.version.code="$version_code" \
      -Pandroid.injected.version.name="$version_name"
  )
  built="$source_root/kmp_module/app/build/outputs/apk/release/app-release-unsigned.apk"
  [[ -f "$built" && ! -L "$built" ]] || { echo "Gradle did not produce $built" >&2; exit 1; }
  cp -- "$built" "$destination"
  chmod 600 "$destination"
  sync_path "$destination"
}

verify_source_integrity_after_build() {
  local observed_commit observed_tree relative line
  observed_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit})
  observed_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree})
  [[ "$observed_commit" == "$source_commit" && "$observed_tree" == "$source_tree" ]] || {
    echo 'source Git identity changed during the Android build' >&2
    exit 2
  }
  if ! "$git_bin" -C "$source_root" diff --quiet --no-ext-diff HEAD --; then
    echo 'Android build modified tracked source files' >&2
    exit 2
  fi
  if ! "$git_bin" -C "$source_root" diff --cached --quiet --no-ext-diff HEAD --; then
    echo 'Android build staged source modifications' >&2
    exit 2
  fi
  if [[ "$closure_mode" == 1 ]]; then
    SOURCE_ROOT="$source_root" GIT_BIN="$git_bin" CLOSURE_ALLOWLIST="$closure_allowlist_file" \
      OUTPUT_REL="${output#"$source_root/"}" FIRST_OUTPUT_REL="${first_output#"$source_root/"}" \
      REPRO_REL="${reproducibility#"$source_root/"}" python3 - <<'PY'
import os
import subprocess
from pathlib import Path

source = Path(os.environ["SOURCE_ROOT"]).resolve(strict=True)
git_bin = os.environ["GIT_BIN"]
allowed = set(Path(os.environ["CLOSURE_ALLOWLIST"]).read_text(encoding="utf-8").splitlines())
allowed.update({os.environ["OUTPUT_REL"], os.environ["FIRST_OUTPUT_REL"], os.environ["REPRO_REL"]})
allowed_prefixes = (
    ".android-build/",
    "kmp_module/build/",
    "kmp_module/.gradle/",
    "kmp_module/.kotlin/",
)

def is_allowed_generated_module_state(relative: str) -> bool:
    parts = Path(relative).parts
    return (
        len(parts) >= 3
        and parts[0] == "kmp_module"
        and parts[2] in {"build", ".gradle", ".kotlin"}
    )

def git_paths(*args: str) -> set[str]:
    completed = subprocess.run(
        [git_bin, "-C", str(source), "ls-files", "-z", *args],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if completed.returncode:
        raise SystemExit(completed.stderr.decode("utf-8", "replace"))
    raw = completed.stdout
    if raw and not raw.endswith(b"\x00"):
        raise SystemExit("Git returned a non-NUL-terminated path list")
    return {item.decode("utf-8") for item in raw.rstrip(b"\x00").split(b"\x00") if item}

paths = git_paths("--others", "--exclude-standard") | git_paths("--others", "--ignored", "--exclude-standard")
unexpected = []
for relative in sorted(paths):
    if (
        relative in allowed
        or any(relative.startswith(prefix) for prefix in allowed_prefixes)
        or is_allowed_generated_module_state(relative)
    ):
        continue
    if relative.endswith("/") and relative[:-1] in allowed:
        continue
    unexpected.append(relative)
if unexpected:
    raise SystemExit("Android build created undeclared source state: " + ", ".join(unexpected[:5]))
PY
    return 0
  fi
  while IFS= read -r -d '' relative; do
    case "$relative" in
      "${output#"$source_root/"}"|"${reproducibility#"$source_root/"}") ;;
      *)
        echo "Android build created an unexpected untracked source path: $relative" >&2
        exit 2
        ;;
    esac
  done < <("$git_bin" -C "$source_root" ls-files --others --exclude-standard -z)
  while IFS= read -r line; do
    [[ "$line" == '!! '* ]] || continue
    relative=${line#!! }
    case "$relative" in
      runtime|runtime/*|kmp_module/build|kmp_module/build/*|kmp_module/.gradle|kmp_module/.gradle/*|kmp_module/.kotlin|kmp_module/.kotlin/*|kmp_module/*/build|kmp_module/*/build/*|kmp_module/*/.gradle|kmp_module/*/.gradle/*|kmp_module/*/.kotlin|kmp_module/*/.kotlin/*) ;;
      *)
        echo "Android build created unexpected ignored source state: $relative" >&2
        exit 2
        ;;
    esac
  done < <("$git_bin" -C "$source_root" status --porcelain=v1 --ignored)
}

run_unsigned_build "$build_cache/first" "$build_tmp/first" "$first_output"
verify_source_integrity_after_build
verify_dependency_manifest
(
  cd -- "$source_root"
  "$gradle_bin" -p kmp_module clean --no-daemon --no-build-cache
)
run_unsigned_build "$build_cache/second" "$build_tmp/second" "$output"
verify_source_integrity_after_build

verify_dependency_manifest

[[ -f "$reproducibility_verifier" && ! -L "$reproducibility_verifier" ]] || { echo 'reproducibility verifier is missing' >&2; exit 2; }
python3 "$reproducibility_verifier" create \
  --first-apk "$first_output" --second-apk "$output" --output "$reproducibility" \
  --source-sha "$source_commit" --version-name "$version_name" --version-code "$version_code"
[[ -f "$source_verifier" && ! -L "$source_verifier" ]] || { echo 'APK source verifier is missing' >&2; exit 2; }
apkanalyzer_bin=${APKANALYZER:-"$(command -v apkanalyzer || true)"}
[[ -n "$apkanalyzer_bin" && -x "$apkanalyzer_bin" ]] || { echo 'apkanalyzer is required for source identity verification' >&2; exit 2; }
python3 "$source_verifier" --apk "$first_output" --apk "$output" --source-sha "$source_commit" --repository "$source_repository" --apkanalyzer "$apkanalyzer_bin"
verify_source_integrity_after_build

dependency_provenance_classification=tracked_dependency_spec
if [[ "$closure_mode" == 1 ]]; then
  dependency_provenance_classification=complete_owner_evidence
fi
SOURCE_ROOT="$source_root" OUTPUT="$output" MANIFEST="$manifest" FIRST_OUTPUT="$first_output" \
  REPRODUCIBILITY="$reproducibility" DEPENDENCY_MANIFEST="$dependency_manifest" \
  SOURCE_COMMIT="$source_commit" SOURCE_TREE="$source_tree" \
  DEPENDENCY_PROVENANCE_CLASSIFICATION="$dependency_provenance_classification" \
  VERSION_NAME="$version_name" VERSION_CODE="$version_code" MAX_ARTIFACT_BYTES="${DOBBYVPN_MAX_ARTIFACT_BYTES:-536870912}" \
  python3 - <<'PY'
import hashlib
import json
import os
import stat
from pathlib import Path

source_root = Path(os.environ["SOURCE_ROOT"])
output = Path(os.environ["OUTPUT"])
manifest = Path(os.environ["MANIFEST"])
first_output = Path(os.environ["FIRST_OUTPUT"])
reproducibility = Path(os.environ["REPRODUCIBILITY"])
dependency_manifest = Path(os.environ["DEPENDENCY_MANIFEST"])
maximum = int(os.environ["MAX_ARTIFACT_BYTES"])

def descriptor(path: Path) -> dict[str, object]:
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise SystemExit(f"descriptor is not a regular file: {path}")
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            size += len(chunk)
            if size > maximum:
                raise SystemExit(f"descriptor exceeds the configured artifact bound: {path}")
            digest.update(chunk)
    return {
        "path": path.relative_to(source_root).as_posix(),
        "sha256": digest.hexdigest(),
        "bytes": size,
    }

document = {
    "schema": 1,
    "repository": "DobbyVPN/DobbyVPN",
    "source_sha": os.environ["SOURCE_COMMIT"],
    "source_tree": os.environ["SOURCE_TREE"],
    "version_name": os.environ["VERSION_NAME"],
    "version_code": int(os.environ["VERSION_CODE"]),
    "package": "com.dobby.vpn",
    "signing_classification": "unsigned",
    "signer_certificate_sha256": None,
    "reproducibility": descriptor(reproducibility),
    "dependency_provenance": {
        "classification": os.environ["DEPENDENCY_PROVENANCE_CLASSIFICATION"],
        **descriptor(dependency_manifest),
    },
    "builds": {
        "first": descriptor(first_output),
        "second": descriptor(output),
    },
    "artifact": {
        "name": output.name,
        "sha256": descriptor(output)["sha256"],
        "bytes": descriptor(output)["bytes"],
        "package": "com.dobby.vpn",
    },
}
encoded = (json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n").encode()
descriptor = os.open(manifest, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
try:
    with os.fdopen(descriptor, "wb") as handle:
        descriptor = -1
        handle.write(encoded)
        handle.flush()
        os.fsync(handle.fileno())
finally:
    if descriptor >= 0:
        os.close(descriptor)
with manifest.open("rb") as stream:
    os.fsync(stream.fileno())
parent = os.open(manifest.parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
try:
    os.fsync(parent)
finally:
    os.close(parent)
PY

if [[ -n "$evidence_dir" ]]; then
  # Ensure both complete tee originals and the generated descriptors reach
  # stable storage before the caller can consume the build result.
  sync_path "$stdout_original"
  sync_path "$stderr_original"
fi

echo "android_build_driver status=passed source_commit=$source_commit source_tree=$source_tree artifact=$output"
