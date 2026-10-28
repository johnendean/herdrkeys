"""The parts of IOKit and ImageIO the CN001 needs, through ctypes.

hidapi and Pillow would do this, but they are compiled dependencies, which is
exactly what ADR 0003 keeps out. The plugin is macOS-only, and macOS ships a
HID stack and a JPEG encoder as system frameworks, so this is the whole of what
herdrkeys needs from them. See ADR 0015.

Every signature here is declared, never left to ctypes' int default: a pointer
truncated to 32 bits is a crash, not an exception.
"""

from __future__ import annotations

import ctypes
import ctypes.util
import logging
import os
import threading
from dataclasses import dataclass

log = logging.getLogger("herdrkeys")

_vp = ctypes.c_void_p
_long = ctypes.c_long  # CFIndex
_u32 = ctypes.c_uint32
_i32 = ctypes.c_int32  # IOReturn

_UTF8 = 0x08000100
_CFNUMBER_LONG = 10  # kCFNumberLongType
_REPORT_OUTPUT = 1  # kIOHIDReportTypeOutput
_OPTIONS_NONE = 0  # kIOHIDOptionsTypeNone: never seize the device
_ALPHA_NONE_SKIP_LAST = 5  # kCGImageAlphaNoneSkipLast: RGBX, X ignored

# void (*)(void *context, IOReturn, void *sender, IOHIDReportType, uint32_t
#          reportID, uint8_t *report, CFIndex length)
_REPORT_CALLBACK = ctypes.CFUNCTYPE(
    None, _vp, _i32, _vp, ctypes.c_int, _u32, ctypes.POINTER(ctypes.c_uint8), _long
)


class HidError(OSError):
    pass


def _framework(name: str) -> ctypes.CDLL:
    path = ctypes.util.find_library(name)
    if path is None:
        raise HidError(f"{name}.framework not found -- herdrkeys runs on macOS only")
    return ctypes.cdll.LoadLibrary(path)


def _declare(lib: ctypes.CDLL, name: str, restype, *argtypes) -> None:
    fn = getattr(lib, name)
    fn.restype = restype
    fn.argtypes = list(argtypes)


_libs: dict[str, ctypes.CDLL] = {}


