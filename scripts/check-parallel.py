"""Run the complete gate in two lanes without sharing mutable embed staging."""

import argparse
import concurrent.futures
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, default=Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    source = args.source.resolve()
    scratch = Path(os.environ.get('OCULAR_SCRATCH_DIR', source / '.agents/tmp')).resolve()
    scratch.mkdir(parents=True, exist_ok=True)
    run = Path(tempfile.mkdtemp(prefix='check-', dir=scratch))
    backend = run / 'backend'
    backend.mkdir()
    # Copy current working bytes, including uncommitted/new source files. Never
    # build the Go lane against HEAD while checking a different working tree.
    names = subprocess.check_output(
        ['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=source
    ).decode().split('\0')
    for name in filter(None, names):
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError(f'Unsafe source path: {name}')
        original = source / relative
        if original.is_relative_to(scratch):
            continue
        if not original.exists() and not original.is_symlink():
            continue  # A tracked deletion is part of the current working tree.
        target = backend / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        if original.is_symlink():
            target.symlink_to(os.readlink(original))
        else:
            shutil.copy2(original, target)

    def lane(name, directory, targets):
        started = time.monotonic()
        print(f'[{name}] starting; log: {run / (name + ".log")}', flush=True)
        # Each lane stays serial. Only the two independent lanes overlap; all
        # frontend builds and both with_dist operations remain in the UI lane.
        with (run / (name + '.log')).open('w') as log:
            process = subprocess.Popen(
                ['make', '-j1', *targets], cwd=directory, stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT, text=True,
            )
            for line in process.stdout:
                log.write(line)
                log.flush()
                print(f'[{name}] {line}', end='', flush=True)
            code = process.wait()
        print(f'[{name}] exit={code} elapsed={time.monotonic() - started:.1f}s', flush=True)
        return code

    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        go = pool.submit(lane, 'go', backend, ['lint-go', 'test-go'])
        ui = pool.submit(lane, 'ui', source, [
            'lint-web', 'lint-workflows', 'test-web', 'test-e2e', 'test-packaging', 'build-desktop',
        ])
        results = [go.result(), ui.result()]
    if not any(results):
        shutil.rmtree(backend)
    return int(any(results))


if __name__ == '__main__':
    raise SystemExit(main())
