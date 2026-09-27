"""Bounded Windows best-route lookup for the functional routing probe."""

from __future__ import annotations

import ctypes
import ipaddress
import json
import socket
import sys


class _Guid(ctypes.Structure):
    _fields_ = [
        ("Data1", ctypes.c_uint32),
        ("Data2", ctypes.c_uint16),
        ("Data3", ctypes.c_uint16),
        ("Data4", ctypes.c_uint8 * 8),
    ]


class _NetLuid(ctypes.Union):
    _fields_ = [("Value", ctypes.c_uint64)]


class _MibIfRow2(ctypes.Structure):
    """Windows MIB_IF_ROW2 layout, with WCHAR represented portably as uint16."""

    _fields_ = [
        ("InterfaceLuid", _NetLuid),
        ("InterfaceIndex", ctypes.c_uint32),
        ("InterfaceGuid", _Guid),
        ("Alias", ctypes.c_uint16 * 257),
        ("Description", ctypes.c_uint16 * 257),
        ("PhysicalAddressLength", ctypes.c_uint32),
        ("PhysicalAddress", ctypes.c_uint8 * 32),
        ("PermanentPhysicalAddress", ctypes.c_uint8 * 32),
        ("Mtu", ctypes.c_uint32),
        ("Type", ctypes.c_uint32),
        ("TunnelType", ctypes.c_uint32),
        ("MediaType", ctypes.c_uint32),
        ("PhysicalMediumType", ctypes.c_uint32),
        ("AccessType", ctypes.c_uint32),
        ("DirectionType", ctypes.c_uint32),
        ("InterfaceAndOperStatusFlags", ctypes.c_uint8),
        ("OperStatus", ctypes.c_uint32),
        ("AdminStatus", ctypes.c_uint32),
        ("MediaConnectState", ctypes.c_uint32),
        ("NetworkGuid", _Guid),
        ("ConnectionType", ctypes.c_uint32),
        ("TransmitLinkSpeed", ctypes.c_uint64),
        ("ReceiveLinkSpeed", ctypes.c_uint64),
        ("InOctets", ctypes.c_uint64),
        ("InUcastPkts", ctypes.c_uint64),
        ("InNUcastPkts", ctypes.c_uint64),
        ("InDiscards", ctypes.c_uint64),
        ("InErrors", ctypes.c_uint64),
        ("InUnknownProtos", ctypes.c_uint64),
        ("InUcastOctets", ctypes.c_uint64),
        ("InMulticastOctets", ctypes.c_uint64),
        ("InBroadcastOctets", ctypes.c_uint64),
        ("OutOctets", ctypes.c_uint64),
        ("OutUcastPkts", ctypes.c_uint64),
        ("OutNUcastPkts", ctypes.c_uint64),
        ("OutDiscards", ctypes.c_uint64),
        ("OutErrors", ctypes.c_uint64),
        ("OutUcastOctets", ctypes.c_uint64),
        ("OutMulticastOctets", ctypes.c_uint64),
        ("OutBroadcastOctets", ctypes.c_uint64),
        ("OutQLen", ctypes.c_uint64),
    ]


def best_ipv4_interface(address: str) -> int:
    """Return Windows' best-route interface index for one IPv4 destination."""

    destination = ipaddress.IPv4Address(address)
    packed = socket.inet_aton(str(destination))
    # IPAddr has the same byte representation as inet_addr's result. ctypes
    # passes an integer by value, so interpret the packed bytes in host order.
    encoded = int.from_bytes(packed, sys.byteorder)
    api = ctypes.WinDLL("iphlpapi", use_last_error=True)
    get_best_interface = api.GetBestInterface
    get_best_interface.argtypes = (ctypes.c_uint32, ctypes.POINTER(ctypes.c_uint32))
    get_best_interface.restype = ctypes.c_uint32
    index = ctypes.c_uint32(0)
    status = get_best_interface(encoded, ctypes.byref(index))
    if status != 0 or index.value == 0:
        raise OSError(status, "GetBestInterface failed")
    return index.value


def interface_counters(interface_index: int) -> tuple[int, int, int]:
    """Return interface index, received bytes, and sent bytes from IP Helper."""

    if (
        isinstance(interface_index, bool)
        or not isinstance(interface_index, int)
        or not 0 < interface_index <= 0xFFFFFFFF
    ):
        raise ValueError("interface index must be a positive 32-bit integer")

    api = ctypes.WinDLL("iphlpapi", use_last_error=True)
    get_if_entry = api.GetIfEntry2
    get_if_entry.argtypes = (ctypes.POINTER(_MibIfRow2),)
    get_if_entry.restype = ctypes.c_uint32

    row = _MibIfRow2()
    row.InterfaceIndex = interface_index
    status = get_if_entry(ctypes.byref(row))
    if status != 0:
        raise OSError(status, f"GetIfEntry2 failed for interface {interface_index}")
    if row.InterfaceIndex != interface_index:
        raise OSError(
            "GetIfEntry2 returned a different interface index "
            f"({row.InterfaceIndex} != {interface_index})"
        )
    return row.InterfaceIndex, row.InOctets, row.OutOctets


def main(argv: list[str] | None = None) -> int:
    values = sys.argv[1:] if argv is None else argv
    if len(values) == 2 and values[0] == "--counters":
        try:
            index, received, sent = interface_counters(int(values[1]))
        except Exception as error:
            print(
                f"interface counter lookup failed: {type(error).__name__}: {error}",
                file=sys.stderr,
            )
            return 1
        print(
            json.dumps(
                {
                    "interface_index": index,
                    "received_bytes": received,
                    "sent_bytes": sent,
                }
            )
        )
        return 0
    if len(values) != 1:
        print("expected one IPv4 destination or --counters <interface index>", file=sys.stderr)
        return 2
    try:
        index = best_ipv4_interface(values[0])
    except Exception as error:
        print(f"best-route lookup failed: {type(error).__name__}: {error}", file=sys.stderr)
        return 1
    print(json.dumps({"interface_index": index}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
