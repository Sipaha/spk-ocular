"""Capture one PID-owned Ocular HWND without a runtime C# compiler."""
import argparse
import ctypes as c
from pathlib import Path
import struct
import sys
import time
import zlib


class Rect(c.Structure):
    _fields_ = [(name, c.c_int32) for name in ('left', 'top', 'right', 'bottom')]


class BitmapHeader(c.Structure):
    _fields_ = [('size', c.c_uint32), ('width', c.c_int32), ('height', c.c_int32),
                ('planes', c.c_uint16), ('bits', c.c_uint16),
                ('compression', c.c_uint32), ('image_size', c.c_uint32),
                ('xppm', c.c_int32), ('yppm', c.c_int32),
                ('colors', c.c_uint32), ('important', c.c_uint32)]


class BitmapInfo(c.Structure):
    _fields_ = [('header', BitmapHeader), ('colors', c.c_uint32 * 1)]


Callback = getattr(c, 'WINFUNCTYPE', c.CFUNCTYPE)(c.c_int32, c.c_void_p, c.c_ssize_t)


class WindowsAPI:
    def __init__(self):
        if sys.platform != 'win32':
            raise RuntimeError('native Windows capture requires Windows')
        self.user = c.WinDLL('user32', use_last_error=True)
        self.gdi = c.WinDLL('gdi32', use_last_error=True)
        self.dwm = c.WinDLL('dwmapi', use_last_error=True)
        signatures = [
            (self.user, 'EnumWindows', [Callback, c.c_ssize_t], c.c_int32),
            (self.user, 'IsWindowVisible', [c.c_void_p], c.c_int32),
            (self.user, 'IsIconic', [c.c_void_p], c.c_int32),
            (self.user, 'GetWindowThreadProcessId', [c.c_void_p, c.POINTER(c.c_uint32)], c.c_uint32),
            (self.user, 'GetWindowTextW', [c.c_void_p, c.c_wchar_p, c.c_int32], c.c_int32),
            (self.user, 'GetWindowRect', [c.c_void_p, c.POINTER(Rect)], c.c_int32),
            (self.user, 'GetWindowDC', [c.c_void_p], c.c_void_p),
            (self.user, 'ReleaseDC', [c.c_void_p, c.c_void_p], c.c_int32),
            (self.user, 'PrintWindow', [c.c_void_p, c.c_void_p, c.c_uint32], c.c_int32),
            (self.gdi, 'CreateCompatibleDC', [c.c_void_p], c.c_void_p),
            (self.gdi, 'CreateCompatibleBitmap', [c.c_void_p, c.c_int32, c.c_int32], c.c_void_p),
            (self.gdi, 'SelectObject', [c.c_void_p, c.c_void_p], c.c_void_p),
            (self.gdi, 'DeleteObject', [c.c_void_p], c.c_int32),
            (self.gdi, 'DeleteDC', [c.c_void_p], c.c_int32),
            (self.gdi, 'GetDIBits', [c.c_void_p, c.c_void_p, c.c_uint32, c.c_uint32,
                                   c.c_void_p, c.POINTER(BitmapInfo), c.c_uint32], c.c_int32),
            (self.dwm, 'DwmFlush', [], c.c_int32),
        ]
        for library, name, arguments, result in signatures:
            function = getattr(library, name)
            function.argtypes, function.restype = arguments, result
        # Only this helper's coordinate interpretation changes, never the display.
        awareness = self.user.SetProcessDpiAwarenessContext
        awareness.argtypes, awareness.restype = [c.c_void_p], c.c_int32
        awareness(c.c_void_p(-4))


def owner(user, window):
    pid = c.c_uint32()
    if not user.GetWindowThreadProcessId(window, c.byref(pid)):
        return None
    return pid.value


