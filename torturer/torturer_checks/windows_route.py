"""Bounded Windows best-route lookup for the functional routing probe."""

from __future__ import annotations

import ctypes
import ipaddress
import json
import socket
import sys


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


def main(argv: list[str] | None = None) -> int:
    values = sys.argv[1:] if argv is None else argv
    if len(values) != 1:
        print("expected one IPv4 destination", file=sys.stderr)
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
