#!/usr/bin/env python3
"""Native Windows/macOS distribution, following the launcher's MSI and app/DMG layout."""
import argparse
import hashlib
import os
from pathlib import Path
import plistlib
import shutil
import struct
import subprocess
import tempfile
import time
import zipfile

from release import ROOT, archive, version


def run(*args, **kwargs):
    return subprocess.run([str(a) for a in args], cwd=ROOT, check=True, **kwargs)


def binary_arch(data, platform, subsystem=None):
    if platform == 'windows':
        if data[:2] != b'MZ' or len(data) < 64:
            raise ValueError('expected a PE executable')
        offset = struct.unpack_from('<I', data, 60)[0]
        if data[offset:offset + 4] != b'PE\0\0':
            raise ValueError('invalid PE signature')
        machine = struct.unpack_from('<H', data, offset + 4)[0]
        if subsystem is not None and struct.unpack_from('<H', data, offset + 24 + 68)[0] != subsystem:
            raise ValueError('incorrect PE subsystem')
        return {0x8664: 'amd64', 0xAA64: 'arm64'}.get(machine, 'unsupported')
    if data[:4] != b'\xcf\xfa\xed\xfe':
        raise ValueError('expected a 64-bit little-endian Mach-O')
    return {0x01000007: 'amd64', 0x0100000C: 'arm64'}.get(struct.unpack_from('<I', data, 4)[0], 'unsupported')


def zip_archive(path, entries, epoch):
    stamp = time.gmtime(max(epoch, 315532800))[:6]
    with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED) as output:
        for name, source, mode in sorted(entries):
            info = zipfile.ZipInfo(name, stamp)
            info.create_system = 3
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, source if isinstance(source, bytes) else source.read_bytes())


def documents(release_version, platform, arch):
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    entries = [('BUILD-INFO', f'version={release_version}\ncommit={commit}\nplatform={platform}/{arch}\n'.encode(), 0o644)]
    entries += [(name, ROOT / name, 0o644) for name in ('LICENSE', 'README.md', 'AGENTS.md', 'RELEASE_NOTES.md')]
    entries += [(p.relative_to(ROOT).as_posix(), p, 0o644) for p in sorted((ROOT / 'docs').rglob('*')) if p.is_file()]
    return entries


def msi_version(value):
    # Windows Installer has a numeric three-field version. The exact SemVer is
    # retained in the filename, BUILD-INFO, display name and executable.
    result = version(value).split('-', 1)[0]
    if any(n > limit for n, limit in zip(map(int, result.split('.')), (255, 255, 65535))):
        raise ValueError('version exceeds Windows Installer limits (255.255.65535)')
    return result


def make_icons(directory):
    png = (ROOT / 'internal/appfiles/icons/icon.png').read_bytes()
    ico = directory / 'app.ico'
    ico.write_bytes(struct.pack('<HHHBBBBHHII', 0, 1, 1, 0, 0, 0, 0, 1, 32, len(png), 22) + png)
    icns = directory / 'app.icns'
    icns.write_bytes(b'icns' + struct.pack('>I', len(png) + 16) + b'ic08' + struct.pack('>I', len(png) + 8) + png)
    return ico, icns


def build(release_version, platform, arch, stage):
    env = {**os.environ, 'GOOS': platform, 'GOARCH': arch, 'CGO_ENABLED': '0' if platform == 'windows' else '1'}
    if platform == 'darwin':
        env['MACOSX_DEPLOYMENT_TARGET'] = '12.0'
        env['CGO_CFLAGS'] = env.get('CGO_CFLAGS', '') + ' -mmacosx-version-min=12.0'
        env['CGO_LDFLAGS'] = env.get('CGO_LDFLAGS', '') + ' -mmacosx-version-min=12.0'
    run(shutil.which('pnpm') or 'pnpm', '--dir', 'web', 'install', '--frozen-lockfile')
    run(shutil.which('pnpm') or 'pnpm', '--dir', 'web', 'build')
    dist = ROOT / 'cmd/spk-ocular/dist'
    resource = ROOT / f'cmd/spk-ocular/rsrc_windows_{arch}.syso'
    suffix = '.exe' if platform == 'windows' else ''
    desktop = stage / ('spk-ocular' + suffix)
    browser = stage / ('spk-ocular-browser' + suffix)
    flags = f'-s -w -X main.version={release_version}'
    try:
        shutil.rmtree(dist)
        shutil.copytree(ROOT / 'web/dist', dist)
        # The browser binary keeps a console and does not link desktop resources.
        run('go', 'build', '-trimpath', '-ldflags', flags, '-o', browser, './cmd/spk-ocular', env={**env, 'CGO_ENABLED': '0'})
        if platform == 'windows':
            ico, _ = make_icons(stage)
            run('go', 'run', 'github.com/akavel/rsrc@v0.10.2', '-ico', ico,
                '-manifest', 'packaging/windows/app.manifest', '-arch', arch, '-o', resource)
            flags += ' -H windowsgui'
        run('go', 'build', '-trimpath', '-tags', 'wails production', '-ldflags', flags, '-o', desktop, './cmd/spk-ocular', env=env)
    finally:
        resource.unlink(missing_ok=True)
        shutil.rmtree(dist)
        dist.mkdir()
        (dist / '.gitkeep').touch()
    for binary, subsystem in [(desktop, 2), (browser, 3)]:
        if binary_arch(binary.read_bytes(), platform, subsystem) != arch:
            raise ValueError(f'wrong architecture: {binary}')
    return desktop, browser


