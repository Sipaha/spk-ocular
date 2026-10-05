#!/usr/bin/env python3
"""Inspect real release artifacts, including RPM payloads, without installing them."""
import argparse
import gzip
import hashlib
import io
import os
from pathlib import Path
import struct
import subprocess
import tarfile
import tempfile
from release import ROOT, elf_arch, version


def assets(release_version, arch):
    base = f'spk-ocular_{release_version}_linux_{arch}'
    return [base + ext for ext in ('.tar.gz', '.deb', '.rpm')] + [f'spk-ocular-browser_{release_version}_linux_{arch}.tar.gz']


def verify_checksums(directory, release_version, arches):
    expected = sorted(name for arch in arches for name in assets(release_version, arch))
    actual = sorted(p.name for p in directory.iterdir() if p.is_file() and p.name != 'SHA256SUMS')
    if actual != sorted(expected + [name + '.sha256' for name in expected]):
        raise ValueError(f'incomplete or unexpected asset set: {actual}')
    lines = []
    for name in expected:
        digest = hashlib.sha256((directory / name).read_bytes()).hexdigest()
        line = f'{digest}  {name}\n'
        if (directory / (name + '.sha256')).read_text() != line:
            raise ValueError(f'checksum mismatch: {name}')
        lines.append(line)
    return ''.join(lines)


def rpm_header(data, start):
    magic, ver, _, count, size = struct.unpack_from('>3sB4sII', data, start)
    if magic != b'\x8e\xad\xe8' or ver != 1:
        raise ValueError('invalid RPM header')
    store = start + 16 + count * 16
    values = {}
    for i in range(count):
        tag, typ, offset, length = struct.unpack_from('>IIII', data, start + 16 + i * 16)
        at = store + offset
        if typ in (6, 8, 9):
            values[tag] = data[at:store + size].split(b'\0', length)[:length]
    return values, store + size


def rpm_contents(path):
    data = path.read_bytes()
    if data[:4] != b'\xed\xab\xee\xdb':
        raise ValueError('invalid RPM lead')
    _, end = rpm_header(data, 96)
    tags, end = rpm_header(data, (end + 7) & ~7)
    payload = gzip.decompress(data[end:])
    files, offset = {}, 0
    while offset < len(payload):
        if payload[offset:offset + 6] not in (b'070701', b'070702'):
            raise ValueError('invalid RPM cpio payload')
        fields = [int(payload[offset + 6 + i * 8:offset + 14 + i * 8], 16) for i in range(13)]
        mode, size, namesize = fields[1], fields[6], fields[11]
        name = payload[offset + 110:offset + 110 + namesize - 1].decode()
        offset = (offset + 110 + namesize + 3) & ~3
        if name == 'TRAILER!!!':
            break
        files[name.removeprefix('./').lstrip('/')] = (payload[offset:offset + size], mode & 0o777)
        offset = (offset + size + 3) & ~3
    return {tag: [value.decode() for value in values] for tag, values in tags.items()}, files


def tar_contents(data):
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:*') as tar:
        files = {}
        for member in tar:
            if member.isfile():
                if '..' in Path(member.name).parts or member.name.startswith('/'):
                    raise ValueError('unsafe archive path')
                files[member.name.removeprefix('./')] = (tar.extractfile(member).read(), member.mode)
        return files


