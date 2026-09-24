#!/usr/bin/env bash
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1

# One Android builder: cached local iteration or reproducible release output.

source_root=''
source_sha=''
output=''
manifest=''
first_output=''
test_companion_output=''
reproducibility=''
dependency_manifest=''
source_repository='DobbyVPN/DobbyVPN'
local_build=0
gradle_archive=''
gradle_root=''
go_binary=''

while (($#)); do
  case "$1" in
    --source-root) source_root=${2:?missing --source-root value}; shift 2 ;;
    --source-sha) source_sha=${2:?missing --source-sha value}; shift 2 ;;
    --output) output=${2:?missing --output value}; shift 2 ;;
    --manifest) manifest=${2:?missing --manifest value}; shift 2 ;;
    --first-output) first_output=${2:?missing --first-output value}; shift 2 ;;
    --test-companion-output) test_companion_output=${2:?missing --test-companion-output value}; shift 2 ;;
    --reproducibility) reproducibility=${2:?missing --reproducibility value}; shift 2 ;;
    --dependency-manifest) dependency_manifest=${2:?missing --dependency-manifest value}; shift 2 ;;
    --gradle-archive) gradle_archive=$2; shift 2 ;;
    --gradle-root) gradle_root=$2; shift 2 ;;
    --go-binary) go_binary=$2; shift 2 ;;
    --local) local_build=1; shift ;;
    --source-repository) source_repository=$2; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$source_root" ]] || { echo '--source-root is required' >&2; exit 2; }
[[ -d "$source_root" ]] || { echo 'source root must be a directory' >&2; exit 2; }
source_root=$(cd -- "$source_root" && pwd -P)
[[ -z "$source_sha" || "$source_sha" =~ ^[0-9a-f]{40}$ ]] || {
  echo 'source SHA must be a full lowercase Git commit identity' >&2
  exit 2
}
if [[ "$local_build" == 1 && -n "$source_sha" ]]; then
  echo '--local cannot be combined with --source-sha' >&2
  exit 2
fi

[[ -n "$output" ]] || { echo '--output is required' >&2; exit 2; }
if [[ "$local_build" == 0 ]]; then
  [[ -n "$manifest" ]] || {
  echo '--manifest is required for a release build' >&2
  exit 2
  }
  first_output=${first_output:-"$source_root/.android-build/first.apk"}
  reproducibility=${reproducibility:-"$source_root/runtime/android-reproducibility.json"}
  dependency_manifest=${dependency_manifest:-"$source_root/runtime/android-dependency-provenance.json"}
fi
dependency_spec="$source_root/.github/android/dependency-spec.json"
dependency_helper="$source_root/.github/scripts/android_dependency_provenance.py"
source_verifier="$source_root/.github/scripts/verify_android_apk_source.py"
reproducibility_verifier="$source_root/.github/scripts/verify_android_reproducibility.py"

# Keep Java's stderr in both the normal diagnostic stream and the command
# substitution used to read its version.
tee_stderr() {
  tee >(cat >&2)
}

git_bin=${GIT_BIN:-git}
gradle_bin=${GRADLE_BIN:-"$source_root/android_module/gradlew"}

validate_source_checkout() {
  local root=$1
  local expected_commit=${2:-}
  local observed_commit untracked
  observed_commit=$("$git_bin" -C "$root" rev-parse --verify HEAD^{commit} | tee_stderr)
  [[ "$observed_commit" =~ ^[0-9a-f]{40}$ ]] || {
    echo "source checkout did not yield a canonical Git commit: $root" >&2
    exit 2
  }
  if [[ -n "$expected_commit" && "$observed_commit" != "$expected_commit" ]]; then
    echo "source checkout commit changed: expected $expected_commit got $observed_commit" >&2
    exit 2
  fi
  "$git_bin" -C "$root" diff --exit-code --no-ext-diff HEAD -- || {
    echo "source checkout has tracked worktree modifications: $root" >&2
    exit 2
  }
  "$git_bin" -C "$root" diff --cached --exit-code --no-ext-diff HEAD -- || {
    echo "source checkout has staged modifications: $root" >&2
    exit 2
  }
  untracked=$("$git_bin" -C "$root" ls-files --others --exclude-standard | tee_stderr)
  [[ -z "$untracked" ]] || {
    echo "source checkout has unexpected untracked files: $root" >&2
    exit 2
  }
}

if [[ "$local_build" == 1 ]]; then
  # Local builds are disposable, not release provenance claims.
  source_commit=local
  source_commit_link=''
else
  validate_source_checkout "$source_root" "$source_sha"
  source_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit} | tee_stderr)
  source_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree} | tee_stderr)
  source_commit_link="https://github.com/$source_repository/tree/$source_commit"
