import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('smoke_native', Path(__file__).parents[1] / 'smoke-native.py')
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class NativeCaptureDiagnostics(unittest.TestCase):
    def test_timeout_retains_capture_stage_output_and_original_deadline(self):
        with tempfile.TemporaryDirectory() as directory:
            png = Path(directory) / 'windows-amd64.png'
            def stalled(command, **options):
                self.assertEqual(options['timeout'], 30)
                self.assertTrue(options['check'])
                self.assertEqual(options['stderr'], subprocess.STDOUT)
                options['stdout'].write(b'Owned window found at 123 ms\n')
                options['stdout'].flush()
                raise subprocess.TimeoutExpired(command, options['timeout'])
            with patch.object(smoke.subprocess, 'run', side_effect=stalled):
                with self.assertRaises(subprocess.TimeoutExpired):
                    smoke.capture('windows', 12345, png)
            self.assertEqual(png.with_suffix('.capture.log').read_bytes(), b'Owned window found at 123 ms\n')
            self.assertFalse(png.exists())

    def test_secondary_capture_failure_does_not_replace_primary_error(self):
        with tempfile.TemporaryDirectory() as directory:
            scratch = Path(directory)
            primary = RuntimeError('original native readiness failure')
            child = Mock(pid=12345)
            child.poll.return_value = None
            with patch.object(smoke, 'capture', side_effect=RuntimeError('secondary capture failure')):
                smoke.capture_failure('windows', child, scratch, primary)
            self.assertIn('original native readiness failure', (scratch / 'failure.txt').read_text())
            self.assertIn('secondary capture failure', (scratch / 'failure-capture.txt').read_text())

    def test_exited_child_is_not_captured(self):
        with tempfile.TemporaryDirectory() as directory:
            child = Mock(pid=12345)
            child.poll.return_value = 1
            with patch.object(smoke, 'capture') as capture:
                smoke.capture_failure('windows', child, Path(directory), RuntimeError('exited'))
                capture.assert_not_called()
