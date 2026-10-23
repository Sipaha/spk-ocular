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


class NativeShutdownDiagnostics(unittest.TestCase):
    def test_failed_close_still_waits_and_reaps_only_owned_process(self):
        child = Mock(pid=12345)
        child.poll.return_value = None
        child.wait.side_effect = [subprocess.TimeoutExpired('owned app', 15), 0]
        primary = subprocess.CalledProcessError(1, 'graceful close')
        with patch.object(smoke.subprocess, 'run', side_effect=[primary, Mock()]) as run:
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                smoke.stop_child('windows', child)
        self.assertIs(raised.exception, primary)
        self.assertEqual(run.call_args_list[0].kwargs['timeout'], 15)
        self.assertEqual(run.call_args_list[1].args[0], ['taskkill.exe', '/PID', '12345', '/T', '/F'])
        self.assertEqual([call.kwargs['timeout'] for call in child.wait.call_args_list], [15, 5])

    def test_cleanup_failure_cannot_replace_primary_readiness_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            scratch = Path(directory)
            primary = TimeoutError('readiness request timed out')
            secondary = subprocess.TimeoutExpired('owned app shutdown', 15)
            with patch.object(smoke, 'stop_child', side_effect=secondary):
                with self.assertRaises(TimeoutError) as raised:
                    try:
                        raise primary
                    finally:
                        smoke.cleanup_child('windows', Mock(), scratch, primary)
            self.assertIs(raised.exception, primary)
            self.assertIn('owned app shutdown', (scratch / 'shutdown-failure.txt').read_text())

    def test_cleanup_failure_after_success_still_fails_verification(self):
        with tempfile.TemporaryDirectory() as directory:
            secondary = subprocess.TimeoutExpired('owned app shutdown', 15)
            with patch.object(smoke, 'stop_child', side_effect=secondary):
                with self.assertRaises(subprocess.TimeoutExpired) as raised:
                    smoke.cleanup_child('windows', Mock(), Path(directory), None)
            self.assertIs(raised.exception, secondary)

    def test_primary_traceback_identifies_request_phase(self):
        with tempfile.TemporaryDirectory() as directory:
            child = Mock()
            child.poll.return_value = 1
            try:
                raise TimeoutError('readiness request timed out')
            except TimeoutError as primary:
                smoke.capture_failure('windows', child, Path(directory), primary)
            trace = (Path(directory) / 'failure-traceback.txt').read_text()
            self.assertIn('test_primary_traceback_identifies_request_phase', trace)
            self.assertIn('TimeoutError: readiness request timed out', trace)
