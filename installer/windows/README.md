# Windows MSI installer

## Prerequisites

* Wix v5
* Prebuilt application

Requires these files in the current folder:

* `dobbyVPN-windows.zip`
* `windows_grpcvpnserver.exe`
* `dobby_bridge.dll`
* `wintun.dll`

`build.bat` fails unless the import-derived service runtime is present, copies
the bridge beside `windows_grpcvpnserver.exe`, stages the checksum-pinned
Wintun DLL in the WiX payload tree, and builds only the amd64 MSI. The
installer workflow independently queries the MSI `File` table and rejects a
package that omits any member of that runtime closure.

The application ZIP must include both `bin\Dobby Vpn.exe` (the GUI launcher)
and `bin\dobby-cli.exe` (the console launcher used for machine-readable CLI
commands). The MSI installs both, but creates shortcuts only for the GUI.

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
└── bin/
    └── amd64/
        └── dobbyVPN-windows-amd64.msi
```
