# Windows MSI installer

The MSI packages the native WinUI frontend, operator CLI, and Go backend
Windows Service.

## Inputs

The installer build requires the Windows application archive and the backend
runtime closure:

- DobbyVPN.exe and dobby-cli.exe
- dobbyvpn-backend.exe
- dobby_bridge.dll
- wintun.dll

Build from this directory on Windows with WiX 5 installed:

    build.bat

The service is installed as DobbyVPN Go backend. The frontend sends JSON
requests to the fixed DobbyVPN.Control named pipe. The pipe access list grants
local access to the account that installed the MSI, using its SID, and rejects
remote clients.

The build produces the amd64 MSI under bin/amd64. APP_MAJOR_VERSION,
APP_MINOR_VERSION, and APP_MAINTENANCE_VERSION provide the product version.
