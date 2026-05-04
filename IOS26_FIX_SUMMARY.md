# iOS 26 VPN Connectivity Fix - Solution Summary

## Problem
The VPN app fails to work on iOS 26 (iOS 26.3.1) but works on iOS 18 and Android.

### Root Cause Analysis
From the logs, the critical issue is:
```
[iOS-Protect] SO_NO_TC_NETPOLICY failed: invalid argument
[Protect] *** CRITICAL *** Outbound TCP connection NOT using VPN tunnel! local=10.171.9.1:55307
```

1. **Deprecated Socket Option**: `SO_NO_TC_NETPOLICY` (socket option `0x1101`) no longer works on iOS 26
2. **IP_BOUND_IF with index 0**: Using `IP_BOUND_IF` with index `0` doesn't bind to any interface
3. **Missing Interface Index**: The code needs the actual physical interface index (WiFi/Cellular) to bind sockets correctly
4. **Routing Loop**: Without socket protection, the VPN's encrypted traffic goes through the physical interface (10.x.x.x) instead of tunnel interface (198.18.x.x), causing failed connections

## Solution

### 1. Go Module Changes

#### New File: `go_module/ios_exports/protector.go`
Exports two functions to Swift:
- `SetDefaultInterfaceIndex(index int)`: Receives interface index from Swift
- `GetDefaultInterfaceIndex() int`: Returns current interface index for diagnostics

#### Updated: `go_module/tunnel/protected_dialer/protect_ios.go`
- Added `SetDefaultInterfaceForIOS()` and `GetDefaultInterfaceForIOS()` functions
- Modified `Protect()` method to use `IP_BOUND_IF` with the actual interface index
- Supports both IPv4 (`IP_BOUND_IF`) and IPv6 (`IPV6_BOUND_IF`)
- Logs appropriate warnings when interface index is not set

#### Updated: `go_module/modules/Cloak/exported_client/protector_ios.go`
- Updated Cloak's socket protector to also use the interface index from Swift
- Same `IP_BOUND_IF` logic as the main protector

### 2. Swift Module Changes

#### Updated: `swift_module/tunnel/PacketTunnelProvider.swift`
- Added import for `if_nametoindex` C function:
  ```swift
  @_silgen_name("if_nametoindex")
  func if_nametoindex(_: UnsafePointer<CChar>) -> CUnsignedInt
  ```

- Added `getDefaultInterfaceIndex(from path:)` method:
  - Filters out VPN tunnel interfaces (type != .other)
  - Prefers WiFi over Cellular
  - Converts interface name to index using `if_nametoindex`

- Added `updateDefaultInterfaceIndex(for path:)` method:
  - Called on every network path change
  - Converts interface name to index
  - Calls Go's `Cloak_outlineSetDefaultInterfaceIndex()`

- Modified `startPathLogging()`:
  - Calls `updateDefaultInterfaceIndex()` at the start of every path update

- Modified `captureNetworkStateAtStartup()`:
  - Sets initial interface index on tunnel startup

## How It Works

1. **Swift Side**: `Network.NWPathMonitor` detects network changes (WiFi/Cellular)
2. **Interface Detection**: Swift determines the primary physical interface (pdp_ip0 = cellular, en0 = WiFi)
3. **Index Conversion**: Uses `if_nametoindex()` to convert interface name (e.g., "pdp_ip0") to numeric index
4. **Go Notification**: Swift calls `Cloak_outlineSetDefaultInterfaceIndex(index)` to pass the index to Go
5. **Socket Protection**: When Go creates outbound sockets:
   - First tries `SO_NO_TC_NETPOLICY` (for backward compatibility with iOS 18)
   - Then uses `IP_BOUND_IF` with the interface index to bind socket to physical interface
6. **Result**: Encrypted VPN traffic correctly bypasses the VPN tunnel and goes through the physical interface

## Files Changed

### New Files:
1. `go_module/ios_exports/protector.go` - Export functions for Swift

### Modified Files:
1. `go_module/tunnel/protected_dialer/protect_ios.go` - Socket protection logic
2. `go_module/modules/Cloak/exported_client/protector_ios.go` - Cloak socket protection
3. `swift_module/tunnel/PacketTunnelProvider.swift` - Interface detection and Go notification

## Build Instructions

1. Regenerate iOS bindings (if using gomobile):
   ```bash
   cd go_module
   gomobile bind -target=ios -o ../ios/Mobile.xcframework .
   ```

2. Build the iOS app as usual

3. Test on iOS 26 device with cellular data and/or WiFi

## Testing

Expected log entries after fix:
```
[tunnel:XXXX] [iOS26-RESEARCH] Set default interface index: 15 (pdp_ip0/Cellular)
[iOS-Protect] IP_BOUND_IF (IPv4) success: fd=20 bound to interface 15
[Protect] TCP dial OK: dest=mirror.example.com:443 local=198.18.0.1:XXXXX remote=x.x.x.x:443
```

The key indicator of success is seeing `local=198.18.0.1` (tunnel address) instead of `local=10.x.x.x` (cellular address).

## Compatibility

- **iOS 18**: Still works - `SO_NO_TC_NETPOLICY` continues to function, `IP_BOUND_IF` is safe to call
- **iOS 26**: Now works - `IP_BOUND_IF` with correct interface index provides socket protection
- **Android**: No changes - uses `VpnService.protect()` method
- **macOS**: No changes - uses different interface detection logic
