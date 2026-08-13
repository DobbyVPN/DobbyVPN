# Tracked tun2socks source

This directory is the complete Go source build closure from
`github.com/xjasonlyu/tun2socks/v2` tag `v2.6.0`, commit
`4127937ea7c450a5230b273f406c9410acec2be7`.
The four unused protocol reference text files and upstream repository/CI assets
are omitted; they are not embedded or read by the Go build.

DobbyVPN carries one production-source backport in
`core/device/fdbased/fd_unix.go`: make `FD.Close` idempotent so gVisor stack
teardown cannot close a raw descriptor number after the operating system has
reused it. This is the correction accepted upstream as pull request 495 and
merged in commit `8fe75611866e343bfa14fdfb80561c7bd49fdd3e`.

The root module replacement is intentional. A package-only replacement would
be ambiguous with the same package contained by the upstream root module, and
the next tagged upstream release requires a newer Go toolchain and includes
unrelated networking changes.