def _load() -> dict[str, ctypes.CDLL]:
    """Load lazily, so importing this module costs nothing on a Keybow-only setup."""
    if _libs:
        return _libs
    cf = _framework("CoreFoundation")
    iokit = _framework("IOKit")
    cg = _framework("CoreGraphics")
    imageio = _framework("ImageIO")

    _declare(cf, "CFStringCreateWithCString", _vp, _vp, ctypes.c_char_p, _u32)
    _declare(cf, "CFNumberGetValue", ctypes.c_bool, _vp, ctypes.c_int, _vp)
    _declare(cf, "CFGetTypeID", ctypes.c_ulong, _vp)
    _declare(cf, "CFStringGetTypeID", ctypes.c_ulong)
    _declare(cf, "CFStringGetCString", ctypes.c_bool, _vp, ctypes.c_char_p, _long, _u32)
    _declare(cf, "CFSetGetCount", _long, _vp)
    _declare(cf, "CFSetGetValues", None, _vp, _vp)
    _declare(cf, "CFRelease", None, _vp)
    _declare(cf, "CFRetain", _vp, _vp)
    _declare(cf, "CFRunLoopGetCurrent", _vp)
    _declare(cf, "CFRunLoopRunInMode", _i32, _vp, ctypes.c_double, ctypes.c_bool)
    _declare(cf, "CFRunLoopStop", None, _vp)
    _declare(cf, "CFDataCreate", _vp, _vp, ctypes.c_char_p, _long)
    _declare(cf, "CFDataCreateMutable", _vp, _vp, _long)
    _declare(cf, "CFDataGetLength", _long, _vp)
    _declare(cf, "CFDataGetBytePtr", _vp, _vp)

    _declare(iokit, "IOHIDManagerCreate", _vp, _vp, _u32)
    _declare(iokit, "IOHIDManagerSetDeviceMatching", None, _vp, _vp)
    _declare(iokit, "IOHIDManagerCopyDevices", _vp, _vp)
    _declare(iokit, "IOHIDDeviceGetProperty", _vp, _vp, _vp)
    _declare(iokit, "IOHIDDeviceOpen", _i32, _vp, _u32)
    _declare(iokit, "IOHIDDeviceClose", _i32, _vp, _u32)
    _declare(iokit, "IOHIDDeviceSetReport", _i32, _vp, ctypes.c_int, _long, ctypes.c_char_p, _long)
    _declare(iokit, "IOHIDDeviceRegisterInputReportCallback", None, _vp, _vp, _long, _REPORT_CALLBACK, _vp)
    _declare(iokit, "IOHIDDeviceScheduleWithRunLoop", None, _vp, _vp, _vp)
    _declare(iokit, "IOHIDDeviceUnscheduleFromRunLoop", None, _vp, _vp, _vp)

    _declare(cg, "CGColorSpaceCreateDeviceRGB", _vp)
    _declare(cg, "CGColorSpaceRelease", None, _vp)
    _declare(cg, "CGDataProviderCreateWithCFData", _vp, _vp)
    _declare(cg, "CGDataProviderRelease", None, _vp)
    _declare(
        cg, "CGImageCreate", _vp,
        ctypes.c_size_t, ctypes.c_size_t, ctypes.c_size_t, ctypes.c_size_t, ctypes.c_size_t,
        _vp, _u32, _vp, _vp, ctypes.c_bool, ctypes.c_int,
    )
    _declare(cg, "CGImageRelease", None, _vp)
    _declare(imageio, "CGImageDestinationCreateWithData", _vp, _vp, _vp, ctypes.c_size_t, _vp)
    _declare(imageio, "CGImageDestinationAddImage", None, _vp, _vp, _vp)
    _declare(imageio, "CGImageDestinationFinalize", ctypes.c_bool, _vp)

    _libs.update(cf=cf, iokit=iokit, cg=cg, imageio=imageio)
    return _libs


def _cfstr(text: str) -> int:
    return _load()["cf"].CFStringCreateWithCString(None, text.encode(), _UTF8)


def _property(device: int, key: str) -> int | str | None:
    libs = _load()
    cf = libs["cf"]
    name = _cfstr(key)
    try:
        ref = libs["iokit"].IOHIDDeviceGetProperty(device, name)
    finally:
        cf.CFRelease(name)
    if not ref:
        return None
    if cf.CFGetTypeID(ref) == cf.CFStringGetTypeID():
        buffer = ctypes.create_string_buffer(256)
        if cf.CFStringGetCString(ref, buffer, len(buffer), _UTF8):
            return buffer.value.decode(errors="replace")
        return None
    value = ctypes.c_long()
    if cf.CFNumberGetValue(ref, _CFNUMBER_LONG, ctypes.byref(value)):
        return value.value
    return None


@dataclass(frozen=True)
class HidInfo:
    vendor_id: int
    product_id: int
    usage_page: int
    usage: int
    serial: str | None
    product: str | None


