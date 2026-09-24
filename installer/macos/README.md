# macOS installer builder

The package supports macOS 12 or newer on Intel and Apple-silicon hosts. It
installs the SwiftUI app and the Go backend as the root launchd daemon
com.dobby.vpnservice. The frontend runs as the logged-in user and connects to
the backend through a local JSON control socket.

## Inputs

The build consumes matching backend and frontend files for arm64 and amd64.
The Intel backend package also includes the pinned TrustTunnelClient helper.
The installer verifies executable architectures before packaging them.

Prepared local builds can supply backend, plist, socket peer user, and log
locations through the environment variables used by the installer scripts.
The normal package installation uses the fixed product paths and needs no
overrides.

The package also installs /usr/local/libexec/dobbyvpn-uninstall. Run it with
sudo to stop the launchd daemon and remove the plist, control socket, package
receipt, and app bundle.

## Build

Run on macOS with the required command line tools:

    sh build.sh

The package outputs are written under bin/amd64 and bin/aarch64. The version is
set by APP_MAJOR_VERSION, APP_MINOR_VERSION, and APP_MAINTENANCE_VERSION.
