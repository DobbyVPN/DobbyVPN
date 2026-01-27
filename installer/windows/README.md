# Windows MSI installer

## Prerequisites

* Wix v5
* Prebuilt application

Requires theese file put in the current folder:

* `dobby-vpn-1.1-mac-aarch64.zip`
* `dobby-vpn-1.1-mac-amd64.zip`
* `grpcvpnserver`

### Install wix

#### Via command-line .NET tool

```bash
dotnet tool install --global wix
wix --version
```

## Build MSI

### Properties, that should be pre defined as environment variable

* APP_MAJOR_VERSION
* APP_MINOR_VERSION
* APP_MAINTENANCE_VERSION

### Build command

```bash
build.bat
```

Creates folders in this structure:

```
.
├── bin/
│   ├── amd64/
│   │   └── dobbyVPN-windows-amd64.msi
│   │   x86/
│   │   └── dobbyVPN-windows-x86.msi
│   │   arm64/
└── └── └── dobbyVPN-windows-arm64.msi
```
