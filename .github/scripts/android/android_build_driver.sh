#!/usr/bin/env bash
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1

# One Android builder: cached local iteration or reproducible release output.

source_root=''
source_sha=''
source_tree=''
output=''
manifest=''
first_output=''
test_companion_output=''
reproducibility=''
dependency_manifest=''
source_repository='DobbyVPN/DobbyVPN'
local_build=0
test_seams=0
trusted_archive_source=0
release_provenance_requested=0
gradle_archive=''
gradle_root=''
go_binary=''
go_build_origin=''
go_binary_sha256=''
gradle_distribution_source='wrapper_checksum'

while (($#)); do
  case "$1" in
    --source-root) source_root=${2:?missing --source-root value}; shift 2 ;;
    --source-sha) source_sha=${2:?missing --source-sha value}; shift 2 ;;
    --source-tree) source_tree=${2:?missing --source-tree value}; shift 2 ;;
    --output) output=${2:?missing --output value}; shift 2 ;;
    --manifest) manifest=${2:?missing --manifest value}; release_provenance_requested=1; shift 2 ;;
    --first-output) first_output=${2:?missing --first-output value}; release_provenance_requested=1; shift 2 ;;
    --test-companion-output) test_companion_output=${2:?missing --test-companion-output value}; shift 2 ;;
    --reproducibility) reproducibility=${2:?missing --reproducibility value}; release_provenance_requested=1; shift 2 ;;
    --dependency-manifest) dependency_manifest=${2:?missing --dependency-manifest value}; release_provenance_requested=1; shift 2 ;;
    --gradle-archive) gradle_archive=$2; shift 2 ;;
    --gradle-root) gradle_root=$2; shift 2 ;;
    --go-binary) go_binary=$2; shift 2 ;;
    --local) local_build=1; shift ;;
    --test-seams) test_seams=1; shift ;;
    --trusted-archive-source) trusted_archive_source=1; shift ;;
    --source-repository) source_repository=$2; release_provenance_requested=1; shift 2 ;;
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
[[ -z "$source_tree" || "$source_tree" =~ ^[0-9a-f]{40}$ ]] || {
  echo 'source tree must be a full lowercase Git tree identity' >&2
  exit 2
}
if [[ "$test_seams" == 1 && ( "$local_build" != 1 || -n "$source_sha" || -n "$source_tree" || "$trusted_archive_source" == 1 || "$release_provenance_requested" == 1 ) ]]; then
  echo '--test-seams requires --local and cannot use source identities, archived/trusted source, or Release provenance outputs' >&2
  exit 2
fi
if [[ "$local_build" == 1 && ( -n "$source_tree" || "$trusted_archive_source" == 1 ) ]]; then
  echo '--local cannot be combined with an archived source tree' >&2
  exit 2
fi
if [[ "$trusted_archive_source" == 1 && ( "$local_build" == 1 || -z "$source_sha" || -z "$source_tree" ) ]]; then
  echo '--trusted-archive-source requires --source-sha and --source-tree and cannot use --local' >&2
  exit 2
fi
if [[ "$trusted_archive_source" == 0 && -n "$source_tree" ]]; then
  echo '--source-tree requires --trusted-archive-source' >&2
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
dependency_spec="$source_root/.github/scripts/android/dependency-spec.json"
dependency_helper="$source_root/.github/scripts/android/android_dependency_provenance.py"
source_verifier="$source_root/.github/scripts/android/verify_android_apk_source.py"
reproducibility_verifier="$source_root/.github/scripts/android/verify_android_reproducibility.py"

# Keep Java's stderr in both the normal diagnostic stream and the command
# substitution used to read its version.
tee_stderr() {
  tee >(cat >&2)
}

git_bin=${GIT_BIN:-git}
gradle_bin=${GRADLE_BIN:-"$source_root/ui/android/gradlew"}

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

if [[ "$trusted_archive_source" == 1 ]]; then
  # Harness passes these identities only after validating the clean source
  # checkout before and after creating its .git-free source archive. The
  # archive mode carries that identity into the Android APK and provenance;
  # no .git directory is shipped to the runner.
  [[ ! -e "$source_root/.git" ]] || {
    echo 'trusted archived-source mode expects a source archive without .git metadata' >&2
    exit 2
  }
  source_commit=$source_sha
  source_commit_link="https://github.com/$source_repository/tree/$source_commit"
  source_identity_mode='harness_verified_archive'
