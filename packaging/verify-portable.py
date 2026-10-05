#!/usr/bin/env python3
"""Verify Windows/macOS payloads and the exact source/version they contain."""
import argparse
from pathlib import Path
import os
import plistlib
import subprocess
import tempfile
import zipfile

from portable import binary_arch, documents
from release import ROOT, version
from verify import assets, tar_contents, verify_checksums


def contents(path):
    if path.suffix != '.zip':
        return tar_contents(path.read_bytes())
    with zipfile.ZipFile(path) as source:
        result = {}
        for info in source.infolist():
            if info.is_dir():
                continue
            name = info.filename
            if name.startswith('/') or '..' in Path(name).parts or name in result:
                raise ValueError('unsafe or duplicate archive path')
            result[name] = source.read(info), (info.external_attr >> 16) & 0o777
        return result


def verify(directory, release_version, platform, arch):
    verify_checksums(directory, release_version, [arch], [platform])
    stage = ROOT / 'build' / f'package-{platform}-{arch}'
    for name in assets(release_version, arch, platform):
        if name.endswith(('.msi', '.dmg')):
            continue
        files = contents(directory / name)
        for path, data, mode in documents(release_version, platform, arch):
            data = data if isinstance(data, bytes) else data.read_bytes()
            if files.get(path) != (data, mode):
                raise ValueError(f'{name}: wrong document or BUILD-INFO: {path}')
        browser = name.startswith('spk-ocular-browser_')
        member = 'spk-ocular-browser' if browser else 'spk-ocular'
        if platform == 'windows':
            member += '.exe'
            source = stage / member
        else:
            source = stage / member if browser else stage / 'SPK Ocular.app/Contents/MacOS/spk-ocular'
            if not browser:
                member = 'SPK Ocular.app/Contents/MacOS/spk-ocular'
                plist = plistlib.loads(files['SPK Ocular.app/Contents/Info.plist'][0])
                if plist['CFBundleIdentifier'] != 'io.github.sipaha.SPKOcular' or plist['CFBundleShortVersionString'] != release_version.split('-')[0]:
                    raise ValueError('incorrect macOS bundle metadata')
                for p in (stage / 'SPK Ocular.app').rglob('*'):
                    if p.is_file() and files.get(p.relative_to(stage).as_posix()) != (p.read_bytes(), p.stat().st_mode & 0o777):
                        raise ValueError('archive differs from the signed app bundle')
        data, mode = files[member]
        if mode != 0o755 or binary_arch(data, platform, 3 if browser else 2) != arch or data != source.read_bytes():
            raise ValueError(f'{name}: wrong binary architecture, mode, subsystem or contents')
        # Running the actual native executable also catches missing runtime DLLs.
        with tempfile.TemporaryDirectory() as scratch:
            env = {**os.environ, 'HOME': scratch, 'USERPROFILE': scratch,
                   'DOCKER_CONFIG': scratch, 'SPK_OCULAR_HOME': scratch,
                   'KUBECONFIG': str(Path(scratch) / 'no-kubeconfig')}
            actual = subprocess.check_output([str(source), 'version'], env=env, text=True).strip()
            if actual != f'spk-ocular {release_version}':
                raise ValueError(f'wrong executable version: {actual}')
    if platform == 'darwin':
        dmg = directory / f'spk-ocular_{release_version}_darwin_{arch}.dmg'
        with tempfile.TemporaryDirectory() as scratch:
            mount = Path(scratch) / 'mounted'
            subprocess.run(['hdiutil', 'attach', '-readonly', '-nobrowse', '-mountpoint', str(mount), str(dmg)], check=True)
            try:
                app = mount / 'SPK Ocular.app'
                subprocess.run(['codesign', '--verify', '--deep', '--strict', str(app)], check=True)
                for p in (stage / app.name).rglob('*'):
                    if p.is_file() and (app / p.relative_to(stage / app.name)).read_bytes() != p.read_bytes():
                        raise ValueError('DMG differs from the signed bundle')
                if os.readlink(mount / 'Applications') != '/Applications':
                    raise ValueError('missing Applications shortcut')
            finally:
                subprocess.run(['hdiutil', 'detach', str(mount)], check=True)
    print(f'Validated {platform}/{arch} {release_version}: source contents, architecture, version, metadata, checksums')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--os', choices=['windows', 'darwin'], required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    args = parser.parse_args()
    release_version = version(args.version)
    verify(ROOT / 'dist' / release_version / f'{args.os}-{args.arch}', release_version, args.os, args.arch)
