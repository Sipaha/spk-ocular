#!/usr/bin/env python3
"""Reject mistyped tags and missing notes before starting expensive release jobs."""
import os
from pathlib import Path
from release import ROOT, version

def validate(raw, root=ROOT):
    release_version = version(raw)
    if raw != 'v' + release_version:
        raise ValueError('release tags must start with v')
    if (root / 'VERSION').read_text().strip() != release_version:
        raise ValueError('release tag must match VERSION')
    notes = (root / 'RELEASE_NOTES.md').read_text().strip().splitlines()
    if not notes or notes[0] != f'## SPK Ocular {release_version}' or not any(line.startswith('- ') for line in notes):
        raise ValueError('RELEASE_NOTES.md must describe this version')
    return release_version


if __name__ == '__main__':
    release_version = validate(os.environ['RELEASE_TAG'])
    with Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
        output.write(f'version={release_version}\nprerelease={str("-" in release_version).lower()}\n')
