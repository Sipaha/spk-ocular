import ctypes
import importlib.util
from pathlib import Path
import struct
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock
import zlib

spec = importlib.util.spec_from_file_location('windows_capture', Path(__file__).parents[1] / 'windows/screenshot.py')
capture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capture)


class WindowsCapture(unittest.TestCase):
    def api(self):
        user, gdi, dwm = Mock(), Mock(), Mock()
        api = SimpleNamespace(user=user, gdi=gdi, dwm=dwm)
        def get_owner(window, pointer):
            ctypes.cast(pointer, ctypes.POINTER(ctypes.c_uint32)).contents.value = 42 if window in (11, 33) else 99
            return 1
        user.GetWindowThreadProcessId.side_effect = get_owner
        user.EnumWindows.side_effect = lambda visit, unused: all(visit(window, 0) for window in (11, 22, 33))
        user.IsWindowVisible.side_effect = lambda window: window != 33
        user.IsIconic.return_value = 0
        def title(window, buffer, length):
            buffer.value = 'SPK Ocular'
            return len(buffer.value)
        user.GetWindowTextW.side_effect = title
        def bounds(window, pointer):
            rect = ctypes.cast(pointer, ctypes.POINTER(capture.Rect)).contents
            rect.left, rect.top, rect.right, rect.bottom = 20, 30, 820, 630
            return 1
        user.GetWindowRect.side_effect = bounds
        user.GetWindowDC.return_value = 100
        user.PrintWindow.return_value = 1
        dwm.DwmFlush.return_value = 0
        gdi.CreateCompatibleDC.return_value = 200
        gdi.CreateCompatibleBitmap.return_value = 300
        gdi.SelectObject.side_effect = [400, 300]
        def pixels(dc, bitmap, start, height, buffer, pointer, usage):
            self.assertEqual((dc, bitmap, start, height, usage), (200, 300, 0, 600, 0))
            self.assertEqual(gdi.SelectObject.call_count, 2)
            info = ctypes.cast(pointer, ctypes.POINTER(capture.BitmapInfo)).contents
            self.assertEqual((info.header.size, info.header.width, info.header.height, info.header.bits), (40, 800, -600, 32))
            return height
        gdi.GetDIBits.side_effect = pixels
        return api

    def test_fixed_width_native_structures(self):
        self.assertEqual(ctypes.sizeof(capture.Rect), 16)
        self.assertEqual(ctypes.sizeof(capture.BitmapHeader), 40)
        self.assertEqual(ctypes.sizeof(capture.BitmapInfo), 44)

    def test_png_preserves_top_down_rgb_pixels_and_chunk_checksums(self):
        # Red/green top row; blue/white bottom row, with unused alpha values.
        image = capture.png_bytes(2, 2, bytes([0, 0, 255, 0, 0, 255, 0, 23,
                                             255, 0, 0, 99, 255, 255, 255, 0]))
        self.assertEqual(image[:8], b'\x89PNG\r\n\x1a\n')
        offset, chunks = 8, {}
        while offset < len(image):
            size = struct.unpack('>I', image[offset:offset + 4])[0]
            kind, data = image[offset + 4:offset + 8], image[offset + 8:offset + 8 + size]
            checksum = struct.unpack('>I', image[offset + 8 + size:offset + 12 + size])[0]
            self.assertEqual(checksum, zlib.crc32(kind + data))
            chunks[kind] = data
            offset += size + 12
        self.assertEqual(struct.unpack('>IIBBBBB', chunks[b'IHDR']), (2, 2, 8, 2, 0, 0, 0))
        self.assertEqual(zlib.decompress(chunks[b'IDAT']), b'\0\xff\0\0\0\xff\0\0\0\0\xff\xff\xff\xff')

    def test_malformed_pixels_are_refused(self):
        for dimensions in [(0, 2), (2, -1), (2, 2)]:
            with self.subTest(dimensions=dimensions), self.assertRaises(ValueError):
                capture.png_bytes(*dimensions, b'bad')

    def test_same_title_foreign_and_hidden_windows_are_never_selected(self):
        api = self.api()
        self.assertEqual(capture.owned_window(api.user, 42), 11)
        api.user.EnumWindows.side_effect = lambda visit, unused: all(visit(window, 0) for window in (22, 33))
        with self.assertRaisesRegex(RuntimeError, 'found 0'):
            capture.owned_window(api.user, 42)

    def test_ambiguous_owned_windows_are_refused(self):
        api = self.api()
        api.user.IsWindowVisible.return_value = 1
        api.user.IsWindowVisible.side_effect = None
        with self.assertRaisesRegex(RuntimeError, 'found 2'):
            capture.owned_window(api.user, 42)

    def test_capture_uses_only_owned_hwnd_and_releases_gdi_objects(self):
        api = self.api()
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'native.png'
            capture.capture(42, output, api)
            self.assertEqual(struct.unpack('>II', output.read_bytes()[16:24]), (800, 600))
        api.user.PrintWindow.assert_called_once_with(11, 200, 2)
        api.gdi.DeleteObject.assert_called_once_with(300)
        api.gdi.DeleteDC.assert_called_once_with(200)
        api.user.ReleaseDC.assert_called_once_with(11, 100)

    def test_render_failure_still_restores_and_releases_without_output(self):
        api = self.api()
        api.user.PrintWindow.return_value = 0
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'native.png'
            with self.assertRaisesRegex(RuntimeError, 'cannot render'):
                capture.capture(42, output, api)
            self.assertFalse(output.exists())
        self.assertEqual(api.gdi.SelectObject.call_count, 2)
        api.gdi.DeleteObject.assert_called_once_with(300)
        api.gdi.DeleteDC.assert_called_once_with(200)
        api.user.ReleaseDC.assert_called_once_with(11, 100)

    def test_partial_bitmap_rows_are_not_a_successful_screenshot(self):
        api = self.api()
        api.gdi.GetDIBits.side_effect = None
        api.gdi.GetDIBits.return_value = 599
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'native.png'
            with self.assertRaisesRegex(RuntimeError, 'all native bitmap rows'):
                capture.capture(42, output, api)
            self.assertFalse(output.exists())
        api.gdi.DeleteObject.assert_called_once_with(300)
        api.user.ReleaseDC.assert_called_once_with(11, 100)

    def test_changed_owner_is_rejected_before_writing_image(self):
        api = self.api()
        original = api.gdi.GetDIBits.side_effect
        def changed(*args):
            result = original(*args)
            api.user.GetWindowThreadProcessId.side_effect = lambda window, pointer: 0
            return result
        api.gdi.GetDIBits.side_effect = changed
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'native.png'
            with self.assertRaisesRegex(RuntimeError, 'owner changed during capture'):
                capture.capture(42, output, api)
            self.assertFalse(output.exists())
        api.gdi.DeleteObject.assert_called_once_with(300)
        api.user.ReleaseDC.assert_called_once_with(11, 100)

    def test_failed_dc_allocation_releases_only_acquired_resources(self):
        api = self.api()
        api.gdi.CreateCompatibleDC.return_value = 0
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(RuntimeError, 'capture DC'):
                capture.capture(42, Path(directory) / 'native.png', api)
        api.gdi.DeleteDC.assert_not_called()
        api.gdi.DeleteObject.assert_not_called()
        api.user.ReleaseDC.assert_called_once_with(11, 100)
