from __future__ import annotations

import ctypes
from pathlib import Path
import sys
from types import SimpleNamespace
from unittest import mock

from torturer_checks import windows_append


def test_windows_append_open_requests_only_append_data() -> None:
    path = Path("C:/synthetic/app.log")
    create_file = mock.Mock(return_value=8123)
    close_handle = mock.Mock(return_value=True)
    kernel32 = SimpleNamespace(
        CreateFileW=create_file,
        CloseHandle=close_handle,
    )
    open_osfhandle = mock.Mock(return_value=27)
    msvcrt = SimpleNamespace(open_osfhandle=open_osfhandle)

    with (
        mock.patch.object(windows_append.os, "name", "nt"),
        mock.patch.object(ctypes, "WinDLL", return_value=kernel32, create=True),
        mock.patch.dict(sys.modules, {"msvcrt": msvcrt}),
    ):
        descriptor = windows_append.open_existing_append(path)

    assert descriptor == 27
    assert create_file.call_count == 1
    arguments = create_file.call_args.args
    assert arguments[0] == str(path)
    assert arguments[1] == 0x00000004
    assert arguments[1] & 0x00000002 == 0
    assert arguments[2] == 0x00000003
    assert arguments[4] == 3
    assert close_handle.call_count == 0
    assert open_osfhandle.call_args.args[0] == 8123
