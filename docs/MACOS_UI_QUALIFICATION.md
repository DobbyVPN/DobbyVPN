# macOS native UI qualification

The macOS `full` lane adds one real-window AUTO journey after the portable
mini contract. It drives the production Go/Fyne app in the active Aqua
session and independently verifies the service, tunnel, routing, stability,
and traffic. CLI actions cannot stand in for visible UI input.

## Fail-closed preflight

The lane checks the same user and process context that will run automation:

1. the authoritative ConsoleUser name and UID match the SSH worker;
2. the matching `launchctl gui/<uid>` domain exists and the screen is unlocked;
3. System Events can inspect Finder, proving Accessibility for that context;
4. a disposable AppKit button is unobstructed, accepts a tagged
   CoreGraphics click, and can be captured as a nonblank image;
5. the Aqua checks are repeated immediately before the production driver.

These checks diagnose permissions and session state; they never edit TCC,
graphics, or accessibility configuration. A missing permission, obstructing
window, ambiguous identity, or malformed response fails the lane.

## Production-window rules

- Launch source-built executables in a disposable product-shaped `.app`.
  Installed Release bundles are used in place for exact-package runs.
- Bind every action and capture to the exact PID, executable, start time,
  AX window, and control frame. CoreGraphics window enumeration is diagnostic
  only; Accessibility remains the discovery and ambiguity authority.
- Official Fyne/GLFW remains unchanged. The app's process-local NSGL bridge
  first requests the normal accelerated format and retries the same format
  without only the accelerated flag when Apple's software renderer is the
  available implementation. Metal and accelerated graphics are not required.
- Keep the submitted profile on the clipboard until the physical Connect
  action is acknowledged, then restore the previous clipboard. A posted paste
  shortcut alone is not proof that Fyne consumed the value.
- On launch, `open -W -n` is only the LaunchServices opener, not the app
  process. A zero exit means the launch request was accepted; keep discovering
  the exact app process and native window until the existing deadline. Do not
  classify that zero exit as app termination or clean up a process whose
  identity has not yet been captured. A nonzero opener exit is a launch error.
- Use the standard Cmd+Q menu action for graceful close. GLFW supplies that
  command but does not supply a default Cmd+W close shortcut. Require both the
  exact app process and its `open` launcher to exit normally before reopen.
- Treat an explicit transient `AXWindows` startup response as retryable inside
  the existing deadline. Other AX failures, distinct duplicate controls, and
  helper timeouts remain hard failures.

Fyne can expose stale accessibility roots over repeated window generations.
The bounded AX helper examines only a newest-first root suffix, selects the
current root by its relationship to the exact window, collapses only exact
same-frame duplicates, and rejects distinct-frame ambiguity. The helper uses
public AX APIs with per-element deadlines and bounded depth/node counts.

## Tunnel teardown timing

macOS may keep the app-owned `utun` interface visible briefly after
`tun2socks` stops. AUTO profile selection can start the next candidate during
that interval, when reusing the interface name still fails with “resource
busy.” After stopping the device, the platform engine therefore waits for its
own interface to disappear (50 ms polling, bounded by 5 seconds) before
allowing the next candidate. Interface-list errors and a timeout remain
connection failures; they are not treated as successful cleanup.

## Diagnostics and cleanup

Forward complete command, launcher, app, AX-helper, service, timeout, and
cleanup streams before removing disposable files. Preserve the original
failure when cleanup or collection also fails. Never replace a stream with a
status, byte count, selected excerpt, or tail.

Required native milestones and failures produce exact-window PNG captures.
Configuration, log, and detail regions are masked before the file leaves the
test context. Validate full PNG framing, CRCs, dimensions, byte length, and
SHA-256. A capture or masking failure is an explicit test failure; screenshots
are additional diagnostics and never replace logs or assertions. They belong
to the one current run and follow the same retention and cleanup policy.

Cleanup targets the captured app identity even if the `open -W` launcher has
already exited. Forced TERM/KILL escalation is cleanup only and cannot satisfy
the graceful close assertion.
