#!/usr/bin/env python3
"""Launch only our packaged application in an isolated profile on a native CI runner."""
import argparse
import json
import os
import re
import shutil
from pathlib import Path
import subprocess
import time
import tempfile
import urllib.request

from release import ROOT


def capture(platform, pid, png):
    if platform == 'windows':
        subprocess.run(['pwsh.exe', '-NoLogo', '-NoProfile', '-NonInteractive', '-File', str(ROOT / 'packaging/windows/screenshot.ps1'), '-ProcessId', str(pid), '-OutputPath', str(png)], check=True, timeout=30)
    elif platform == 'linux':
        tree = subprocess.check_output(['xwininfo', '-root', '-tree'], text=True)
        owned = None
        for line in tree.splitlines():
            match = re.search(r'(0x[0-9a-f]+).*SPK Ocular', line)
            if not match:
                continue
            window = match.group(1)
            props = subprocess.check_output(['xprop', '-id', window, '_NET_WM_PID'], text=True)
            if props.strip().endswith('= ' + str(pid)):
                owned = window
                break
        if owned is None:
            raise RuntimeError('cannot find the owned native X window')
        subprocess.run(['import', '-window', owned, str(png)], check=True)
    else:
        subprocess.run(['swift', str(ROOT / 'packaging/macos-smoke.swift'), str(pid), str(png)], check=True, timeout=60)
    if png.stat().st_size < 4096:
        raise RuntimeError('native screenshot is empty')


def smoke(platform, arch, version):
    scratch = Path(os.environ['OCULAR_SCRATCH_DIR']) / 'native-smoke'
    scratch.mkdir(parents=True, exist_ok=True)
    profile = Path(tempfile.mkdtemp(prefix='p-', dir=scratch.parent))
    env = dict(os.environ)
    for key, name in {'HOME': 'home', 'USERPROFILE': 'home', 'DOCKER_CONFIG': 'docker', 'SPK_OCULAR_HOME': 'data',
                      'XDG_CONFIG_HOME': 'config', 'XDG_DATA_HOME': 'share', 'XDG_CACHE_HOME': 'cache',
                      'XDG_RUNTIME_DIR': 'run', 'XDG_DOWNLOAD_DIR': 'Downloads', 'APPDATA': 'appdata', 'LOCALAPPDATA': 'localappdata'}.items():
        path = profile / name
        path.mkdir(exist_ok=True)
        env[key] = str(path)
    env.update(KUBECONFIG=str(profile / 'no-kubeconfig'), DOCKER_HOST='tcp://127.0.0.1:1', DOCKER_CONTEXT='', DOCKER_TLS_VERIFY='', DOCKER_CERT_PATH='',
               DBUS_SESSION_BUS_ADDRESS='unix:path=' + str(profile / 'nonexistent-bus'), LANG='en_US.UTF-8', LANGUAGE='en')
    # This native smoke covers rendering the synthetic workspace; onboarding
    # has its own fresh-profile UI tests. Seed only an empty dismissed registry.
    configs = profile / 'data/configurations'
    configs.mkdir(mode=0o700)
    registry = configs / 'kubeconfigs.json'
    registry.write_text(json.dumps({'Version': 1, 'Initialized': True}), encoding='utf-8')
    registry.chmod(0o600)
    stage = ROOT / 'build' / f'package-{platform}-{arch}'
    binary = ROOT / 'build/bin/spk-ocular-release' if platform == 'linux' else stage / ('spk-ocular.exe' if platform == 'windows' else 'SPK Ocular.app/Contents/MacOS/spk-ocular')
    assert subprocess.check_output([str(binary), 'version'], env=env, text=True).strip() == 'spk-ocular ' + version
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    info_file = profile / 'data/test-api.json'
    info_file.unlink(missing_ok=True)
    with (scratch / 'app.log').open('wb') as log:
        child = subprocess.Popen([str(binary), '--test-api', '--test-synthetic'], env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 90
            stats = {}
            selected_at = 0.0
            connect_requested = False
            while time.monotonic() < deadline:
                if child.poll() is not None:
                    raise RuntimeError(f'native app exited during startup: {child.returncode}')
                if info_file.exists():
                    info = json.loads(info_file.read_text())
                    # The initial frontend target-list request can overlap this
                    # fixture selection. Repeat the idempotent fixture action
                    # until the page has subscribed and opened its view.
                    if time.monotonic() - selected_at >= 1:
                        request = urllib.request.Request(info['url'] + '/api/_test/synthetic/select', data=b'', headers={'Authorization': 'Bearer ' + info['token']})
                        with opener.open(request, timeout=5) as response:
                            response.read()
                        selected_at = time.monotonic()
                    if not connect_requested:
                        request = urllib.request.Request(info['url'] + '/api/_test/synthetic/connect', data=b'', headers={'Authorization': 'Bearer ' + info['token']})
                        with opener.open(request, timeout=5) as response:
                            response.read()
                        connect_requested = True
                    request = urllib.request.Request(info['url'] + '/api/_test/stats', headers={'Authorization': 'Bearer ' + info['token']})
                    with opener.open(request, timeout=5) as response:
                        stats = json.load(response)
                    # A view opened by the actual webview proves that its JS and
                    # Wails bindings loaded; the HTTP test server alone is not enough.
                    if stats.get('sessions', 0) > 0 and stats.get('views', 0) > 0:
                        break
                time.sleep(.5)
            else:
                raise RuntimeError(f'native page did not open a synthetic view: {stats}')
            (scratch / 'ready.json').write_text(json.dumps(stats, indent=2))
            time.sleep(3)
            png = scratch / f'{platform}-{arch}.png'
            capture(platform, child.pid, png)
            report = {'platform': platform, 'arch': arch, 'version': version, 'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(), 'production': True, 'fixtureSynthetic': True, 'ownedWindow': True, 'pid': child.pid, 'screenshot': png.name}
            (scratch / 'report.json').write_text(json.dumps(report, indent=2))
            print('Native webview opened a synthetic view; screenshot:', png)
        except Exception:
            if child.poll() is None:
                capture(platform, child.pid, scratch / 'failure.png')
            raise
        finally:
            if child.poll() is None:
                if platform == 'windows':
                    subprocess.run(['pwsh.exe', '-NoLogo', '-NoProfile', '-NonInteractive', '-Command', f'(Get-Process -Id {child.pid}).CloseMainWindow()'], check=True, timeout=15)
                else:
                    child.terminate()
            try:
                child.wait(timeout=15)
            except subprocess.TimeoutExpired:
                if platform == 'windows':
                    subprocess.run(['taskkill.exe', '/PID', str(child.pid), '/T', '/F'], check=True)
                else:
                    child.kill()
                child.wait(timeout=5)
                raise
            finally:
                desktop_log = profile / 'data/desktop.log'
                if desktop_log.is_file():
                    shutil.copy2(desktop_log, scratch / 'desktop.log')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--os', choices=['windows', 'darwin', 'linux'], required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    smoke(args.os, args.arch, args.version)
