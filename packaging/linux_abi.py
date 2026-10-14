"""Fail packaging when a native binary exceeds the verified Linux glibc floor."""
import re
import subprocess

MIN_GLIBC = '2.35'


def version_tuple(value):
    parts = tuple(int(part) for part in value.split('.'))
    while len(parts) > 2 and parts[-1] == 0:
        parts = parts[:-1]
    return parts + (0,) * max(0, 3 - len(parts))


def verify_glibc_floor(binary):
    output = subprocess.check_output(['readelf', '--version-info', '--wide', str(binary)], text=True)
    versions = set(re.findall(r'\bGLIBC_(\d+(?:\.\d+)+)\b', output))
    if not versions:
        raise ValueError('native Linux executable has no inspected glibc version requirements')
    maximum = max(versions, key=version_tuple)
    if version_tuple(maximum) > version_tuple(MIN_GLIBC):
        raise ValueError(f'native executable requires glibc {maximum}, above package floor {MIN_GLIBC}')
    return maximum
