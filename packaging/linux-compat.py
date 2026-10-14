#!/usr/bin/env python3
"""Install and exercise a native DEB in an owned, pinned Ubuntu 22.04 userspace."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import urllib.request

from linux_abi import MIN_GLIBC, verify_glibc_floor
from release import ROOT, elf_arch, version

ROOTFS_DATE = '20260930'
ROOTFS_SHA256 = {
    'amd64': 'bf77f4dbc4b9bdca141695de07c6cf67f962d18a948fae58af3bce02c35f7b96',
    'arm64': '1488ed73cd8917044116953d7c0f860009c6d9d241dcadd43a7c61fca450fb07',
}


def failure_diagnostics(arch, error):
    """Expose bounded, fixture-only errors even when CI log downloads need auth."""
    details = [str(error)]
    scratch = Path(os.environ['OCULAR_SCRATCH_DIR']).resolve() / 'linux-compatibility' / arch
    for name in ('install.log', 'runtime.log'):
        path = scratch / 'work' / name
        if path.is_file():
            details.append(name + ':\n' + path.read_bytes()[-4096:].decode('utf-8', errors='replace'))
    message = '\n'.join(details)
    print(message, file=sys.stderr)
    if os.environ.get('GITHUB_ACTIONS') == 'true':
        escaped = message.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
        print('::error title=Owned Ubuntu 22.04 compatibility fixture::' + escaped, flush=True)


def rootfs_url(arch):
    return f'https://partner-images.canonical.com/oci/jammy/{ROOTFS_DATE}/ubuntu-jammy-oci-{arch}-root.tar.gz'


def native_arch():
    return {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine(), 'unsupported')


def prepare_rootfs(scratch, arch):
    rootfs = scratch / 'rootfs'
    marker = scratch / 'rootfs-owner.json'
    expected = {'arch': arch, 'url': rootfs_url(arch), 'sha256': ROOTFS_SHA256[arch]}
    if rootfs.exists():
        if rootfs.is_symlink() or not marker.is_file() or json.loads(marker.read_text()) != expected:
            raise ValueError('refusing a rootfs without this exact owned fixture identity')
        return rootfs
    archive = scratch / 'ubuntu-rootfs.tar.gz'
    if not archive.exists():
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(expected['url'], timeout=60) as response, archive.open('wb') as output:
            shutil.copyfileobj(response, output)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != expected['sha256']:
        raise ValueError('Ubuntu rootfs SHA256 mismatch')
    with tarfile.open(archive) as data:
        if any(m.name.startswith('/') or '..' in Path(m.name).parts for m in data):
            raise ValueError('unsafe rootfs member path')
    rootfs.mkdir()
    subprocess.run(['tar', '-xf', str(archive), '-C', str(rootfs), '--no-same-owner',
                    '--exclude=./dev/*'], check=True)
    marker.write_text(json.dumps(expected, indent=2) + '\n')
    resolver = rootfs / 'etc/resolv.conf'
    resolver.unlink(missing_ok=True)
    shutil.copyfile('/etc/resolv.conf', resolver)
    policy = rootfs / 'usr/sbin/policy-rc.d'
    policy.write_text('#!/bin/sh\nexit 101\n')
    policy.chmod(0o755)
    return rootfs


def sandbox(rootfs, work, artifacts, setup):
    # Only this owned rootfs and scratch are writable. No host home, daemon
    # socket, display, runtime directory or source tree is writable/mounted.
    args = ['bwrap', '--bind' if setup else '--ro-bind', str(rootfs), '/', '--bind', str(work), '/verify',
            '--bind', str(work / 'tmp'), '/tmp',
            '--ro-bind', str(artifacts), '/artifacts', '--ro-bind', str(ROOT / 'packaging'), '/packaging',
            '--proc', '/proc', '--dev', '/dev', '--unshare-pid', '--unshare-ipc', '--unshare-uts',
            '--hostname', 'ocular-linux-compat', '--die-with-parent', '--new-session', '--clearenv',
            '--chdir', '/packaging']
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': '/verify/home',
           'TMPDIR': '/verify/tmp', 'OCULAR_SCRATCH_DIR': '/verify', 'PYTHONDONTWRITEBYTECODE': '1',
           'DEBIAN_FRONTEND': 'noninteractive', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
           'LD_BIND_NOW': '1'}
    for key, value in env.items():
        args += ['--setenv', key, value]
    if setup:
        # Complete UID/GID mappings allow normal package ownership, rather
        # than force flags or fake dependency records. Local non-root runs use
        # only the operator's preconfigured subordinate ranges.
        args += ['--cap-drop', 'ALL']
        for capability in ('CAP_CHOWN', 'CAP_DAC_OVERRIDE', 'CAP_FOWNER',
                           'CAP_SETUID', 'CAP_SETGID', 'CAP_SETFCAP'):
            args += ['--cap-add', capability]
        # Privileged disposable CI runners already have all package IDs.
        # Do not ask newuidmap to remap root's IDs through absent subuid rules.
        prefix = [] if os.geteuid() == 0 else ['unshare', '--user', '--map-auto', '--map-root-user']
        return prefix + args
    if os.geteuid() == 0:
        # Resolve/mount the runner's private checkout as root first. Changing
        # bwrap's UID before mount setup cannot traverse a runner-owned 0700
        # parent. Drop identity and every capability inside the owned mount
        # namespace instead; the application never runs as root.
        return args + ['--unshare-net', '--cap-drop', 'ALL',
                       '--cap-add', 'CAP_SETUID', '--cap-add', 'CAP_SETGID',
                       '--cap-add', 'CAP_SETPCAP', 'setpriv', '--reuid=1000',
                       '--regid=1000', '--clear-groups', '--bounding-set=-all',
                       '--no-new-privs', '--']
    return args + ['--unshare-user', '--uid', '1000', '--gid', '1000', '--unshare-net']


def verify(arch, release_version, artifacts):
    release_version = version(release_version)
    if native_arch() != arch:
        raise ValueError('older-userspace GTK verification must run natively on the requested architecture')
    artifacts = artifacts.resolve()
    deb = artifacts / f'spk-ocular_{release_version}_linux_{arch}.deb'
    if not deb.is_file():
        raise ValueError('missing exact version/architecture DEB')
    scratch = Path(os.environ['OCULAR_SCRATCH_DIR']).resolve() / 'linux-compatibility' / arch
    if not (scratch.is_relative_to(ROOT) or scratch.is_relative_to(ROOT.parent / '.tmp')):
        raise ValueError('compatibility scratch must be inside the project or Solution .tmp')
    scratch.mkdir(parents=True, exist_ok=True)
    with (scratch / 'fixture.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        rootfs = prepare_rootfs(scratch, arch)
        work = scratch / 'work'
        for name in ('', 'home', 'tmp'):
            (work / name).mkdir(exist_ok=True)
            if os.geteuid() == 0:
                os.chown(work / name, 1000, 1000)
        setup = sandbox(rootfs, work, artifacts, True)
        packages = ['libgtk-3-0', 'libwebkit2gtk-4.1-0', 'python3', 'binutils', 'xvfb', 'xauth',
                    'x11-utils', 'imagemagick', 'fonts-dejavu-core']
        with (work / 'install.log').open('w') as log:
            subprocess.run(setup + ['apt-get', '-o', 'APT::Sandbox::User=root', 'update'],
                           stdout=log, stderr=log, check=True)
            subprocess.run(setup + ['apt-get', '-o', 'APT::Sandbox::User=root', 'install', '-y',
                                    '--no-install-recommends', *packages, '/artifacts/' + deb.name],
                           stdout=log, stderr=log, check=True)
        passwd = rootfs / 'etc/passwd'
        if not any(line.split(':')[2] == '1000' for line in passwd.read_text().splitlines()):
            with passwd.open('a') as output:
                output.write('ocular:x:1000:1000:Owned Ocular fixture:/verify/home:/bin/bash\n')
            with (rootfs / 'etc/group').open('a') as output:
                output.write('ocular:x:1000:\n')
        run = sandbox(rootfs, work, artifacts, False)
        identity = json.loads(subprocess.check_output(run + ['python3', '-c',
            'import json,os; s=dict(line.split(":",1) for line in open("/proc/self/status") if ":" in line); '
            'print(json.dumps({"uid":os.geteuid(),"gid":os.getegid(),"capEff":s["CapEff"].strip(),'
            '"capBnd":s["CapBnd"].strip(),"noNewPrivs":s["NoNewPrivs"].strip()}))'], text=True))
        if (identity['uid'] != 1000 or identity['gid'] != 1000 or int(identity['capEff'], 16) != 0
                or identity['noNewPrivs'] != '1'
                or (os.geteuid() == 0 and int(identity['capBnd'], 16) != 0)):
            raise ValueError('compatibility application runtime retained a privileged identity or capability')
        glibc = subprocess.check_output(run + ['getconf', 'GNU_LIBC_VERSION'], text=True).strip()
        if glibc != 'glibc ' + MIN_GLIBC:
            raise ValueError('compatibility environment did not retain the exact tested glibc baseline')
        installed = rootfs / 'usr/bin/spk-ocular'
        if elf_arch(installed) != arch:
            raise ValueError('installed executable architecture mismatch')
        required = verify_glibc_floor(installed)
        actual_version = subprocess.check_output(run + ['/usr/bin/spk-ocular', 'version'], text=True).strip()
        if actual_version != 'spk-ocular ' + release_version:
            raise ValueError('installed executable version mismatch')
        with (work / 'runtime.log').open('w') as log:
            subprocess.run(run + ['python3', '-c', 'from browser_smoke import verify_browser; verify_browser("/usr/bin/spk-ocular")'],
                           stdout=log, stderr=log, check=True)
            commit = subprocess.check_output(['git', '-c', 'safe.directory=' + str(ROOT), 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
            subprocess.run(run + ['xvfb-run', '-a', 'python3', '/packaging/smoke-native.py',
                                    '--os', 'linux', '--arch', arch, '--version', release_version,
                                    '--binary', '/usr/bin/spk-ocular', '--commit', commit],
                           stdout=log, stderr=log, check=True)
        report = json.loads((work / 'native-smoke/report.json').read_text())
        report.update(baseline='ubuntu-22.04', glibc=MIN_GLIBC, maximumGLIBCSymbol=required,
                      packageInstalled=True, browserNoDisplay=True, mode='native', kernelShared=True,
                      rootfsSHA256=ROOTFS_SHA256[arch], binarySHA256=hashlib.sha256(installed.read_bytes()).hexdigest())
        target = Path(os.environ['OCULAR_SCRATCH_DIR']) / 'native-smoke'
        target.mkdir(parents=True, exist_ok=True)
        image_name = f'linux-compat-{arch}.png'
        shutil.copyfile(work / f'native-smoke/linux-{arch}.png', target / image_name)
        report['screenshot'] = image_name
        (target / 'linux-compat-report.json').write_text(json.dumps(report, indent=2) + '\n')
        print(f'PASS native {arch}: normal DEB installation, glibc {MIN_GLIBC}, browser and owned GTK view on Ubuntu 22.04')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--artifacts', type=Path, required=True)
    args = parser.parse_args()
    try:
        verify(args.arch, args.version, args.artifacts)
    except Exception as error:
        failure_diagnostics(args.arch, error)
        raise
