#!/usr/bin/env python3
"""Validate the release version taken from the Git tag, as in the launcher."""
import os
from pathlib import Path
from release import version

def validate(raw):
    release_version = version(raw)
    if raw != 'v' + release_version:
        raise ValueError('release tags must start with v')
    return release_version


if __name__ == '__main__':
    release_version = validate(os.environ['RELEASE_TAG'])
    with Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
        output.write(f'version={release_version}\nprerelease={str("-" in release_version).lower()}\n')
