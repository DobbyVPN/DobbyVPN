# MacOS installer builder

The package supports macOS 12.0 and newer on both Intel (`amd64`) and
Apple-silicon (`arm64`) hosts. The package's Go/Fyne GUI runs as the logged-in
user while the VPN service runs as the root launchd daemon.

The desktop GUI runs as its user; `com.dobby.vpnservice` is the root launchd
daemon. `postinstall.sh` is the single service installation path. Prepared
source builds may supply `DOBBYVPN_SERVICE_RESOURCES` and
`DOBBYVPN_SERVICE_PLIST` to use their built service and this same template,
plus `DOBBYVPN_CONTROL_PEER_UID` for the normal-user client. The local
source-build service checks use these inputs to install the built service and
template. The normal package installation needs no overrides.

When `DOBBY_LOG_PATH` names a pre-created complete service log, the installer
also retains daemon stdout and stderr separately at that path plus `.stdout`
and `.stderr`. The run owner retains all three before removing its runtime.
The local service checks supply `DOBBY_SERVICE_STDOUT_PATH` and
`DOBBY_SERVICE_STDERR_PATH` to retain daemon output in their per-command result
tree. The normal package installation uses the service-log path defaults.
Source builds and packages use the same system control socket and launchd
lifecycle; neither requires running the GUI or test process as root.

## Dependencies

Requires the following files in the current folder:

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

The package also installs `/usr/local/libexec/dobbyvpn-uninstall`. This is the
single supported uninstall path: run it with `sudo` to stop
`com.dobby.vpnservice`, remove its launchd plist and control socket, forget the
package receipt, and remove the app bundle. It accepts no path arguments.

## Build PKG

### Properties to define as environment variables

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
│   ├── aarch64/
│   │   └── dobbyVPN-macos-aarch64.pkg
│   └── amd64/
│       └── dobbyVPN-macos-amd64.pkg
```
