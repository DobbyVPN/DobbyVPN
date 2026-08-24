#!/usr/bin/env bash
set -euo pipefail

# This is a deliberately thin public-source entry point.  The Android build
# contract remains in kmp_module's Gradle project; this file only supplies the
# reproducible task invocation and records the exact bytes it produced.  The
# private runner may provide GRADLE_BIN and GIT_BIN after verifying their
# request-bound closure.  Outside that runner, the checked-in Gradle wrapper
# remains the default.

source_root=''
output=''
manifest=''
first_output=''
reproducibility=''
dependency_manifest=''
dependency_closure=''
dependency_helper=''
source_verifier=''
reproducibility_verifier=''
source_repository='DobbyVPN/DobbyVPN'
tool_closure_manifest=${DOBBYVPN_ANDROID_TOOL_CLOSURE:-}
git_bin=${GIT_BIN:-git}
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
      shift 2
      ;;
    --reproducibility-verifier)
      (($# >= 2)) || { echo 'missing --reproducibility-verifier value' >&2; exit 2; }
      reproducibility_verifier=$2
      shift 2
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

[[ -n "$source_root" && -n "$output" && -n "$manifest" ]] || {
  echo '--source-root, --output, and --manifest are required' >&2
  exit 2
}
[[ -d "$source_root" && ! -L "$source_root" ]] || {
  echo 'source root must be a non-symlink directory' >&2
  exit 2
}
source_root=$(cd -- "$source_root" && pwd -P)
first_output=${first_output:-"$source_root/.android-build/first.apk"}
reproducibility=${reproducibility:-"$source_root/runtime/android-reproducibility.json"}
dependency_manifest=${dependency_manifest:-"$source_root/runtime/android-dependency-provenance.json"}
dependency_closure=${dependency_closure:-"${DOBBYVPN_ANDROID_DEPENDENCY_CLOSURE:-$source_root/.github/android/dependency-closure.json}"}
dependency_helper=${dependency_helper:-"$source_root/.github/scripts/android_dependency_provenance.py"}
source_verifier=${source_verifier:-"$source_root/.github/scripts/verify_android_apk_source.py"}
reproducibility_verifier=${reproducibility_verifier:-"$source_root/.github/scripts/verify_android_reproducibility.py"}

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

validate_tool_closure
gradle_bin=${GRADLE_BIN:-"$source_root/kmp_module/gradlew"}

source_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit})
source_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree})
[[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_tree" =~ ^[0-9a-f]{40}$ ]] || {
  echo 'source checkout did not yield canonical Git identities' >&2
  exit 2
}

# Report source-integrity failures before resolving optional owner-staged
# dependency helpers. This preserves the request-bound diagnostic contract and
# ensures a tampered checkout cannot be obscured by a missing helper fixture.
if ! "$git_bin" -C "$source_root" diff --quiet --no-ext-diff HEAD --; then
  echo 'source checkout has tracked worktree modifications' >&2
  exit 2
fi
if ! "$git_bin" -C "$source_root" diff --cached --quiet --no-ext-diff HEAD --; then
  echo 'source checkout has staged modifications' >&2
  exit 2
fi

# Resolve the helper through the exact source checkout before using it to
# inspect owner-staged state.  A lexical prefix such as /source/../outside is
# not an acceptable trust boundary.
SOURCE_ROOT="$source_root" DEPENDENCY_HELPER="$dependency_helper" python3 - <<'PY'
import os
from pathlib import Path

source = Path(os.environ["SOURCE_ROOT"]).resolve(strict=True)
helper = Path(os.environ["DEPENDENCY_HELPER"])
if not helper.is_absolute() or helper.is_symlink() or not helper.is_file():
    raise SystemExit("dependency helper must be a regular source-checkout file")
try:
    helper.resolve(strict=True).relative_to(source)
except ValueError as error:
    raise SystemExit("dependency helper must be inside the exact source checkout") from error
PY

# The closure parser emits only the closure file and its declared evidence and
# artifact paths.  Git state is checked against that exact allowlist below;
# the complete byte/digest binding still runs through the helper before any
# product build is started.
closure_allowlist_file=$(mktemp "${TMPDIR:-/tmp}/dobbyvpn-android-closure-allowlist.XXXXXX")
chmod 600 "$closure_allowlist_file"
python3 "$dependency_helper" \
  --source-root "$source_root" \
  --closure-evidence "$dependency_closure" \
  --print-staged-paths > "$closure_allowlist_file"

# The commit/tree pair is only meaningful when the checkout bytes are still
# exactly those objects.  Refuse tracked edits and every untracked/ignored
# path outside the declared owner closure before creating any build output;
# the runner's detached bundle materialization and workflow source copy are
# expected to be clean apart from this request-bound closure.
SOURCE_ROOT="$source_root" GIT_BIN="$git_bin" DEPENDENCY_CLOSURE="$dependency_closure" CLOSURE_ALLOWLIST="$closure_allowlist_file" python3 - <<'PY'
import os
import subprocess
from pathlib import Path

source = Path(os.environ["SOURCE_ROOT"]).resolve(strict=True)
git_bin = os.environ["GIT_BIN"]
allowlist_path = Path(os.environ["CLOSURE_ALLOWLIST"])
closure_path = Path(os.environ["DEPENDENCY_CLOSURE"])
allowed = set()
for line in allowlist_path.read_text(encoding="utf-8").splitlines():
    if not line or "\x00" in line or "\n" in line:
        raise SystemExit("closure allowlist contains an invalid path")
    relative = Path(line)
    if relative.is_absolute() or relative.as_posix() != line or any(part in {"", ".", ".."} for part in relative.parts):
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

untracked = git_paths("--others", "--exclude-standard")
ignored = git_paths("--others", "--ignored", "--exclude-standard")
staged = (untracked | ignored) & allowed
unexpected = sorted((untracked | ignored) - allowed)
if unexpected:
    raise SystemExit("source checkout contains undeclared untracked/ignored build state: " + ", ".join(unexpected[:5]))
if staged:
    closure_relative = closure_path.relative_to(source)
    closure_directory = source / closure_relative.parent
    directories = [closure_directory]
    for relative in staged:
        path = source / relative
        current = path.parent
        while current != closure_directory:
            directories.append(current)
            current = current.parent
        info = path.lstat()
        if path.is_symlink() or not path.is_file() or (info.st_mode & 0o077):
            raise SystemExit("owner-staged closure files must be regular owner-only files: " + relative)
    for directory in directories:
        info = directory.lstat()
        if directory.is_symlink() or not directory.is_dir() or (info.st_mode & 0o077):
            raise SystemExit("owner-staged closure directories must be real owner-only directories: " + str(directory))
PY
for helper in "$dependency_helper" "$source_verifier" "$reproducibility_verifier"; do
  case "$helper" in
    "$source_root"/*) [[ -f "$helper" && ! -L "$helper" ]] || { echo "source helper is missing or symlinked: $helper" >&2; exit 2; } ;;
    *) echo "source helper must be inside the exact source checkout: $helper" >&2; exit 2 ;;
  esac
done
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
[[ -x "$gomobile_bin" && -x "$gobind_bin" ]] || { echo 'exact gomobile and gobind executables are required' >&2; exit 2; }
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
for tool in "$GOMOBILE" "$GOBIND"; do
  # Keep the complete tool metadata visible before the predicates derive the
  # pinned-module assertion.  The command's stderr remains on stderr; stdout
  # is duplicated to stderr before grep consumes its derived view.
  "$go_bin" version -m "$tool" | tee /dev/stderr | grep -F 'golang.org/x/mobile' | grep -F 'v0.0.0-20260520154334-0e4426e1883d' >/dev/null || {
    echo "tool module closure is not the pinned golang.org/x/mobile revision: $tool" >&2
    exit 2
  }
done
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
gradle_version=$("$gradle_bin" --version --no-daemon | tee /dev/stderr | awk '/^Gradle / {print $2; exit}')
[[ "$gradle_version" == '8.13' ]] || { echo 'Gradle version is not 8.13' >&2; exit 2; }

gradle_offline=${DOBBYVPN_GRADLE_OFFLINE:-0}
if [[ "${DOBBYVPN_REQUIRE_TOOL_CLOSURE:-0}" == 1 ]]; then
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

python3 "$dependency_helper" --source-root "$source_root" --source-commit "$source_commit" --source-tree "$source_tree" --closure-evidence "$dependency_closure" --output "$dependency_manifest"

evidence_dir=${DOBBYVPN_BUILD_EVIDENCE_DIR:-}
stdout_original=''
stderr_original=''
if [[ -n "$evidence_dir" ]]; then
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
fi

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

run_unsigned_build "$build_cache/first" "$build_tmp/first" "$first_output"
(
  cd -- "$source_root"
  "$gradle_bin" -p kmp_module clean --no-daemon --no-build-cache
)
run_unsigned_build "$build_cache/second" "$build_tmp/second" "$output"

[[ -f "$reproducibility_verifier" && ! -L "$reproducibility_verifier" ]] || { echo 'reproducibility verifier is missing' >&2; exit 2; }
python3 "$reproducibility_verifier" create \
  --first-apk "$first_output" --second-apk "$output" --output "$reproducibility" \
  --source-sha "$source_commit" --version-name "$version_name" --version-code "$version_code"
[[ -f "$source_verifier" && ! -L "$source_verifier" ]] || { echo 'APK source verifier is missing' >&2; exit 2; }
apkanalyzer_bin=${APKANALYZER:-"$(command -v apkanalyzer || true)"}
[[ -n "$apkanalyzer_bin" && -x "$apkanalyzer_bin" ]] || { echo 'apkanalyzer is required for source identity verification' >&2; exit 2; }
python3 "$source_verifier" --apk "$first_output" --apk "$output" --source-sha "$source_commit" --repository "$source_repository" --apkanalyzer "$apkanalyzer_bin"

SOURCE_ROOT="$source_root" OUTPUT="$output" MANIFEST="$manifest" FIRST_OUTPUT="$first_output" \
  REPRODUCIBILITY="$reproducibility" DEPENDENCY_MANIFEST="$dependency_manifest" \
  SOURCE_COMMIT="$source_commit" SOURCE_TREE="$source_tree" \
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
        "classification": "complete_owner_evidence",
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
