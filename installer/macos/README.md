# MacOS installer builder

The desktop GUI runs as its user; `com.dobby.vpnservice` is the root launchd
daemon. `postinstall.sh` is the single service installation path. Prepared
source builds may supply `DOBBYVPN_SERVICE_RESOURCES` and
`DOBBYVPN_SERVICE_PLIST` to use their built service and this same template,
plus `DOBBYVPN_CONTROL_PEER_UID` for the normal-user client. These inputs serve
source-built service checks and can be removed if those checks become
package-only. The normal package installation needs no overrides.

When `DOBBY_LOG_PATH` names a pre-created complete service log, the installer
also retains daemon stdout and stderr separately at that path plus `.stdout`
and `.stderr`. The run owner retains all three before removing its runtime.
Command-capturing callers may instead supply `DOBBY_SERVICE_STDOUT_PATH` and
`DOBBY_SERVICE_STDERR_PATH` to use their existing per-command output tree.
Those optional paths serve complete command-stream retention; remove them if
that consumer no longer requires caller-selected stream locations.
Source builds and packages use the same system control socket and launchd
lifecycle; neither requires running the GUI or test process as root.

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
