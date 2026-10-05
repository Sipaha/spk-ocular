#!/usr/bin/env python3
"""Finish a draft only after the complete, verified asset set is uploaded."""
import json
import os
import subprocess
from release import ROOT, version
from verify import PLATFORMS, verify_checksums


def release_notes(root, release_version):
    # Same lookup and fallback as citeck-launcher's release-go.yml. Keep the
    # exact tagged version's English file; localized files live beside it.
    source = root / 'changelog' / release_version / 'en.md'
    if source.is_file():
        return source
    scratch = root / '.agents/tmp/publish'
    scratch.mkdir(parents=True, exist_ok=True)
    fallback = scratch / 'release-notes.md'
    fallback.write_text(f'Release {release_version}\n', encoding='utf-8')
    return fallback


def publish(tag, root=ROOT):
    release_version = version(tag)
    if tag != 'v' + release_version:
        raise ValueError('release tags must start with v')
    directory = root / 'dist/release'
    checksums = verify_checksums(directory, release_version, ['amd64', 'arm64'], PLATFORMS)
    if (directory / 'SHA256SUMS').read_text() != checksums:
        raise ValueError('SHA256SUMS does not match the verified assets')
    files = sorted(path for path in directory.iterdir() if path.is_file())
    expected = {path.name: path.stat().st_size for path in files}
    notes = release_notes(root, release_version)
    args = ['gh', 'release']
    view = args + ['view', tag, '--json', 'isDraft,assets']
    existing = subprocess.run(view, capture_output=True, text=True)
    if existing.returncode == 0:
        state = json.loads(existing.stdout)
        if not state['isDraft']:
            raise ValueError('published releases are immutable; choose a new version')
        if any(asset['name'] not in expected for asset in state['assets']):
            raise ValueError('draft contains unexpected assets; review them before retrying')
    else:
        subprocess.run(args + ['create', tag, '--verify-tag', '--draft', '--title', tag, '--notes-file', str(notes)], check=True)
    subprocess.run(args + ['upload', tag, *map(str, files), '--clobber'], check=True)
    uploaded = json.loads(subprocess.check_output(view, text=True))
    actual = {asset['name']: asset['size'] for asset in uploaded['assets']}
    if not uploaded['isDraft'] or len(uploaded['assets']) != len(expected) or actual != expected:
        raise ValueError('remote draft does not contain exactly the complete uploaded asset set')
    subprocess.run(args + ['edit', tag, '--draft=false', '--prerelease=' + str('-' in release_version).lower(), '--notes-file', str(notes)], check=True)


if __name__ == '__main__':
    publish(os.environ['RELEASE_TAG'])
