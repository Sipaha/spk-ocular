#!/usr/bin/env python3
"""Launch only our packaged application in an isolated profile on a native CI runner."""
import argparse
import json
import os
import re
import shutil
from pathlib import Path
import subprocess
import sys
import time
import tempfile
import traceback
import urllib.request

from release import ROOT
from diagnostics import report_failure


def capture(platform, pid, png):
    if platform == 'windows':
        # Keep stage output even when Windows composition/capture blocks. An
        # inherited CI console is not included in the diagnostic artifact.
        with png.with_suffix('.capture.log').open('wb') as output:
            subprocess.run([sys.executable, str(ROOT / 'packaging/windows/screenshot.py'), '--pid', str(pid), '--output', str(png)], check=True, timeout=30, stdout=output, stderr=subprocess.STDOUT)
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
        subprocess.run([str(Path(os.environ['GOBIN']) / 'native-macos-smoke'), str(pid), str(png)], check=True, timeout=60)
    if png.stat().st_size < 4096:
        raise RuntimeError('native screenshot is empty')


def capture_failure(platform, child, scratch, error):
    """Keep primary failure evidence even when the secondary capture fails."""
    (scratch / 'failure.txt').write_text(repr(error), encoding='utf-8')
    (scratch / 'failure-traceback.txt').write_text(''.join(traceback.format_exception(error)), encoding='utf-8')
    if child.poll() is None:
        try:
            capture(platform, child.pid, scratch / 'failure.png')
        except Exception as capture_error:
            (scratch / 'failure-capture.txt').write_text(repr(capture_error), encoding='utf-8')


def stop_child(platform, child):
    """Reap only our child, even when its graceful shutdown fails."""
    error = None
    if child.poll() is None:
        try:
            if platform == 'windows':
                subprocess.run(['pwsh.exe', '-NoLogo', '-NoProfile', '-NonInteractive', '-Command', f'(Get-Process -Id {child.pid}).CloseMainWindow()'], check=True, timeout=15)
            else:
                child.terminate()
        except Exception as failure:
            error = failure
    try:
        child.wait(timeout=15)
    except subprocess.TimeoutExpired as failure:
        error = error or failure
        try:
            if platform == 'windows':
                subprocess.run(['taskkill.exe', '/PID', str(child.pid), '/T', '/F'], check=True)
            else:
                child.kill()
        finally:
            child.wait(timeout=5)
    if error is not None:
        raise error


def cleanup_child(platform, child, scratch, primary_error):
    try:
        stop_child(platform, child)
    except Exception as error:
        (scratch / 'shutdown-failure.txt').write_text(repr(error), encoding='utf-8')
        if primary_error is None:
            raise
        # An earlier readiness/capture error remains the failing result. The
        # cleanup failure is separate evidence, never a replacement diagnosis.


def smoke(platform, arch, version, binary_override=None, commit_override=None):
    scratch = Path(os.environ['OCULAR_SCRATCH_DIR']) / 'native-smoke'
    scratch.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    (scratch / 'stages.log').write_text('', encoding='utf-8')

    def stage(name):
        # Fixed labels only: never log the loopback bearer token or profile.
        with (scratch / 'stages.log').open('a', encoding='utf-8') as output:
            output.write(f'{time.monotonic() - started:.3f}s {name}\n')

    stage('profile setup')
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
    package_stage = ROOT / 'build' / f'package-{platform}-{arch}'
    binary = ROOT / 'build/bin/spk-ocular-release' if platform == 'linux' else package_stage / ('spk-ocular.exe' if platform == 'windows' else 'SPK Ocular.app/Contents/MacOS/spk-ocular')
    if binary_override is not None:
        binary = Path(binary_override).resolve()
    stage('check executable version')
    assert subprocess.check_output([str(binary), 'version'], env=env, text=True).strip() == 'spk-ocular ' + version
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    info_file = profile / 'data/test-api.json'
    info_file.unlink(missing_ok=True)
    with (scratch / 'app.log').open('wb') as log:
        stage('launch native application')
        child = subprocess.Popen([str(binary), '--test-api', '--test-synthetic'], env=env, stdout=log, stderr=log)
        primary_error = None
        try:
            deadline = time.monotonic() + 90
            stats = {}
            selected_at = 0.0
            connect_requested = False
            while time.monotonic() < deadline:
                if child.poll() is not None:
                    raise RuntimeError(f'native app exited during startup: {child.returncode}')
                if info_file.exists():
                    stage('read test API descriptor')
                    info = json.loads(info_file.read_text())
                    # The initial frontend target-list request can overlap this
                    # fixture selection. Repeat the idempotent fixture action
                    # until the page has subscribed and opened its view.
                    if time.monotonic() - selected_at >= 1:
                        request = urllib.request.Request(info['url'] + '/api/_test/synthetic/select', data=b'', headers={'Authorization': 'Bearer ' + info['token']})
                        stage('select synthetic target')
                        with opener.open(request, timeout=5) as response:
                            response.read()
                        selected_at = time.monotonic()
                    if not connect_requested:
                        request = urllib.request.Request(info['url'] + '/api/_test/synthetic/connect', data=b'', headers={'Authorization': 'Bearer ' + info['token']})
                        stage('connect synthetic target')
                        with opener.open(request, timeout=5) as response:
                            response.read()
                        connect_requested = True
                    request = urllib.request.Request(info['url'] + '/api/_test/stats', headers={'Authorization': 'Bearer ' + info['token']})
                    stage('read native view readiness')
                    with opener.open(request, timeout=5) as response:
                        stats = json.load(response)
                    # A view opened by the actual webview proves that its JS and
                    # Wails bindings loaded; the HTTP test server alone is not enough.
                    if stats.get('sessions', 0) > 0 and stats.get('views', 0) > 0:
                        break
                time.sleep(.5)
            else:
                raise RuntimeError(f'native page did not open a synthetic view: {stats}')
            stage('native synthetic view ready')
            (scratch / 'ready.json').write_text(json.dumps(stats, indent=2))
            time.sleep(3)
            png = scratch / f'{platform}-{arch}.png'
            stage('capture owned native window')
            capture(platform, child.pid, png)
            report = {'platform': platform, 'arch': arch, 'version': version, 'commit': commit_override or subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(), 'production': True, 'fixtureSynthetic': True, 'ownedWindow': True, 'pid': child.pid, 'screenshot': png.name}
            (scratch / 'report.json').write_text(json.dumps(report, indent=2))
            print('Native webview opened a synthetic view; screenshot:', png)
        except Exception as error:
            primary_error = error
            stage('primary native verification failure')
            capture_failure(platform, child, scratch, error)
            raise
        finally:
            try:
                stage('stop owned application')
                cleanup_child(platform, child, scratch, primary_error)
            finally:
                desktop_log = profile / 'data/desktop.log'
                if desktop_log.is_file():
                    shutil.copy2(desktop_log, scratch / 'desktop.log')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--os', choices=['windows', 'darwin', 'linux'], required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--binary', type=Path, help='installed executable in an isolated compatibility rootfs')
    parser.add_argument('--commit', help='source revision inspected by the compatibility controller')
    args = parser.parse_args()
    try:
        smoke(args.os, args.arch, args.version, args.binary, args.commit)
    except Exception as error:
        report_failure(error)
        raise
