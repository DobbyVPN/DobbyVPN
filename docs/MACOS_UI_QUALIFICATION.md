# macOS native UI qualification

The macOS native-window journey is part of the interactive full suite. It
launches the packaged Dobby VPN app and exercises its visible controls while
the Go backend runs as the launchd service.

The runner must have a logged-in Aqua desktop owned by the configured test
user. The user must have Accessibility access for the smoke driver and
Screen Recording access for required window captures. SSH execution alone
does not establish that a usable desktop exists. If these conditions are
missing, the UI check is unavailable and the run fails.

The local runner verifies the console account, its GUI launchd session, and
that the display can produce a window capture before starting the app. The
smoke driver validates the app process and window, sends keyboard and pointer
input to accessible controls, and captures only the target app window. It
restores the clipboard after profile input. Qualification does not alter TCC
settings, machine-wide graphics configuration, or other users' desktop state.

See the [functional contract](../torturer/docs/contract.md) for the full-suite
coverage and [product testing](../TESTING.md) for supported commands.
