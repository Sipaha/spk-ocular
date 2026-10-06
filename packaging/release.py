#!/usr/bin/env python3
"""Build native Linux packages and portable archives; never install or publish them."""
import argparse
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def version(value):
    value = value.removeprefix('v')
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?', value):
        raise ValueError('version must be MAJOR.MINOR.PATCH with an optional prerelease')
    if '-' in value:
        for part in value.split('-', 1)[1].split('.'):
            if part.isdigit() and len(part) > 1 and part.startswith('0'):
                raise ValueError('numeric prerelease identifiers must not have leading zeroes')
    return value


def elf_arch(path):
    with path.open('rb') as stream:
        head = stream.read(20)
    if len(head) != 20 or head[:6] != b'\x7fELF\x02\x01':
        raise ValueError(f'{path}: expected a 64-bit little-endian ELF')
    return {62: 'amd64', 183: 'arm64'}.get(int.from_bytes(head[18:20], 'little'), 'unsupported')


def archive(path, entries, epoch):
    # Stable owners, modes, order and timestamps; do not capture a developer's home.
    with path.open('wb') as output, gzip.GzipFile(fileobj=output, mode='wb', filename='', mtime=epoch) as zipped:
        with tarfile.open(fileobj=zipped, mode='w') as tar:
            for name, source, mode in sorted(entries):
                data = source if isinstance(source, bytes) else source.read_bytes()
                info = tarfile.TarInfo(name)
                info.size, info.mode, info.mtime = len(data), mode, epoch
                tar.addfile(info, io.BytesIO(data))


def document_paths(root=ROOT):
    paths = [root / name for name in ('LICENSE', 'THIRD-PARTY-NOTICES.txt', 'README.md', 'AGENTS.md')]
    paths += sorted(path for directory in ('docs', 'changelog') for path in (root / directory).rglob('*') if path.is_file())
    return paths


def package(release_version, arch):
    release_version = version(release_version)
    host = subprocess.check_output(['go', 'env', 'GOHOSTOS', 'GOHOSTARCH'], text=True).split()
    if host != ['linux', arch]:
        raise ValueError('desktop packages must be built on a native Linux runner of the requested architecture')
    subprocess.run(['make', 'release', 'build-go', f'VERSION={release_version}'], cwd=ROOT, check=True)
    desktop, browser = ROOT / 'build/bin/spk-ocular-release', ROOT / 'build/bin/spk-ocular'
    for binary in (desktop, browser):
        if elf_arch(binary) != arch:
            raise ValueError(f'wrong architecture: {binary}')
        with tempfile.TemporaryDirectory() as scratch:
            profile = Path(scratch)
            isolated = {**os.environ, 'HOME': str(profile / 'home'), 'DOCKER_CONFIG': str(profile / 'docker'), 'SPK_OCULAR_HOME': str(profile / 'data'), 'KUBECONFIG': str(profile / 'no-kubeconfig')}
            actual = subprocess.check_output([str(binary), 'version'], env=isolated, text=True).strip()
        if actual != f'spk-ocular {release_version}':
            raise ValueError(f'wrong embedded version: {actual}')
    output = ROOT / 'dist' / release_version / f'linux-{arch}'
    output.mkdir(parents=True, exist_ok=True)
    epoch = int(subprocess.check_output(['git', 'log', '-1', '--format=%ct'], cwd=ROOT, text=True))
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    info = f'version={release_version}\ncommit={commit}\nplatform=linux/{arch}\n'.encode()
    common = [('BUILD-INFO', info, 0o644)]
    common += [(path.relative_to(ROOT).as_posix(), path, 0o644) for path in document_paths()]
    desktop_archive = output / f'spk-ocular_{release_version}_linux_{arch}.tar.gz'
    archive(desktop_archive, common + [
        ('spk-ocular', desktop, 0o755),
        ('spk-ocular.desktop', ROOT / 'packaging/linux/spk-ocular.desktop', 0o644),
        ('spk-ocular.png', ROOT / 'internal/appfiles/icons/icon.png', 0o644),
    ], epoch)
    browser_archive = output / f'spk-ocular-browser_{release_version}_linux_{arch}.tar.gz'
    archive(browser_archive, common + [('spk-ocular-browser', browser, 0o755)], epoch)
    artifacts = [desktop_archive, browser_archive]
    env = {**os.environ, 'RELEASE_VERSION': release_version, 'ARCH': arch, 'SOURCE_DATE_EPOCH': str(epoch)}
    for kind in ('deb', 'rpm'):
        path = output / f'spk-ocular_{release_version}_linux_{arch}.{kind}'
        subprocess.run(['nfpm', 'package', '--config', 'packaging/nfpm.yaml', '--packager', kind, '--target', str(path)], cwd=ROOT, env=env, check=True)
        artifacts.append(path)
    for path in artifacts:
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        path.with_name(path.name + '.sha256').write_text(f'{digest}  {path.name}\n')
    print(output)
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    args = parser.parse_args()
    try:
        package(args.version, args.arch)
    except (ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
