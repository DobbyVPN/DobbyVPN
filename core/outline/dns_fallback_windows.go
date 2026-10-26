//go:build windows

package outline

// Keep real server reachability visible to Windows so it can try the next DNS
// address family. Synthetic truncation makes an unreachable server look alive.
const forceTCPDNSForPlatform = false