elif [[ "$local_build" == 1 ]]; then
  # Fast local builds are not Release provenance claims. When the caller
  # supplies a source SHA, validate and embed it for CI build checks; ordinary
  # dirty local candidates remain explicitly identified as "local".
  if [[ -n "$source_sha" ]]; then
    validate_source_checkout "$source_root" "$source_sha"
    source_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit} | tee_stderr)
    source_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree} | tee_stderr)
    source_commit_link="https://github.com/$source_repository/tree/$source_commit"
    source_identity_mode='git_checkout'
  else
    source_commit=${GITHUB_SHA:-local}
    [[ "$source_commit" == local || "$source_commit" =~ ^[0-9a-f]{40}(-dirty)?$ ]] || {
      echo 'local build identity must be a commit SHA, optional -dirty suffix, or local' >&2
      exit 2
    }
    source_commit_link=''
    source_tree=''
    source_identity_mode='unverified_local_checkout'
  fi
else
  validate_source_checkout "$source_root" "$source_sha"
  source_commit=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{commit} | tee_stderr)
  source_tree=$("$git_bin" -C "$source_root" rev-parse --verify HEAD^{tree} | tee_stderr)
  source_commit_link="https://github.com/$source_repository/tree/$source_commit"
  source_identity_mode='git_checkout'
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
  gradle_distribution_source='external_verified_archive'
fi
gradle_proof_args=()
if [[ -n "$gradle_archive" ]]; then
  gradle_proof_args=(--gradle-archive "$gradle_archive" --gradle-root "$gradle_root")
fi
[[ -x "$gradle_bin" ]] || { echo "Gradle entry point is not executable: $gradle_bin" >&2; exit 2; }

cat "$source_root/ui/android/gradle.properties" >&2
version_name=$(sed -n 's/^versionName=//p' "$source_root/ui/android/gradle.properties")
version_code=$(sed -n 's/^versionCode=//p' "$source_root/ui/android/gradle.properties")
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
# Gradle owns the Go backend Exec tasks and overrides their environment. Pass
# this explicit mode from the driver so only --local --test-seams adds the Go
# build tag; inherited values cannot opt Release builds into test code.
export DOBBYVPN_BUILD_LOCAL="$local_build" DOBBYVPN_BUILD_TEST_SEAMS="$test_seams"
expected_go_version="go$(tr -d '[:space:]' < "$source_root/.go-version")"
go_version=$("$go_bin" env GOVERSION | tee_stderr)
[[ "$go_version" == "$expected_go_version" ]] || { echo 'Go version does not match .go-version' >&2; exit 2; }
go_root=$("$go_bin" env GOROOT | tee_stderr)
selected_go_path=$("$go_bin" env GOPATH | tee_stderr)
[[ -d "$go_root" && "$selected_go_path" == "$GOPATH" ]] || {
  echo 'Go environment does not match the selected tool inputs' >&2
  exit 2
}
if [[ "$trusted_archive_source" == 1 ]]; then
  # Local complete uses the version-pinned Go executable already installed by
  # source_checks. Record the selected binary's digest and do not claim the Go
  # source commit used by the hosted Release toolchain.
  go_build_origin='binary'
  go_binary_sha256=$(python3 - "$go_bin" <<'PY'
import hashlib
import sys

with open(sys.argv[1], "rb") as stream:
    print(hashlib.file_digest(stream, "sha256").hexdigest())
PY
)
elif [[ "$local_build" == 1 ]]; then
  # Cached local iteration does not emit dependency or Release provenance.
  go_build_origin='binary'
else
  pinned_go_commit=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-pin go_source_commit | tee_stderr)
  observed_go_commit=$("$git_bin" -C "$go_root" rev-parse --verify HEAD^{commit} | tee_stderr)
  [[ "$observed_go_commit" == "$pinned_go_commit" ]] || {
    echo 'Go source checkout does not match the approved source commit' >&2
    exit 2
  }
  go_build_origin='source_tree'
fi

