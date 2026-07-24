# MacOS installer builder

## Dependencies

Requires theese file put in the current folder:

* `dobbyVPN-macos-aarch64.zip`
* `dobbyVPN-macos-amd64.zip`
* `services/arm64/macos_grpcvpnserver`
* `services/amd64/macos_grpcvpnserver`
* `services/amd64/trusttunnel_client` (official TrustTunnelClient v1.0.49
  universal helper, checksum-verified by the desktop service workflow)

`build.sh` verifies the Mach-O architecture with `lipo` before inserting each
service. An amd64 package therefore fails to build if it is given an arm64
service binary. The amd64 package also places the validated official
`trusttunnel_client` helper beside its service; the Intel-only backend never
searches PATH for it.

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
