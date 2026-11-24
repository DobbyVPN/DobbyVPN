#!/bin/bash
set -euo pipefail

configuration="${1:?missing Debug or Release configuration}"
output="${2:?missing Apple output directory}"
case "$configuration" in
  Debug) suffix=-debug ;;
  Release) suffix= ;;
  *) echo "Invalid Apple build configuration: $configuration" >&2; exit 2 ;;
esac
export DOBBY_BUILD_CONFIGURATION="$configuration"

# gomobile writes a fixed path. Move each framework out of the checkout
# before building the matching archive and before starting the next variant.
(cd core && ./scripts/build_ios_xcframework.sh)
mkdir -p "$output/$configuration"
runtime="$output/$configuration/DobbyVPNRuntime.xcframework"
mv core/DobbyVPNRuntime.xcframework "$runtime"
test -z "$(git status --porcelain --untracked-files=all)"
bash core/scripts/package_ios_app.sh iosarchive "$output/DobbyVPN.xcarchive$suffix.tar.gz" "$runtime"
