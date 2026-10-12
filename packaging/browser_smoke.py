"""Verify HTTP mode in the actual native distribution executable."""
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def verify_browser(binary, timeout=30):
    binary = Path(binary).resolve()
    with tempfile.TemporaryDirectory(prefix='ocular-browser-smoke-') as scratch:
        root = Path(scratch)
        env = dict(os.environ)
        for key in ('HOME', 'USERPROFILE', 'DOCKER_CONFIG', 'SPK_OCULAR_HOME',
                    'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME',
                    'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'APPDATA', 'LOCALAPPDATA'):
            directory = root / key.lower()
            directory.mkdir(mode=0o700)
            env[key] = str(directory)
        env.update(KUBECONFIG=str(root / 'no-kubeconfig'), DOCKER_HOST='tcp://127.0.0.1:1',
                   DOCKER_CONTEXT='', DOCKER_TLS_VERIFY='', DOCKER_CERT_PATH='')
        # HTTP mode must not need an X/Wayland display or enter the native runner.
        for key in ('DISPLAY', 'WAYLAND_DISPLAY', 'DBUS_SESSION_BUS_ADDRESS',
                    'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy',
                    'https_proxy', 'all_proxy', 'NO_PROXY', 'no_proxy'):
            env.pop(key, None)
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with (root / 'app.log').open('wb') as log:
            child = subprocess.Popen([str(binary), '--browser', '--port', str(port)],
                                     env=env, stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + timeout
                while time.monotonic() < deadline:
                    if child.poll() is not None:
                        raise RuntimeError('native binary exited in browser mode: ' +
                                           (root / 'app.log').read_text(errors='replace'))
                    try:
                        with opener.open(f'http://127.0.0.1:{port}/', timeout=1) as response:
                            html = response.read(4 * 1024 * 1024)
                            if response.status != 200 or b'SPK Ocular' not in html:
                                raise RuntimeError('browser mode did not serve the embedded application')
                        print('PASS native distribution binary --browser on loopback without a display')
                        return
                    except (urllib.error.URLError, TimeoutError):
                        time.sleep(0.1)
                raise RuntimeError('native browser-mode startup timed out')
            finally:
                if child.poll() is None:
                    child.terminate()
                try:
                    child.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=5)
                    raise RuntimeError('owned browser-mode verification process did not stop')
