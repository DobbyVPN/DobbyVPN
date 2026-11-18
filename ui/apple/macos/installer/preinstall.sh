#!/bin/bash
set -euo pipefail

# Runs as root before the payload is copied: a refusal here leaves the Mac as it
# was. build.sh replaces the two @...@ placeholders for the package being built.
# The DOBBYVPN_* overrides exist only so these checks can be run without a real
# installation.

PACKAGE_ARCH="${DOBBYVPN_PACKAGE_ARCH:-@PACKAGE_ARCH@}"
PACKAGE_VERSION="${DOBBYVPN_PACKAGE_VERSION:-@PACKAGE_VERSION@}"
PLIST_DEST="${DOBBYVPN_DAEMON_PLIST:-/Library/LaunchDaemons/com.dobby.vpnservice.plist}"
SCUTIL="${DOBBYVPN_SCUTIL:-/usr/sbin/scutil}"
PKGUTIL="${DOBBYVPN_PKGUTIL:-/usr/sbin/pkgutil}"

# 1. The package must match the CPU. sysctl reports Apple silicon even when the
#    installer itself runs under Rosetta, where uname would say x86_64.
HOST_ARCH="${DOBBYVPN_HOST_ARCH:-}"
if [ -z "$HOST_ARCH" ]; then
    if [ "$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null || true)" = "1" ]; then
        HOST_ARCH="arm64"
    else
        HOST_ARCH="x86_64"
    fi
fi
if [ "$PACKAGE_ARCH" != "$HOST_ARCH" ]; then
    echo "This DobbyVPN package is built for $PACKAGE_ARCH, but this Mac is $HOST_ARCH." >&2
    echo "Download the package for this Mac (arm64 for Apple silicon, x86_64 for Intel) and run it again. Nothing was installed." >&2
    exit 1
fi

# 2. The service needs an owner (the user allowed to talk to it): the logged-in
#    user, or the owner recorded in an already installed service plist.
uid_is_usable() { [[ "$1" =~ ^[0-9]+$ ]] && [ "$1" -ne 0 ]; }
OWNER_UID="${DOBBYVPN_CONTROL_PEER_UID:-}"
if [ -z "$OWNER_UID" ]; then
    # A failing scutil must lead to the plist fallback, not end the script.
    OWNER_UID="$("$SCUTIL" <<'SCUTIL_INPUT' | /usr/bin/awk '/kCGSSessionUserIDKey[[:space:]]*:/ { sub(/.*:[[:space:]]*/, ""); print; exit }' || true
show State:/Users/ConsoleUser
SCUTIL_INPUT
)"
fi
if ! uid_is_usable "$OWNER_UID"; then
    OWNER_UID="$(/usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:DOBBYVPN_CONTROL_PEER_UID" "$PLIST_DEST" 2>/dev/null || true)"
fi
if ! uid_is_usable "$OWNER_UID"; then
    echo "Nobody is logged in at this Mac's screen and no earlier DobbyVPN installation was found, so the VPN service has no owner." >&2
    echo "Log in at the Mac and run the installer again. Nothing was installed." >&2
    exit 1
fi

# 3. An older package over a newer one is allowed, but it is probably a mistake.
INSTALLED_VERSION="$("$PKGUTIL" --pkg-info com.dobby.pkg 2>/dev/null | /usr/bin/awk '/^version:/ { print $2 }' || true)"
if [ -n "$INSTALLED_VERSION" ] && /usr/bin/awk -v a="$INSTALLED_VERSION" -v b="$PACKAGE_VERSION" '
    BEGIN {
        na = split(a, x, "."); nb = split(b, y, ".")
        for (i = 1; i <= (na > nb ? na : nb); i++) {
            if (x[i] + 0 > y[i] + 0) exit 0
            if (x[i] + 0 < y[i] + 0) exit 1
        }
        exit 1
    }'; then
    echo "Warning: DobbyVPN $INSTALLED_VERSION is installed and this package is the older $PACKAGE_VERSION. Installing it replaces the newer version." >&2
fi