mobile_version=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-pin mobile_version | tee_stderr)
observed_mobile_version=$(cd "$source_root/core" && "$go_bin" list -m -f '{{.Version}}' golang.org/x/mobile | tee_stderr)
[[ "$observed_mobile_version" == "$mobile_version" ]] || {
  echo 'Go module graph is not pinned to the approved x/mobile revision' >&2
  exit 2
}

build_tools_version=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-pin android_build_tools | tee_stderr)
ndk_version=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-pin android_ndk | tee_stderr)
[[ -n "${ANDROID_SDK_ROOT:-}" && -x "$ANDROID_SDK_ROOT/build-tools/$build_tools_version/apksigner" ]] || {
  echo "Android SDK/build-tools $build_tools_version apksigner is required" >&2
  exit 2
}
[[ -n "${ANDROID_NDK_HOME:-}" && -f "$ANDROID_NDK_HOME/source.properties" ]] || {
  echo "Android NDK $ndk_version is required" >&2
  exit 2
}
ndk_properties="$(cat "$ANDROID_NDK_HOME/source.properties")"
printf '%s\n' "$ndk_properties"
[[ "$ndk_properties" == *"Pkg.Revision = $ndk_version"* ]] || {
  echo "Android NDK revision is not $ndk_version" >&2
  exit 2
}
gradle_version=$("$gradle_bin" --version --no-daemon | tee_stderr | awk '/^Gradle / && !seen {version=$2; seen=1} END {if (seen) print version}')
expected_gradle_version=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-pin gradle_version | tee_stderr)
[[ "$gradle_version" == "$expected_gradle_version" ]] || { echo "Gradle version is not $expected_gradle_version" >&2; exit 2; }

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
  local go_origin_args=(--go-build-origin "$go_build_origin")
  if [[ -n "$go_binary_sha256" ]]; then
    go_origin_args+=(--go-binary-sha256 "$go_binary_sha256")
  fi
  python3 "$dependency_helper" --source-root "$source_root" --source-commit "$source_commit" \
    --source-tree "$source_tree" --spec "$dependency_spec" \
    --java-version "$java_version" "${gradle_proof_args[@]}" "${go_origin_args[@]}" \
    --output "$dependency_manifest"
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
java_major=$(python3 "$dependency_helper" --spec "$dependency_spec" --print-pin java_major | tee_stderr)
[[ "$java_version" == "$java_major".* ]] || { echo "Java runtime must have major version $java_major; observed $java_version" >&2; exit 2; }

