#!/usr/bin/env python3
"""Bounded macOS Accessibility lookup for the native UI smoke driver.

This helper deliberately runs in its own process.  A malformed or stale
Accessibility element must be killable by the caller; a timeout in the parent
therefore cannot leave the native UI protocol wedged behind one AX call.
Only the public ApplicationServices/CoreFoundation APIs are used.  No Fyne
or GLFW source is copied or patched here.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import math
import sys
import time
from collections.abc import Iterator


_CF_STRING_ENCODING_UTF8 = 0x08000100
_AX_SUCCESS = 0
_AX_ERROR_CANNOT_COMPLETE = -25204
_AX_ERROR_NO_VALUE = -25212
_AX_CGPOINT_TYPE = 1
_AX_CGSIZE_TYPE = 2
_AX_CGRECT_TYPE = 3
_MAX_DEPTH = 64
_MAX_NODES = 8192
_DEADLINE = 0.0
_AX_MESSAGE_TIMEOUT_SECONDS = 1.0


class AXLookupError(RuntimeError):
    def __init__(
        self,
        stage: str,
        message: str,
        *,
        transient: bool = False,
        details: dict[str, object] | None = None,
    ) -> None:
        super().__init__(message)
        self.stage = stage
        self.transient = transient
        self.details = details or {}


def _check_deadline() -> None:
    if _DEADLINE and time.monotonic() >= _DEADLINE:
        raise AXLookupError("deadline", "Accessibility lookup exceeded its deadline")


class CGPoint(ctypes.Structure):
    _fields_ = [("x", ctypes.c_double), ("y", ctypes.c_double)]


class CGSize(ctypes.Structure):
    _fields_ = [("width", ctypes.c_double), ("height", ctypes.c_double)]


class CGRect(ctypes.Structure):
    _fields_ = [("origin", CGPoint), ("size", CGSize)]


class Frameworks:
    def __init__(self) -> None:
        if sys.platform != "darwin":
            raise AXLookupError("platform", "macOS is required for AXUIElement lookup")
        try:
            self.core = ctypes.CDLL(
                "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
            )
            self.ax = ctypes.CDLL(
                "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"
            )
            self.cg = ctypes.CDLL(
                "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
            )
        except OSError as error:
            raise AXLookupError("framework", f"macOS accessibility frameworks unavailable: {error}") from error

        void_p = ctypes.c_void_p
        self.core.CFStringCreateWithCString.argtypes = [void_p, ctypes.c_char_p, ctypes.c_uint32]
        self.core.CFStringCreateWithCString.restype = void_p
        self.core.CFStringGetCString.argtypes = [void_p, ctypes.c_char_p, ctypes.c_long, ctypes.c_uint32]
        self.core.CFStringGetCString.restype = ctypes.c_bool
        self.core.CFGetTypeID.argtypes = [void_p]
        self.core.CFGetTypeID.restype = ctypes.c_ulong
        self.core.CFStringGetTypeID.argtypes = []
        self.core.CFStringGetTypeID.restype = ctypes.c_ulong
        self.core.CFArrayGetTypeID.argtypes = []
        self.core.CFArrayGetTypeID.restype = ctypes.c_ulong
        self.core.CFArrayGetCount.argtypes = [void_p]
        self.core.CFArrayGetCount.restype = ctypes.c_long
        self.core.CFArrayGetValueAtIndex.argtypes = [void_p, ctypes.c_long]
        self.core.CFArrayGetValueAtIndex.restype = void_p
        self.core.CFDictionaryGetValue.argtypes = [void_p, void_p]
        self.core.CFDictionaryGetValue.restype = void_p
        self.core.CFEqual.argtypes = [void_p, void_p]
        self.core.CFEqual.restype = ctypes.c_bool
        self.core.CFHash.argtypes = [void_p]
        self.core.CFHash.restype = ctypes.c_ulong
        self.core.CFNumberGetValue.argtypes = [void_p, ctypes.c_int, void_p]
        self.core.CFNumberGetValue.restype = ctypes.c_bool
        self.core.CFRelease.argtypes = [void_p]
        self.core.CFRelease.restype = None
        self.core.CFRetain.argtypes = [void_p]
        self.core.CFRetain.restype = void_p

        self.ax.AXUIElementCreateApplication.argtypes = [ctypes.c_int32]
        self.ax.AXUIElementCreateApplication.restype = void_p
        self.ax.AXUIElementCopyAttributeValue.argtypes = [void_p, void_p, ctypes.POINTER(void_p)]
        self.ax.AXUIElementCopyAttributeValue.restype = ctypes.c_int32
        self.ax.AXUIElementSetMessagingTimeout.argtypes = [void_p, ctypes.c_float]
        self.ax.AXUIElementSetMessagingTimeout.restype = ctypes.c_int32
        self.ax.AXValueGetType.argtypes = [void_p]
        self.ax.AXValueGetType.restype = ctypes.c_int
        self.ax.AXValueGetValue.argtypes = [void_p, ctypes.c_int, void_p]
        self.ax.AXValueGetValue.restype = ctypes.c_bool

        self.cg.CGWindowListCopyWindowInfo.argtypes = [ctypes.c_uint32, ctypes.c_uint32]
        self.cg.CGWindowListCopyWindowInfo.restype = void_p

    def string(self, value: str) -> ctypes.c_void_p:
        result = self.core.CFStringCreateWithCString(
            None, value.encode("utf-8"), _CF_STRING_ENCODING_UTF8
        )
        if not result:
            raise AXLookupError("attribute", f"could not create CoreFoundation key {value!r}")
        return result

    def release(self, value: ctypes.c_void_p | None) -> None:
        if value:
            self.core.CFRelease(value)

    def retain(self, value: ctypes.c_void_p | None) -> ctypes.c_void_p | None:
        return self.core.CFRetain(value) if value else None

    def text(self, value: ctypes.c_void_p) -> str | None:
        if self.core.CFGetTypeID(value) != self.core.CFStringGetTypeID():
            return None
        buffer = ctypes.create_string_buffer(4096)
        if not self.core.CFStringGetCString(
            value, buffer, len(buffer), _CF_STRING_ENCODING_UTF8
        ):
            return None
        return buffer.value.decode("utf-8", errors="replace")

    def array_values(self, value: ctypes.c_void_p) -> tuple[ctypes.c_void_p, ...]:
        if self.core.CFGetTypeID(value) != self.core.CFArrayGetTypeID():
            return ()
        count = self.core.CFArrayGetCount(value)
        if count < 0 or count > _MAX_NODES:
            raise AXLookupError("ax-tree", "Accessibility array has an invalid size")
        return tuple(self.core.CFArrayGetValueAtIndex(value, index) for index in range(count))


def _copy_attribute(
    frameworks: Frameworks,
    element: ctypes.c_void_p,
    name: str,
) -> tuple[int, ctypes.c_void_p | None]:
    _check_deadline()
    key = frameworks.string(name)
    result = ctypes.c_void_p()
    try:
        frameworks.ax.AXUIElementSetMessagingTimeout(
            element, _AX_MESSAGE_TIMEOUT_SECONDS
        )
        status = int(
            frameworks.ax.AXUIElementCopyAttributeValue(element, key, ctypes.byref(result))
        )
    finally:
        frameworks.release(key)
    return status, result.value


def _attribute(
    frameworks: Frameworks,
    element: ctypes.c_void_p,
    name: str,
) -> ctypes.c_void_p | None:
    status, value = _copy_attribute(frameworks, element, name)
    if status != _AX_SUCCESS:
        return None
    return value


def _frame(frameworks: Frameworks, element: ctypes.c_void_p) -> tuple[int, int, int, int] | None:
    value = _attribute(frameworks, element, "AXFrame")
    if value is None:
        return None
    try:
        if frameworks.ax.AXValueGetType(value) != _AX_CGRECT_TYPE:
            return None
        rect = CGRect()
        if not frameworks.ax.AXValueGetValue(value, _AX_CGRECT_TYPE, ctypes.byref(rect)):
            return None
        values = (
            rect.origin.x,
            rect.origin.y,
            rect.origin.x + rect.size.width,
            rect.origin.y + rect.size.height,
        )
        if not all(math.isfinite(number) for number in values):
            return None
        left, top, right, bottom = (int(round(number)) for number in values)
        if right <= left or bottom <= top:
            return None
        return left, top, right, bottom
    finally:
        frameworks.release(value)


def _element_texts(frameworks: Frameworks, element: ctypes.c_void_p) -> Iterator[str]:
    for attribute in ("AXTitle", "AXDescription", "AXIdentifier"):
        value = _attribute(frameworks, element, attribute)
        if value is None:
            continue
        try:
            text = frameworks.text(value)
            if text:
                yield text
        finally:
            frameworks.release(value)


def _children(frameworks: Frameworks, element: ctypes.c_void_p) -> tuple[ctypes.c_void_p, ...]:
    value = _attribute(frameworks, element, "AXChildren")
    if value is None:
        return ()
    try:
        # CFArray members are borrowed. Retain them while the array is alive;
        # the caller owns and releases the returned references after traversal.
        retained: list[ctypes.c_void_p] = []
        for child in frameworks.array_values(value):
            if not child:
                continue
            reference = frameworks.retain(child)
            if reference:
                retained.append(reference)
        return tuple(retained)
    finally:
        frameworks.release(value)


def _contains(outer: tuple[int, int, int, int], inner: tuple[int, int, int, int]) -> bool:
    return (
        inner[2] > outer[0]
        and inner[0] < outer[2]
        and inner[3] > outer[1]
        and inner[1] < outer[3]
    )


def _windows(frameworks: Frameworks, pid: int) -> tuple[ctypes.c_void_p, ...]:
    _check_deadline()
    app = frameworks.ax.AXUIElementCreateApplication(pid)
    if not app:
        raise AXLookupError("ax-application", "AXUIElementCreateApplication returned no application")
    try:
        status, value = _copy_attribute(frameworks, app, "AXWindows")
        if value is None:
            try:
                cg = _cg_window_diagnostics(frameworks, pid)
            except AXLookupError as error:
                cg = f"error:{error}"
            raise AXLookupError(
                "ax-windows",
                "the process exposed no AXWindows collection "
                f"(status={status}, cg={cg})",
                transient=status in {_AX_ERROR_CANNOT_COMPLETE, _AX_ERROR_NO_VALUE},
                details={"ax_status": status, "cg": cg},
            )
        try:
            result = frameworks.array_values(value)
            for window in result:
                frameworks.retain(window)
        finally:
            frameworks.release(value)
        if not result:
            try:
                cg = _cg_window_diagnostics(frameworks, pid)
            except AXLookupError as error:
                cg = f"error:{error}"
            raise AXLookupError(
                "ax-windows",
                f"the process exposed no windows (cg={cg})",
                transient=True,
                details={"cg": cg},
            )
        return result
    finally:
        frameworks.release(app)


def _cg_window_count(frameworks: Frameworks, pid: int, options: int = 1) -> int | None:
    """Return a CoreGraphics owner count for diagnostics only."""

    windows = frameworks.cg.CGWindowListCopyWindowInfo(options, 0)
    if not windows:
        return None
    try:
        key = frameworks.string("kCGWindowOwnerPID")
        try:
            count = 0
            for entry in frameworks.array_values(windows):
                _check_deadline()
                if not entry:
                    continue
                owner = frameworks.core.CFDictionaryGetValue(entry, key)
                if not owner:
                    continue
                observed = ctypes.c_int32()
                if frameworks.core.CFNumberGetValue(owner, 3, ctypes.byref(observed)) and observed.value == pid:
                    count += 1
            return count
        finally:
            frameworks.release(key)
    finally:
        frameworks.release(windows)


def _cg_window_diagnostics(frameworks: Frameworks, pid: int) -> dict[str, int | None]:
    """Return all-window and on-screen counts without becoming an interaction path."""

    return {
        "cg_window_count": _cg_window_count(frameworks, pid, 1),
        "cg_window_total_count": _cg_window_count(frameworks, pid, 0),
    }


def _window_probe(frameworks: Frameworks, pid: int) -> dict[str, object]:
    windows = _windows(frameworks, pid)
    try:
        bounds = None
        for window in windows:
            _check_deadline()
            candidate = _frame(frameworks, window)
            if candidate is not None:
                bounds = candidate
                break
        if bounds is None:
            raise AXLookupError("ax-window-frame", "AXWindows had no visible frame")
        return {
            "ok": True,
            "stage": "window",
            "bounds": list(bounds),
            "ax_window_count": len(windows),
            **_cg_window_diagnostics(frameworks, pid),
        }
    finally:
        for window in windows:
            frameworks.release(window)


def _find_control(
    frameworks: Frameworks,
    pid: int,
    name: str,
    prefix: bool,
) -> dict[str, object]:
    windows = _windows(frameworks, pid)
    try:
        seen: dict[int, list[ctypes.c_void_p]] = {}
        owned_elements: list[ctypes.c_void_p] = []
        matches: list[tuple[int, int, int, int]] = []
        match_frames: set[tuple[int, int, int, int]] = set()
        nodes = 0
        for window in windows:
            window_frame = _frame(frameworks, window)
            if window_frame is None:
                continue
            pending: list[tuple[ctypes.c_void_p, int, bool]] = [(window, 0, False)]
            while pending:
                _check_deadline()
                element, depth, owned = pending.pop()
                if owned:
                    owned_elements.append(element)
                if not element or depth > _MAX_DEPTH:
                    continue
                hash_value = int(frameworks.core.CFHash(element))
                bucket = seen.setdefault(hash_value, [])
                if any(frameworks.core.CFEqual(previous, element) for previous in bucket):
                    continue
                bucket.append(element)
                nodes += 1
                if nodes > _MAX_NODES:
                    raise AXLookupError("ax-tree", "Accessibility tree exceeded the node limit")
                frame = _frame(frameworks, element)
                if frame is not None and _contains(window_frame, frame):
                    for text in _element_texts(frameworks, element):
                        if (text.startswith(name) if prefix else text == name):
                            # The official Darwin bridge retains regenerated
                            # roots in a process-global array. A refresh can
                            # therefore expose the same logical control more
                            # than once. Collapse only exact same-frame
                            # duplicates; distinct controls remain ambiguous.
                            if frame not in match_frames:
                                match_frames.add(frame)
                                matches.append(frame)
                            if len(matches) > 1:
                                raise AXLookupError(
                                    "ambiguous",
                                    f"accessibility element {name!r} matched multiple controls",
                                )
                            break
                children = _children(frameworks, element)
                for child in reversed(children):
                    if child:
                        pending.append((child, depth + 1, True))
        if matches:
            return {
                "ok": True,
                "stage": "control",
                "bounds": list(matches[0]),
                "ax_window_count": len(windows),
                "nodes": nodes,
                **_cg_window_diagnostics(frameworks, pid),
            }
        raise AXLookupError("control", f"accessibility element {name!r} was not found")
    finally:
        for element in owned_elements:
            frameworks.release(element)
        for window in windows:
            frameworks.release(window)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pid", type=int, required=True)
    parser.add_argument("--name")
    parser.add_argument("--prefix", action="store_true")
    parser.add_argument("--window", action="store_true")
    parser.add_argument("--deadline", type=float, default=4.0)
    return parser


def main() -> int:
    global _DEADLINE
    args = _parser().parse_args()
    if args.pid <= 0:
        raise SystemExit("pid must be positive")
    if args.window == (args.name is not None):
        raise SystemExit("choose exactly one of --window or --name")
    if args.deadline <= 0 or not math.isfinite(args.deadline):
        raise SystemExit("deadline must be positive and finite")
    _DEADLINE = time.monotonic() + args.deadline
    started = time.monotonic()
    try:
        frameworks = Frameworks()
        if args.window:
            payload = _window_probe(frameworks, args.pid)
        else:
            payload = _find_control(frameworks, args.pid, args.name, args.prefix)
        payload["elapsed_ms"] = int((time.monotonic() - started) * 1000)
        print(json.dumps(payload, separators=(",", ":")), flush=True)
        return 0
    except AXLookupError as error:
        payload: dict[str, object] = {
            "ok": False,
            "stage": error.stage,
            "error": str(error),
            "elapsed_ms": int((time.monotonic() - started) * 1000),
        }
        if error.transient:
            payload["transient"] = True
        if error.details:
            payload["details"] = error.details
        print(json.dumps(payload, separators=(",", ":")), flush=True)
        return 1
    except Exception as error:  # pragma: no cover - defensive native boundary
        print(
            json.dumps(
                {
                    "ok": False,
                    "stage": "helper",
                    "error": f"{type(error).__name__}: {error}",
                    "elapsed_ms": int((time.monotonic() - started) * 1000),
                },
                separators=(",", ":"),
            ),
            flush=True,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