def package(release_version, platform, arch):
    release_version = version(release_version)
    host = subprocess.check_output(['go', 'env', 'GOHOSTOS', 'GOHOSTARCH'], text=True).split()
    if host != [platform, arch]:
        raise ValueError('packages must be built and verified on a native runner of the requested platform and architecture')
    output = ROOT / 'dist' / release_version / f'{platform}-{arch}'
    output.mkdir(parents=True, exist_ok=True)
    stage = ROOT / 'build' / f'package-{platform}-{arch}'
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir(parents=True)
    desktop, browser = build(release_version, platform, arch, stage)
    common = documents(release_version, platform, arch)
    epoch = int(subprocess.check_output(['git', 'log', '-1', '--format=%ct'], cwd=ROOT, text=True))
    base = f'spk-ocular_{release_version}_{platform}_{arch}'
    browser_base = f'spk-ocular-browser_{release_version}_{platform}_{arch}'
    if platform == 'windows':
        zip_archive(output / (base + '.zip'), common + [('spk-ocular.exe', desktop, 0o755)], epoch)
        zip_archive(output / (browser_base + '.zip'), common + [('spk-ocular-browser.exe', browser, 0o755)], epoch)
        run('wix', 'build', 'packaging/windows/spk-ocular.wxs', '-arch', 'x64' if arch == 'amd64' else 'arm64',
            '-d', f'Version={msi_version(release_version)}', '-d', f'DisplayVersion={release_version}',
            '-d', f'Stage={stage}', '-o', output / (base + '.msi'), '-pdbtype', 'none')
    else:
        app = stage / 'SPK Ocular.app'
        macos, resources = app / 'Contents/MacOS', app / 'Contents/Resources'
        macos.mkdir(parents=True)
        resources.mkdir()
        shutil.copy2(desktop, macos / 'spk-ocular')
        _, icns = make_icons(stage)
        shutil.copy2(icns, resources / 'app.icns')
        info = {'CFBundleName': 'SPK Ocular', 'CFBundleDisplayName': 'SPK Ocular', 'CFBundleExecutable': 'spk-ocular',
                'CFBundleIdentifier': 'io.github.sipaha.SPKOcular', 'CFBundleIconFile': 'app.icns', 'CFBundlePackageType': 'APPL',
                'CFBundleShortVersionString': release_version.split('-')[0], 'CFBundleVersion': release_version.split('-')[0],
                'LSMinimumSystemVersion': '12.0', 'NSHighResolutionCapable': True, 'NSHumanReadableCopyright': 'SPK Ocular contributors'}
        (app / 'Contents/Info.plist').write_bytes(plistlib.dumps(info))
        for name, source, _ in common:
            target = resources / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(source if isinstance(source, bytes) else source.read_bytes())
        # Like the launcher: seal the final bundle, then create the DMG. An ad-hoc
        # signature is not notarization; errors must still fail the build.
        run('codesign', '--force', '--sign', '-', '--timestamp=none', app)
        run('codesign', '--verify', '--deep', '--strict', app)
        entries = [(p.relative_to(stage).as_posix(), p, p.stat().st_mode & 0o777) for p in app.rglob('*') if p.is_file()]
        archive(output / (base + '.tar.gz'), common + entries, epoch)
        archive(output / (browser_base + '.tar.gz'), common + [('spk-ocular-browser', browser, 0o755)], epoch)
        with tempfile.TemporaryDirectory() as scratch:
            dmg_stage = Path(scratch)
            run('ditto', app, dmg_stage / app.name)
            (dmg_stage / 'Applications').symlink_to('/Applications')
            run('hdiutil', 'create', '-volname', 'SPK Ocular', '-srcfolder', dmg_stage, '-ov', '-format', 'UDZO', output / (base + '.dmg'))
    for path in output.iterdir():
        if path.name.endswith('.sha256'):
            continue
        path.with_name(path.name + '.sha256').write_text(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n', encoding='utf-8', newline='\n')
    print(output)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--os', choices=['darwin', 'windows'], required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    args = parser.parse_args()
    package(args.version, args.os, args.arch)