def verify_native(directory, release_version, arch):
    base = f'spk-ocular_{release_version}_linux_{arch}'
    deb = directory / (base + '.deb')
    for field, expected_value in [('Package', 'spk-ocular'), ('Architecture', arch), ('Maintainer', 'Pavel Simonov <sipahabk@gmail.com>')]:
        if subprocess.check_output(['dpkg-deb', '--field', str(deb), field], text=True).strip() != expected_value:
            raise ValueError(f'wrong DEB {field}')
    depends = subprocess.check_output(['dpkg-deb', '--field', str(deb), 'Depends'], text=True)
    for dependency in ['libc6 (>= 2.39)', 'libgtk-3-0', 'libwebkit2gtk-4.1-0']:
        if dependency not in depends:
            raise ValueError(f'DEB dependency missing: {dependency}')
    deb_version = subprocess.check_output(['dpkg-deb', '--field', str(deb), 'Version'], text=True).strip()
    if deb_version != release_version.replace('-', '~', 1):
        raise ValueError(f'wrong DEB version: {deb_version}')
    deb_files = tar_contents(subprocess.check_output(['dpkg-deb', '--fsys-tarfile', str(deb)]))
    tags, rpm_files = rpm_contents(directory / (base + '.rpm'))
    rpm_arch = {'amd64': 'x86_64', 'arm64': 'aarch64'}[arch]
    if tags[1000] != ['spk-ocular'] or tags[1022] != [rpm_arch] or tags[1014] != ['Apache-2.0']:
        raise ValueError('wrong RPM identity, architecture or license')
    base_version, sep, prerelease = release_version.partition('-')
    rpm_version = base_version + ('~' + prerelease.replace('-', '_') if sep else '')
    if tags[1001] != [rpm_version]:
        raise ValueError('wrong RPM version')
    for dependency in ['glibc', 'gtk3', 'webkit2gtk4.1']:
        if dependency not in tags[1049]:
            raise ValueError(f'RPM dependency missing: {dependency}')
    expected = {
        'usr/bin/spk-ocular': (ROOT / 'build/bin/spk-ocular-release', 0o755),
        'usr/share/applications/spk-ocular.desktop': (ROOT / 'packaging/linux/spk-ocular.desktop', 0o644),
        'usr/share/icons/hicolor/256x256/apps/spk-ocular.png': (ROOT / 'internal/appfiles/icons/icon.png', 0o644),
        'usr/share/icons/hicolor/scalable/apps/spk-ocular.svg': (ROOT / 'internal/appfiles/icons/icon.svg', 0o644),
        'usr/share/pixmaps/spk-ocular.png': (ROOT / 'internal/appfiles/icons/icon.png', 0o644),
        'usr/share/metainfo/io.github.sipaha.SPKOcular.metainfo.xml': (ROOT / 'packaging/linux/io.github.sipaha.SPKOcular.metainfo.xml', 0o644),
        'usr/share/doc/spk-ocular/LICENSE': (ROOT / 'LICENSE', 0o644),
    }
    for name, (source, mode) in expected.items():
        for kind, files in [('DEB', deb_files), ('RPM', rpm_files)]:
            if files.get(name) != (source.read_bytes(), mode):
                raise ValueError(f'{kind}: wrong contents or mode for {name}')
    for filename, member in [(base + '.tar.gz', 'spk-ocular'), (f'spk-ocular-browser_{release_version}_linux_{arch}.tar.gz', 'spk-ocular-browser')]:
        contents = tar_contents((directory / filename).read_bytes())
        documents = [ROOT / name for name in ('LICENSE', 'README.md', 'AGENTS.md', 'RELEASE_NOTES.md')]
        documents += [path for path in (ROOT / 'docs').rglob('*') if path.is_file()]
        for path in documents:
            if contents.get(str(path.relative_to(ROOT))) != (path.read_bytes(), 0o644):
                raise ValueError(f'{filename}: missing or incorrect document {path.name}')
        for required in [member, 'BUILD-INFO']:
            if required not in contents:
                raise ValueError(f'{filename}: missing {required}')
        data, mode = contents[member]
        if mode != 0o755:
            raise ValueError('archive binary must be executable')
        source = ROOT / ('build/bin/spk-ocular-release' if member == 'spk-ocular' else 'build/bin/spk-ocular')
        if data != source.read_bytes():
            raise ValueError('archive binary differs from the verified build')
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            binary = root / member
            binary.write_bytes(data)
            binary.chmod(mode)
            if elf_arch(binary) != arch:
                raise ValueError('archive architecture mismatch')
            env = {**os.environ, 'HOME': str(root / 'home'), 'DOCKER_CONFIG': str(root / 'docker'), 'SPK_OCULAR_HOME': str(root / 'data'), 'KUBECONFIG': str(root / 'no-kubeconfig')}
            actual = subprocess.check_output([str(binary), 'version'], env=env, text=True).strip()
            if actual != f'spk-ocular {release_version}':
                raise ValueError('archive version mismatch')
    print(f'Validated DEB, RPM and both archives for linux/{arch} {release_version}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    choice = parser.add_mutually_exclusive_group(required=True)
    choice.add_argument('--arch', choices=['amd64', 'arm64'])
    choice.add_argument('--all-architectures', action='store_true')
    parser.add_argument('--directory', type=Path)
    args = parser.parse_args()
    release_version = version(args.version)
    directory = args.directory or ROOT / 'dist' / release_version / f'linux-{args.arch}'
    checksums = verify_checksums(directory, release_version, ['amd64', 'arm64'] if args.all_architectures else [args.arch])
    if args.all_architectures:
        (directory / 'SHA256SUMS').write_text(checksums)
        print('Verified the complete release asset set and wrote SHA256SUMS')
    else:
        verify_native(directory, release_version, args.arch)