def find_hid(vendor_id: int, product_id: int, usage_page: int) -> list[tuple[HidInfo, int]]:
    """Every HID interface with these IDs, and its IOHIDDeviceRef, retained.

    Matched by hand rather than through a matching dictionary, because building
    a CFDictionary from ctypes needs CoreFoundation's callback structs, and the
    device list is short. The caller owns each ref and releases it.
    """
    libs = _load()
    cf, iokit = libs["cf"], libs["iokit"]
    manager = iokit.IOHIDManagerCreate(None, _OPTIONS_NONE)
    if not manager:
        raise HidError("IOHIDManagerCreate failed")
    try:
        iokit.IOHIDManagerSetDeviceMatching(manager, None)
        devices = iokit.IOHIDManagerCopyDevices(manager)
        if not devices:
            return []
        try:
            count = cf.CFSetGetCount(devices)
            refs = (_vp * count)()
            cf.CFSetGetValues(devices, refs)
            found = []
            for ref in refs:
                if (
                    _property(ref, "VendorID") == vendor_id
                    and _property(ref, "ProductID") == product_id
                    and _property(ref, "PrimaryUsagePage") == usage_page
                ):
                    info = HidInfo(
                        vendor_id=vendor_id,
                        product_id=product_id,
                        usage_page=usage_page,
                        usage=_property(ref, "PrimaryUsage") or 0,
                        serial=_str_or_none(_property(ref, "SerialNumber")),
                        product=_str_or_none(_property(ref, "Product")),
                    )
                    found.append((info, cf.CFRetain(ref)))
            return found
        finally:
            cf.CFRelease(devices)
    finally:
        cf.CFRelease(manager)


def release(ref: int) -> None:
    """Give back a ref `find_hid` handed out and nobody is going to open."""
    _load()["cf"].CFRelease(ref)


def _str_or_none(value: int | str | None) -> str | None:
    return value if isinstance(value, str) else None


class HidDevice:
    """One opened HID interface. Writes output reports; reads input reports.

    IOKit delivers input reports only to a run loop, and the daemon's loop is a
    selector, not a run loop. So reports are received on a thread of their own
    that does nothing but run one, and each one that arrives writes a byte to a
    pipe: `fileno()` is that pipe, and the selector wakes on a press exactly as
    it does for the Keybow's serial port. `read()` never blocks.

    Every failure is a HidError, including the one a write raises once the
    device has been unplugged.
    """

    _POLL_SECONDS = 0.25  # how soon the reader notices `close()`

    def __init__(self, ref: int, *, input_size: int) -> None:
        libs = _load()
        self._cf, self._iokit = libs["cf"], libs["iokit"]
        self._ref = ref
        status = self._iokit.IOHIDDeviceOpen(ref, _OPTIONS_NONE)
        if status != 0:
            self._cf.CFRelease(ref)
            raise HidError(f"IOHIDDeviceOpen failed: {_ioreturn(status)}")
        self._open = True
        self._reports: list[bytes] = []
        self._lock = threading.Lock()
        self._wake_r, self._wake_w = os.pipe()
        os.set_blocking(self._wake_r, False)
        os.set_blocking(self._wake_w, False)
        self._buffer = (ctypes.c_uint8 * input_size)()
        # Held on self: a CFUNCTYPE that is garbage collected while IOKit still
        # holds its address crashes the interpreter on the next report.
        self._callback = _REPORT_CALLBACK(self._on_report)
        self._iokit.IOHIDDeviceRegisterInputReportCallback(
            ref, ctypes.cast(self._buffer, _vp), input_size, self._callback, None
        )
        self._scheduled = threading.Event()
        self._loop: int | None = None  # the reader's run loop, once it has one
        self._reader = threading.Thread(target=self._run, name="cn001-reader", daemon=True)
        self._reader.start()
        self._scheduled.wait(2.0)

    def _run(self) -> None:
        mode = _vp.in_dll(self._cf, "kCFRunLoopDefaultMode")
        loop = self._cf.CFRunLoopGetCurrent()
        self._iokit.IOHIDDeviceScheduleWithRunLoop(self._ref, loop, mode)
        self._loop = loop
        self._scheduled.set()
        try:
            while self._open:
                self._cf.CFRunLoopRunInMode(mode, self._POLL_SECONDS, False)
        finally:
            self._iokit.IOHIDDeviceUnscheduleFromRunLoop(self._ref, loop, mode)

    def _on_report(self, _context, result, _sender, _type, _report_id, report, length) -> None:
        if result != 0 or length <= 0:
            return
        with self._lock:
            self._reports.append(ctypes.string_at(report, length))
        try:
            os.write(self._wake_w, b"\0")
        except BlockingIOError:
            pass  # the pipe is full of wakeups already; one more says nothing new

    def fileno(self) -> int:
        return self._wake_r

    def write(self, report: bytes) -> None:
        """Send one output report. `report` excludes the report-ID byte."""
        if not self._open:
            raise HidError("device is closed")
        status = self._iokit.IOHIDDeviceSetReport(self._ref, _REPORT_OUTPUT, 0, report, len(report))
        if status != 0:
            raise HidError(f"IOHIDDeviceSetReport failed: {_ioreturn(status)}")

    def read(self) -> list[bytes]:
        try:
            while os.read(self._wake_r, 4096):
                pass
        except BlockingIOError:
            pass
        with self._lock:
            reports, self._reports = self._reports, []
        return reports

    def close(self) -> None:
        if not self._open:
            return
        self._open = False
        # Stopped rather than waited out, so the reader leaves now and not at
        # the end of its next poll.
        if self._loop is not None:
            self._cf.CFRunLoopStop(self._loop)
        self._reader.join(self._POLL_SECONDS * 4)
        if self._reader.is_alive():
            # The reader still holds the device ref and the pipe: it may yet
            # unschedule the one and write to the other. Freeing them under it
            # would be a crash, or a write into whatever file reuses the
            # descriptor. Leaking one device's worth is the lesser harm, and a
            # wait without a limit would hang the daemon on an unplug.
            log.warning("cn001 reader did not stop; leaving its resources unreleased")
            return
        self._iokit.IOHIDDeviceClose(self._ref, _OPTIONS_NONE)
        self._cf.CFRelease(self._ref)
        os.close(self._wake_r)
        os.close(self._wake_w)


