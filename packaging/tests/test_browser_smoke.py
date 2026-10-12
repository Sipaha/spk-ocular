import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('browser_smoke', Path(__file__).parents[1] / 'browser_smoke.py')
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


@unittest.skipIf(os.name == 'nt', 'executable Python fixtures use a POSIX shebang')
class BrowserMode(unittest.TestCase):
    def fixture(self, root, body):
        binary = root / 'native-app'
        binary.write_text('#!/usr/bin/env python3\n' + body)
        binary.chmod(0o755)
        return binary

    def test_serves_embedded_ui_without_display_and_stops_owned_child(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            marker = root / 'stopped'
            binary = self.fixture(root, f'''
import os,sys,signal
from http.server import HTTPServer,BaseHTTPRequestHandler
assert sys.argv[1]=='--browser'
assert not any(os.environ.get(k) for k in ['DISPLAY','WAYLAND_DISPLAY'])
assert os.environ['HOME']!=os.environ['SPK_OCULAR_HOME']
assert not os.path.exists(os.environ['KUBECONFIG'])
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'<title>SPK Ocular</title>')
def stop(*args):
 open({str(marker)!r},'w').write('stopped');sys.exit(0)
signal.signal(signal.SIGTERM,stop)
HTTPServer(('127.0.0.1',int(sys.argv[3])),Handler).serve_forever()
''')
            smoke.verify_browser(binary, timeout=5)
            self.assertEqual(marker.read_text(), 'stopped')

    def test_early_native_startup_failure_is_not_http_success(self):
        with tempfile.TemporaryDirectory() as scratch:
            binary = self.fixture(Path(scratch), 'raise SystemExit("missing native runtime")\n')
            with self.assertRaisesRegex(RuntimeError, 'missing native runtime'):
                smoke.verify_browser(binary, timeout=5)

    def test_timeout_still_stops_owned_child(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            marker = root / 'stopped'
            binary = self.fixture(root, f'''
import signal,sys,time
def stop(*args):
 open({str(marker)!r},'w').write('stopped');sys.exit(0)
signal.signal(signal.SIGTERM,stop)
while True:time.sleep(.01)
''')
            with self.assertRaisesRegex(RuntimeError, 'startup timed out'):
                smoke.verify_browser(binary, timeout=1)
            self.assertEqual(marker.read_text(), 'stopped')