fi
[[ -f "$dependency_helper" && -f "$dependency_spec" && -f "$source_verifier" && -f "$reproducibility_verifier" ]] || {
  echo 'Android build helper or dependency specification is missing' >&2
  exit 2
}
if [[ -n "$gradle_archive" || -n "$gradle_root" ]]; then
  [[ -n "$gradle_archive" && -n "$gradle_root" ]] || {
    echo 'external Gradle archive and root proof must be supplied together' >&2
    exit 2
  }
  python3 "$dependency_helper" --spec "$dependency_spec" \
    --verify-gradle-distribution --gradle-archive "$gradle_archive" --gradle-root "$gradle_root"
  gradle_bin=${GRADLE_BIN:-"$gradle_root/bin/gradle"}
fi
gradle_proof_args=()
if [[ -n "$gradle_archive" ]]; then
  gradle_proof_args=(--gradle-archive "$gradle_archive" --gradle-root "$gradle_root")
fi
[[ -x "$gradle_bin" ]] || { echo "Gradle entry point is not executable: $gradle_bin" >&2; exit 2; }

cat "$source_root/android_module/gradle.properties" >&2
version_name=$(sed -n 's/^versionName=//p' "$source_root/android_module/gradle.properties")
version_code=$(sed -n 's/^versionCode=//p' "$source_root/android_module/gradle.properties")
[[ "$version_name" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$version_code" =~ ^[1-9][0-9]*$ ]] || {
  echo 'Android version properties are missing or invalid' >&2
  exit 2
}

go_bin=${go_binary:-${GO_BIN:-"$(command -v go || true)"}}
go_path=${GOPATH:-}
[[ -n "$go_bin" && -x "$go_bin" && -n "$go_path" && -d "$go_path" ]] || {
  echo 'the pinned Go executable and GOPATH are required' >&2
  exit 2
}
unset GOROOT
export GOPATH="$go_path" GOFLAGS='-trimpath -buildvcs=false' GOTOOLCHAIN='local'
expected_go_version="go$(tr -d '[:space:]' < "$source_root/.go-version")"
go_version=$("$go_bin" env GOVERSION | tee_stderr)
[[ "$go_version" == "$expected_go_version" ]] || { echo 'Go version does not match .go-version' >&2; exit 2; }
go_root=$("$go_bin" env GOROOT | tee_stderr)
selected_go_path=$("$go_bin" env GOPATH | tee_stderr)
[[ -d "$go_root" && "$selected_go_path" == "$GOPATH" ]] || {
  echo 'Go environment does not match the selected tool inputs' >&2
  exit 2
}

mobile_pin=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-mobile-version | tee_stderr)
mobile_module=${mobile_pin%@*}
mobile_version=${mobile_pin#*@}
[[ "$mobile_module" == 'golang.org/x/mobile' && "$mobile_version" == 'v0.0.0-20260520154334-0e4426e1883d' ]] || {
  echo 'dependency specification yielded an unexpected x/mobile pin' >&2
  exit 2
}
observed_mobile_version=$(cd "$source_root/go_module" && "$go_bin" list -m -f '{{.Version}}' golang.org/x/mobile | tee_stderr)
[[ "$observed_mobile_version" == "$mobile_version" ]] || {
  echo 'Go module graph is not pinned to the approved x/mobile revision' >&2
  exit 2
}

[[ -n "${ANDROID_SDK_ROOT:-}" && -x "$ANDROID_SDK_ROOT/build-tools/36.0.0/apksigner" ]] || {
  echo 'Android SDK/build-tools 36.0.0 apksigner is required' >&2
  exit 2
}
[[ -n "${ANDROID_NDK_HOME:-}" && -f "$ANDROID_NDK_HOME/source.properties" ]] || {
  echo 'Android NDK 27.3.13750724 is required' >&2
  exit 2
}
ndk_properties="$(cat "$ANDROID_NDK_HOME/source.properties")"
printf '%s\n' "$ndk_properties"
[[ "$ndk_properties" == *'Pkg.Revision = 27.3.13750724'* ]] || {
  echo 'Android NDK revision is not 27.3.13750724' >&2
  exit 2
}
gradle_version=$("$gradle_bin" --version --no-daemon | tee_stderr | awk '/^Gradle / && !seen {version=$2; seen=1} END {if (seen) print version}')
[[ "$gradle_version" == '8.13' ]] || { echo 'Gradle version is not 8.13' >&2; exit 2; }

build_cache=${DOBBYVPN_ANDROID_GO_CACHE:-"$source_root/.android-build/go-cache"}
build_tmp=${DOBBYVPN_ANDROID_GO_TMPDIR:-"$source_root/.android-build/go-tmp"}
build_mod_cache="$go_path/pkg/mod"
mkdir -p "$build_cache" "$build_tmp" "$build_mod_cache" "$(dirname -- "$output")"
if [[ "$local_build" == 0 ]]; then
  mkdir -p "$(dirname -- "$first_output")" "$(dirname -- "$manifest")" \
    "$(dirname -- "$reproducibility")" "$(dirname -- "$dependency_manifest")"
fi
export GOMODCACHE="$build_mod_cache"
if [[ -n "$test_companion_output" ]]; then
  mkdir -p "$(dirname -- "$test_companion_output")"
fi

write_dependency_manifest() {
  python3 "$dependency_helper" --source-root "$source_root" --source-commit "$source_commit" \
    --source-tree "$source_tree" --spec "$dependency_spec" \
    --java-version "$java_version" "${gradle_proof_args[@]}" --output "$dependency_manifest"
}
java_bin=${JAVA_BIN:-"$(command -v java || true)"}
[[ -n "$java_bin" && -x "$java_bin" ]] || { echo 'Java executable is required' >&2; exit 2; }
java_version_output=$("$java_bin" -version 2>&1 | tee_stderr)
java_version=''
while IFS= read -r java_line; do
  if [[ "$java_line" =~ version[[:space:]]+\"([^\"]+)\" ]]; then
    java_version=${BASH_REMATCH[1]}
    break
  fi
done <<< "$java_version_output"
[[ "$java_version" == 17.* ]] || { echo "Java runtime must have major version 17; observed $java_version" >&2; exit 2; }

verify_source_integrity_after_build() {
  observed_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit} | tee_stderr)
  observed_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree} | tee_stderr)
  [[ "$observed_commit" == "$source_commit" && "$observed_tree" == "$source_tree" ]] || {
    echo 'source Git tree identity changed during the Android build' >&2
    exit 2
  }
  "$git_bin" -C "$source_root" diff --exit-code --no-ext-diff HEAD -- || {
    echo 'Android build modified tracked source files' >&2
    exit 2
  }
  "$git_bin" -C "$source_root" diff --cached --exit-code --no-ext-diff HEAD -- || {
    echo 'Android build staged source modifications' >&2
    exit 2
  }
}

