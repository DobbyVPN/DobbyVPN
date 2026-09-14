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
│   ├── README.md 
│   └── vpnservice.plist
│
└── README.md
```

## Supported Platforms

| Platform | Output Format | Architecture | Status |
| --- | --- | --- | --- |
| Windows | `.msi` | amd64 | Supported |
| macOS | `.pkg` | amd64 | Supported |
| macOS | `.pkg` | aarch64 | Supported |

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

Each installer installs the application and its gRPC VPN service. The Windows
uninstaller removes the service. macOS has no uninstaller; remove the service
manually when uninstalling the app.
