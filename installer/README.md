# Installer build scripts

This directory contains the Windows MSI and macOS PKG build scripts.

## Supported packages

| Platform | Package | Architecture |
| --- | --- | --- |
| Windows | MSI | amd64 |
| macOS 12 or newer | PKG | amd64 and arm64 |

## Windows

The Windows installer consumes the application archive and the Go backend
runtime closure: dobbyvpn-backend.exe, dobby_bridge.dll, and wintun.dll. The
installer places the native WinUI frontend and operator CLI in the app folder
and installs the Go backend as the DobbyVPN Go backend Windows Service.

Build from installer/windows on a Windows host with WiX 5 installed:

    build.bat

The version and source identity are supplied through APP_MAJOR_VERSION,
APP_MINOR_VERSION, APP_MAINTENANCE_VERSION, GITHUB_SHA, and GITHUB_REPOSITORY.

## macOS

The macOS installer consumes one backend for each supported architecture and
the matching SwiftUI app archive. The Intel package also includes the pinned
TrustTunnel helper. Build on macOS with Xcode command line tools:

    sh build.sh

The package installs the SwiftUI app, Go backend and CLI, and a launchd daemon.
The fixed JSON control socket is under /var/run/dobbyvpn. The supported
uninstall path is /usr/local/libexec/dobbyvpn-uninstall; run it with sudo to
stop and remove the service, plist, socket, app bundle, and package receipt.

## Release migration checks

Release qualifies fresh install, upgrade, rollback, and uninstall for the
exact packages built in that Release run. The previous package used for upgrade
or rollback is selected and verified through
.github/scripts/installer_rollback_manifest.json. Migration checks run on
Windows and both macOS architectures.
