# MacOS installer builder

## Dependencies

Requires theese file put in the current folder:

* `dobby-vpn-1.1-mac-aarch64.zip`
* `dobby-vpn-1.1-mac-amd64.zip`
* `grpcvpnserver`

## Build PKG

### Properties, that should be pre defined as environment variable

* APP_MAJOR_VERSION
* APP_MINOR_VERSION
* APP_MAINTENANCE_VERSION

### Build command

```bash
sh build.sh
```

Creates folders in this structure:

```
.
├── bin/
│   ├── aarch64
│   │   └── dobbyVPN-macos-aarch64.pkg
│   │   amd6464
└── └── └── dobbyVPN-macos-amd6464.pkg
```
