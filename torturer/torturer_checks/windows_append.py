from __future__ import annotations

import os
from pathlib import Path


def open_existing_append(path: Path) -> int:
    """Open one existing Windows file with append-data, never write-data."""

    if os.name != "nt":
        return os.open(
            path,
            os.O_WRONLY | os.O_APPEND | getattr(os, "O_CLOEXEC", 0),
        )

    import ctypes
    import msvcrt
    from ctypes import wintypes

    file_append_data = 0x00000004
    file_share_read = 0x00000001
    file_share_write = 0x00000002
    open_existing = 3
    file_attribute_normal = 0x00000080

    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    create_file = kernel32.CreateFileW
    create_file.argtypes = (
        wintypes.LPCWSTR,
        wintypes.DWORD,
        wintypes.DWORD,
        wintypes.LPVOID,
        wintypes.DWORD,
        wintypes.DWORD,
        wintypes.HANDLE,
    )
    create_file.restype = wintypes.HANDLE
    close_handle = kernel32.CloseHandle
    close_handle.argtypes = (wintypes.HANDLE,)
    close_handle.restype = wintypes.BOOL

    handle = create_file(
        str(path),
        file_append_data,
        file_share_read | file_share_write,
        None,
        open_existing,
        file_attribute_normal,
        None,
    )
    invalid_handle = ctypes.c_void_p(-1).value
    if handle == invalid_handle:
        error = ctypes.get_last_error()
        raise OSError(error, "append-only Windows log open failed", str(path))
    try:
        return msvcrt.open_osfhandle(
            handle,
            os.O_WRONLY | os.O_APPEND | getattr(os, "O_BINARY", 0),
        )
    except BaseException:
        close_handle(handle)
        raise