verify_source_integrity_after_build() {
  if [[ "$trusted_archive_source" == 1 ]]; then
    # The Harness already checks the selected commit/tree before and after it
    # creates the source archive. This runner has only that trusted tarball,
    # so it records the externally validated pair instead of running Git
    # commands against unavailable object metadata.
    [[ "$source_commit" == "$source_sha" && "$source_tree" =~ ^[0-9a-f]{40}$ ]]
    [[ ! -e "$source_root/.git" ]] || {
      echo 'unexpected .git metadata appeared during the archived-source build' >&2
      exit 2
    }
    return
  fi
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

gradle_flags=(--no-build-cache --no-daemon --rerun-tasks --stacktrace --warning-mode=all)
run_unsigned_build() {
  local cache=$1 tmp=$2 destination=$3 built
  export GOCACHE="$cache" GOTMPDIR="$tmp"
  mkdir -p "$cache" "$tmp"
  ( cd -- "$source_root"
    "$gradle_bin" -p ui/android :app:assembleRelease "${gradle_flags[@]}" \
      -PprojectRepositoryCommit="$source_commit" -PprojectRepositoryCommitLink="$source_commit_link" \
      -PdobbyGoBinary="$go_bin" \
      -Pandroid.injected.version.code="$version_code" -Pandroid.injected.version.name="$version_name"
  )
  built="$source_root/ui/android/app/build/outputs/apk/release/app-release-unsigned.apk"
  [[ -f "$built" ]] || { echo "Gradle did not produce $built" >&2; exit 1; }
  cp -- "$built" "$destination"
}

run_test_companion_build() {
  local destination=$1 built="$source_root/ui/android/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk"
  ( cd -- "$source_root"
    "$gradle_bin" -p ui/android :app:assembleReleaseAndroidTest "${gradle_flags[@]}" \
      -PprojectRepositoryCommit="$source_commit" -PprojectRepositoryCommitLink="$source_commit_link" \
      -PdobbyGoBinary="$go_bin" \
      -Pandroid.injected.version.code="$version_code" -Pandroid.injected.version.name="$version_name"
  )
  [[ -f "$built" ]] || { echo "Gradle did not produce $built" >&2; exit 1; }
  cp -- "$built" "$destination"
}

if [[ "$local_build" == 1 ]]; then
  gradle_flags=(--no-daemon --stacktrace --warning-mode=all)
  run_unsigned_build "$build_cache/local" "$build_tmp/local" "$output"
  if [[ -n "$test_companion_output" ]]; then
    run_test_companion_build "$test_companion_output"
  fi
  if [[ -n "$source_sha" ]]; then
    verify_source_integrity_after_build
  fi
  echo "android_build_driver mode=local artifact=$output"
  exit 0
fi

run_unsigned_build "$build_cache/first" "$build_tmp/first" "$first_output"
( cd -- "$source_root"; "$gradle_bin" -p ui/android clean --no-daemon --no-build-cache )
run_unsigned_build "$build_cache/second" "$build_tmp/second" "$output"
if [[ -n "$test_companion_output" ]]; then
  run_test_companion_build "$test_companion_output"
fi
verify_source_integrity_after_build
write_dependency_manifest

[[ -f "$reproducibility_verifier" ]] || { echo 'reproducibility verifier is missing' >&2; exit 2; }
reproducibility_profile_args=()
if [[ "$trusted_archive_source" == 1 ]]; then
  reproducibility_profile_args=(
    --profile local-complete
    --source-root "$source_root"
    --go-root "$go_root"
    --gopath "$selected_go_path"
    --gradle-source "$gradle_distribution_source"
    --go-binary-sha256 "$go_binary_sha256"
  )
fi
python3 "$reproducibility_verifier" create --first-apk "$first_output" --second-apk "$output" \
  --output "$reproducibility" --source-sha "$source_commit" --version-name "$version_name" --version-code "$version_code" \
  "${reproducibility_profile_args[@]}"
[[ -f "$source_verifier" ]] || { echo 'APK source verifier is missing' >&2; exit 2; }
apkanalyzer_bin=${APKANALYZER:-"$(command -v apkanalyzer || true)"}
if [[ -z "$apkanalyzer_bin" ]]; then
  sdk_root=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}
  if [[ -n "$sdk_root" && -x "$sdk_root/cmdline-tools/latest/bin/apkanalyzer" ]]; then
    apkanalyzer_bin="$sdk_root/cmdline-tools/latest/bin/apkanalyzer"
  fi
fi
[[ -n "$apkanalyzer_bin" && -x "$apkanalyzer_bin" ]] || { echo 'apkanalyzer is required for source identity verification' >&2; exit 2; }
source_verifier_args=(
  --apk "$first_output" --apk "$output"
  --source-sha "$source_commit" --repository "$source_repository"
  --version-name "$version_name" --version-code "$version_code"
  --apkanalyzer "$apkanalyzer_bin"
)
if [[ -n "$test_companion_output" ]]; then
  source_verifier_args+=(--test-companion "$test_companion_output")
fi
python3 "$source_verifier" "${source_verifier_args[@]}"

# Verify the final Go backend libraries against the ABI policy. Both packaged
# ABIs include the TrustTunnel bridge and resolve its native runtime symbols.
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
python3 "$source_root/.github/scripts/android/verify_android_native_payloads.py" \
  --apk "$output" --readelf "$readelf_bin"

SOURCE_ROOT="$source_root" OUTPUT="$output" MANIFEST="$manifest" FIRST_OUTPUT="$first_output" \
  TEST_COMPANION_OUTPUT="$test_companion_output" REPRODUCIBILITY="$reproducibility" DEPENDENCY_MANIFEST="$dependency_manifest" \
  SOURCE_COMMIT="$source_commit" SOURCE_TREE="$source_tree" SOURCE_REPOSITORY="$source_repository" \
  SOURCE_IDENTITY_MODE="$source_identity_mode" \
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
    "source_identity_mode": os.environ["SOURCE_IDENTITY_MODE"],
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