def owned_window(user, pid):
    matches = []

    @Callback
    def visit(window, unused):
        if owner(user, window) != pid or not user.IsWindowVisible(window) or user.IsIconic(window):
            return 1
        title = c.create_unicode_buffer(512)
        user.GetWindowTextW(window, title, len(title))
        if title.value == 'SPK Ocular':
            matches.append(window)
        return 1

    if not user.EnumWindows(visit, 0):
        raise RuntimeError('cannot enumerate native windows')
    if len(matches) != 1:
        raise RuntimeError(f'expected one visible Ocular window owned by PID {pid}, found {len(matches)}')
    return matches[0]


def png_bytes(width, height, bgra):
    if width <= 0 or height <= 0 or len(bgra) != width * height * 4:
        raise ValueError('invalid top-down BGRA image')
    scanlines = bytearray()
    for y in range(height):
        source = bgra[y * width * 4:(y + 1) * width * 4]
        row = bytearray(1 + width * 3)
        row[1::3], row[2::3], row[3::3] = source[2::4], source[1::4], source[0::4]
        scanlines.extend(row)

    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))

    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(scanlines)) + chunk(b'IEND', b''))


def capture(pid, output, api=None):
    started = time.monotonic()

    def stage(text):
        print(f'{text} at {(time.monotonic() - started) * 1000:.0f} ms', flush=True)

    stage('Starting direct Win32 capture')
    api = api or WindowsAPI()
    user, gdi = api.user, api.gdi
    window = owned_window(user, pid)
    stage('Owned visible window found')
    if api.dwm.DwmFlush() != 0:
        raise RuntimeError('cannot flush desktop composition')
    stage('Desktop composition flushed')
    rect = Rect()
    if not user.GetWindowRect(window, c.byref(rect)):
        raise RuntimeError('cannot read owned window bounds')
    width, height = rect.right - rect.left, rect.bottom - rect.top
    if width < 800 or height < 600 or width * height > 32_000_000:
        raise RuntimeError(f'invalid native window bounds {width}x{height}')
    dc = user.GetWindowDC(window)
    if not dc:
        raise RuntimeError('cannot acquire owned window DC')
    memory_dc = bitmap = previous = None
    try:
        memory_dc = gdi.CreateCompatibleDC(dc)
        if not memory_dc:
            raise RuntimeError('cannot create capture DC')
        bitmap = gdi.CreateCompatibleBitmap(dc, width, height)
        if not bitmap:
            raise RuntimeError('cannot create capture bitmap')
        previous = gdi.SelectObject(memory_dc, bitmap)
        if not previous or previous == c.c_void_p(-1).value:
            previous = None
            raise RuntimeError('cannot select capture bitmap')
        try:
            if owner(user, window) != pid:
                raise RuntimeError('native window owner changed before capture')
            if not user.PrintWindow(window, memory_dc, 2):
                raise RuntimeError('cannot render owned native window')
            stage('Owned window rendered')
        finally:
            gdi.SelectObject(memory_dc, previous)
            previous = None
        # GetDIBits requires the bitmap to be deselected. Negative height requests
        # top-down rows; all Win32 scalar fields retain their native fixed widths.
        info = BitmapInfo()
        info.header = BitmapHeader(c.sizeof(BitmapHeader), width, -height, 1, 32, 0, 0, 0, 0, 0, 0)
        pixels = c.create_string_buffer(width * height * 4)
        if gdi.GetDIBits(memory_dc, bitmap, 0, height, pixels, c.byref(info), 0) != height:
            raise RuntimeError('cannot read all native bitmap rows')
        if owner(user, window) != pid:
            raise RuntimeError('native window owner changed during capture')
        Path(output).write_bytes(png_bytes(width, height, pixels.raw))
        stage('Native screenshot saved')
    finally:
        if previous:
            gdi.SelectObject(memory_dc, previous)
        if bitmap:
            gdi.DeleteObject(bitmap)
        if memory_dc:
            gdi.DeleteDC(memory_dc)
        user.ReleaseDC(window, dc)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pid', type=int, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    capture(args.pid, args.output)