def _ioreturn(status: int) -> str:
    names = {
        0xE00002BC: "kIOReturnError",
        0xE00002C0: "kIOReturnNoDevice",
        0xE00002C1: "kIOReturnNotPrivileged",
        0xE00002C2: "kIOReturnBadArgument",
        0xE00002C5: "kIOReturnExclusiveAccess",  # another app holds it
        0xE00002CD: "kIOReturnNotOpen",
        0xE00002D6: "kIOReturnTimeout",
        0xE00002D8: "kIOReturnNotAttached",  # unplugged
        0xE00002E2: "kIOReturnNotPermitted",
    }
    status &= 0xFFFFFFFF
    return names.get(status, f"IOReturn {status:#x}")


def encode_jpeg(width: int, height: int, rgbx: bytes) -> bytes:
    """Encode a `width` x `height` image, four bytes a pixel (R, G, B, ignored)."""
    if len(rgbx) != width * height * 4:
        raise ValueError(f"expected {width * height * 4} bytes, got {len(rgbx)}")
    libs = _load()
    cf, cg, imageio = libs["cf"], libs["cg"], libs["imageio"]
    pixels = cf.CFDataCreate(None, rgbx, len(rgbx))
    provider = cg.CGDataProviderCreateWithCFData(pixels)
    space = cg.CGColorSpaceCreateDeviceRGB()
    image = cg.CGImageCreate(
        width, height, 8, 32, width * 4, space, _ALPHA_NONE_SKIP_LAST, provider, None, False, 0
    )
    out = cf.CFDataCreateMutable(None, 0)
    uti = _cfstr("public.jpeg")
    destination = imageio.CGImageDestinationCreateWithData(out, uti, 1, None)
    try:
        if not (image and destination):
            raise HidError("could not set up JPEG encoding")
        imageio.CGImageDestinationAddImage(destination, image, None)
        if not imageio.CGImageDestinationFinalize(destination):
            raise HidError("JPEG encoding failed")
        return ctypes.string_at(cf.CFDataGetBytePtr(out), cf.CFDataGetLength(out))
    finally:
        for ref in (destination, uti, out, pixels):
            if ref:
                cf.CFRelease(ref)
        if image:
            cg.CGImageRelease(image)
        cg.CGColorSpaceRelease(space)
        cg.CGDataProviderRelease(provider)