gradle_flags=(--no-build-cache --no-daemon --rerun-tasks --stacktrace)
run_unsigned_build() {
  local cache=$1 tmp=$2 destination=$3 built
  export GOCACHE="$cache" GOTMPDIR="$tmp"
  mkdir -p "$cache" "$tmp"
  ( cd -- "$source_root"
    "$gradle_bin" -p android_module :app:assembleRelease "${gradle_flags[@]}" \
      -PprojectRepositoryCommit="$source_commit" -PprojectRepositoryCommitLink="$source_commit_link" \
      -PdobbyGoBinary="$go_bin" \
      -Pandroid.injected.version.code="$version_code" -Pandroid.injected.version.name="$version_name"
  )
  built="$source_root/android_module/app/build/outputs/apk/release/app-release-unsigned.apk"
  [[ -f "$built" ]] || { echo "Gradle did not produce $built" >&2; exit 1; }
  cp -- "$built" "$destination"
}

run_test_companion_build() {
  local destination=$1 built="$source_root/android_module/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk"
  ( cd -- "$source_root"
    "$gradle_bin" -p android_module :app:assembleReleaseAndroidTest "${gradle_flags[@]}" \
      -PprojectRepositoryCommit="$source_commit" -PprojectRepositoryCommitLink="$source_commit_link" \
      -PdobbyGoBinary="$go_bin" \
      -Pandroid.injected.version.code="$version_code" -Pandroid.injected.version.name="$version_name"
  )
  [[ -f "$built" ]] || { echo "Gradle did not produce $built" >&2; exit 1; }
  cp -- "$built" "$destination"
}

if [[ "$local_build" == 1 ]]; then
  gradle_flags=(--no-daemon --stacktrace)
  run_unsigned_build "$build_cache/local" "$build_tmp/local" "$output"
  if [[ -n "$test_companion_output" ]]; then
    run_test_companion_build "$test_companion_output"
  fi
  echo "android_build_driver mode=local artifact=$output"
  exit 0
fi

run_unsigned_build "$build_cache/first" "$build_tmp/first" "$first_output"
( cd -- "$source_root"; "$gradle_bin" -p android_module clean --no-daemon --no-build-cache )
run_unsigned_build "$build_cache/second" "$build_tmp/second" "$output"
if [[ -n "$test_companion_output" ]]; then
  run_test_companion_build "$test_companion_output"
fi
verify_source_integrity_after_build
write_dependency_manifest

