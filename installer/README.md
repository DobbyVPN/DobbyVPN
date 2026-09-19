# Installer build scripts

This directory contains the scripts and configuration used to build Windows
and macOS installers.

## Folder Structure

```
installer/
├── windows/
│   ├── .gitignore 
│   ├── AppComponents.wxs 
│   ├── build.bat 
│   ├── Folders.wxs 
│   ├── Package.wxs 
│   └── README.md
│
├── macos/
│   ├── .gitignore
│   ├── build.sh
│   ├── postinstall.sh
│   ├── uninstall.sh
│   ├── README.md
│   └── vpnservice.plist
│
└── README.md
```

## Supported Platforms

| Platform | Output Format | Architecture | Status |
| --- | --- | --- | --- |
| Windows | `.msi` | amd64 | Supported |
| macOS 12+ | `.pkg` | amd64 | Supported |
| macOS 12+ | `.pkg` | aarch64 | Supported |

## Windows Installer

### Requirements

* Installer tool (WiX)

### Build

```powershell
cd windows/
./build.bat
```

### Environment variables

* APP_MAJOR_VERSION
* APP_MINOR_VERSION
* APP_MAINTENANCE_VERSION
* GITHUB_SHA
* GITHUB_REPOSITORY

### Output
```
installer/
└── windows/
    └── bin/
        └── amd64/
            └── dobbyVPN-windows-amd64.msi
```

## MacOS Installer

### Environment variables

* APP_MAJOR_VERSION
* APP_MINOR_VERSION
* APP_MAINTENANCE_VERSION

### Build

```bash
cd macos/
sh build.sh
```

The builder requires separate `services/arm64/macos_grpcvpnserver` and
`services/amd64/macos_grpcvpnserver` inputs. It checks their Mach-O
architectures before packaging.

### Output

```
installer/
├── macos/
│   ├── bin/
│   │   ├── amd64/
│   │   │   └── dobbyVPN-macos-amd64.pkg
│   │   └── aarch64/
│   │       └── dobbyVPN-macos-aarch64.pkg
```

## Notes

Each installer installs the application and its gRPC VPN service. Windows uses
the MSI uninstaller, which removes the service with the package. macOS installs
one fixed product-owned uninstaller at
`/usr/local/libexec/dobbyvpn-uninstall`; run it with `sudo` to stop and remove
the launchd service, plist, control socket, app bundle, and package receipt.

Release migration qualification downloads the published v1.5.0 package only
after checking the pinned entries in
`.github/scripts/installer_rollback_manifest.json`. It then proves fresh
install, upgrade, explicit uninstall-and-reinstall rollback, and final
uninstall for the exact v1.5.1 package on Windows and both macOS architectures.
The downloaded
rollback package is temporary and is removed when the check exits.

The macOS packages target macOS 12.0 or newer on both Intel and Apple-silicon
hosts. Release migration qualification also exercises the package's native
uninstall path; it does not require the old manual service-removal procedure.
