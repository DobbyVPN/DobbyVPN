# Native UI

DobbyVPN uses SwiftUI on macOS and iOS, Jetpack Compose on Android, and WinUI 3
on Windows. Linux has no GUI.

The screens show the Go backend's current session snapshot and send its
configuration and connection commands. Go remains the only owner of profile
parsing, automatic selection, recovery, connection state, and tunnel cleanup.
Desktop frontends use the local JSON socket or Windows named pipe described in
[the architecture page](ARCHITECTURE.md). Android and iOS call the same Go
session API through narrow native bindings.

Go returns the accepted configuration URL in Snapshot, and owns its saved
copy. The UI also keeps an accepted inline configuration visible while the
current app session remains alive; inline text is not stored as a saved
configuration and does not replace the last saved URL. Reset clears that URL.
The UI reads local diagnostic files directly and presents the contents as
written. Product logs are not sanitized.

Native UI checks use rendered controls on the target platform. Windows and
macOS native-window interaction is part of their interactive full suite.
Android mini uses a rendered emulator, and the iOS Simulator mini suite checks
its rendered UI and app lifecycle without claiming VPN traffic. Linux checks
the backend and CLI only. The authoritative functional coverage is in
[the functional contract](../torturer/docs/contract.md).