[[ -f "$reproducibility_verifier" ]] || { echo 'reproducibility verifier is missing' >&2; exit 2; }
python3 "$reproducibility_verifier" create --first-apk "$first_output" --second-apk "$output" \
  --output "$reproducibility" --source-sha "$source_commit" --version-name "$version_name" --version-code "$version_code"
[[ -f "$source_verifier" ]] || { echo 'APK source verifier is missing' >&2; exit 2; }
apkanalyzer_bin=${APKANALYZER:-"$(command -v apkanalyzer || true)"}
[[ -n "$apkanalyzer_bin" && -x "$apkanalyzer_bin" ]] || { echo 'apkanalyzer is required for source identity verification' >&2; exit 2; }
source_verifier_args=(--apk "$first_output" --apk "$output" --source-sha "$source_commit" --repository "$source_repository" --apkanalyzer "$apkanalyzer_bin")
if [[ -n "$test_companion_output" ]]; then
  source_verifier_args+=(--test-companion "$test_companion_output")
fi
python3 "$source_verifier" "${source_verifier_args[@]}"

# Verify the final Go backend libraries against the ABI policy. TrustTunnel
# bridge symbols are present only in arm64-v8a; x86_64 must remain a portable
# backend build with no unresolved native bridge dependency.
readelf_bin=${ANDROID_READELF:-}
if [[ -z "$readelf_bin" ]]; then
  mapfile -t ndk_toolchains < <(
    find "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt" -mindepth 1 -maxdepth 1 -type d -print
  )
  printf '%s\n' "${ndk_toolchains[@]}" >&2
  [[ "${#ndk_toolchains[@]}" -eq 1 ]] || {
    echo 'Android NDK must contain exactly one host toolchain' >&2
    exit 2
  }
  ndk_toolchain=${ndk_toolchains[0]}
  readelf_bin="$ndk_toolchain/bin/llvm-readelf"
fi
[[ -x "$readelf_bin" ]] || { echo "Android NDK llvm-readelf is required: $readelf_bin" >&2; exit 2; }
python3 "$source_root/.github/scripts/verify_android_native_payloads.py" \
  --apk "$output" --readelf "$readelf_bin"

SOURCE_ROOT="$source_root" OUTPUT="$output" MANIFEST="$manifest" FIRST_OUTPUT="$first_output" \
  TEST_COMPANION_OUTPUT="$test_companion_output" REPRODUCIBILITY="$reproducibility" DEPENDENCY_MANIFEST="$dependency_manifest" \
  SOURCE_COMMIT="$source_commit" SOURCE_TREE="$source_tree" SOURCE_REPOSITORY="$source_repository" \
  VERSION_NAME="$version_name" VERSION_CODE="$version_code" \
  python3 - <<'PY'
import hashlib
import json
import os
from pathlib import Path

source_root = Path(os.environ["SOURCE_ROOT"])
test_companion_value = os.environ.get("TEST_COMPANION_OUTPUT", "")

def descriptor(path: Path) -> dict[str, object]:
    if not path.is_file():
        raise SystemExit(f"build output is missing: {path}")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return {"path": path.relative_to(source_root).as_posix(), "sha256": digest.hexdigest(), "bytes": path.stat().st_size}

document = {
    "schema": 1,
    "repository": os.environ["SOURCE_REPOSITORY"],
    "source_sha": os.environ["SOURCE_COMMIT"],
    "source_tree": os.environ["SOURCE_TREE"],
    "version_name": os.environ["VERSION_NAME"],
    "version_code": int(os.environ["VERSION_CODE"]),
    "package": "com.dobby.vpn",
    "signing_classification": "unsigned",
    "signer_certificate_sha256": None,
    "reproducibility": descriptor(Path(os.environ["REPRODUCIBILITY"])),
    "dependency_provenance": {
        "classification": "tracked_dependency_spec",
        **descriptor(Path(os.environ["DEPENDENCY_MANIFEST"])),
    },
    "builds": {"first": descriptor(Path(os.environ["FIRST_OUTPUT"])), "second": descriptor(Path(os.environ["OUTPUT"]))},
    "artifact": {
        "name": Path(os.environ["OUTPUT"]).name,
        **descriptor(Path(os.environ["OUTPUT"])),
        "package": "com.dobby.vpn",
    },
    "test_companion": None if not test_companion_value else {
        "name": Path(test_companion_value).name,
        **descriptor(Path(test_companion_value)),
        "signing_classification": "unsigned",
    },
}
manifest = Path(os.environ["MANIFEST"])
manifest.parent.mkdir(parents=True, exist_ok=True)
manifest.write_text(json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
PY

echo "android_build_driver status=passed source_commit=$source_commit source_tree=$source_tree artifact=$output"
