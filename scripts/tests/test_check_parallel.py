import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / 'check-parallel.py'


class ParallelGateTests(unittest.TestCase):
    def test_current_source_isolation_concurrency_and_both_failure_directions(self):
        for failing in ('', 'go', 'ui'):
            with self.subTest(failing=failing), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                source = root / 'source'
                source.mkdir()
                subprocess.run(['git', 'init', '-q', str(source)], check=True)
                (source / 'current.txt').write_text('staged')
                (source / 'deleted.txt').write_text('deleted')
                (source / 'embed.txt').write_text('original')
                subprocess.run(['git', 'add', '.'], cwd=source, check=True)
                (source / 'current.txt').write_text('working bytes')
                (source / 'deleted.txt').unlink()
                (source / 'new.txt').write_text('new bytes')
                tools = root / 'tools'
                tools.mkdir()
                make = tools / 'make'
                make.write_text(f'#!{sys.executable}\n' + '''
import json, os, pathlib, sys, time
root = pathlib.Path(os.environ['GATE_TEST_ROOT'])
name = 'go' if 'lint-go' in sys.argv else 'ui'
(root / (name + '.started')).write_text(json.dumps(sys.argv[1:]))
other = 'ui' if name == 'go' else 'go'
deadline = time.monotonic() + 5
while not (root / (other + '.started')).exists():
    if time.monotonic() > deadline:
        raise RuntimeError('The two independent lanes did not overlap')
    time.sleep(0.01)
assert pathlib.Path('current.txt').read_text() == 'working bytes'
assert pathlib.Path('new.txt').read_text() == 'new bytes'
assert not pathlib.Path('deleted.txt').exists()
if name == 'go':
    pathlib.Path('embed.txt').write_text('backend only')
    (root / 'backend.modified').touch()
else:
    while not (root / 'backend.modified').exists():
        if time.monotonic() > deadline:
            raise RuntimeError('Backend fixture did not finish its write')
        time.sleep(0.01)
    assert pathlib.Path('embed.txt').read_text() == 'original'
raise SystemExit(7 if name == os.environ['GATE_TEST_FAIL'] else 0)
''')
                make.chmod(0o755)
                environment = {
                    **os.environ, 'PATH': str(tools) + os.pathsep + os.environ['PATH'],
                    'OCULAR_SCRATCH_DIR': str(root / 'scratch'),
                    'GATE_TEST_ROOT': str(root), 'GATE_TEST_FAIL': failing,
                }
                result = subprocess.run(
                    [sys.executable, str(SCRIPT), '--source', str(source)],
                    env=environment, text=True, capture_output=True, timeout=15,
                )
                self.assertEqual(result.returncode, int(bool(failing)), result.stdout + result.stderr)
                self.assertEqual(json.loads((root / 'go.started').read_text()), ['-j1', 'lint-go', 'test-go'])
                self.assertEqual(json.loads((root / 'ui.started').read_text()), [
                    '-j1', 'lint-web', 'lint-workflows', 'test-web', 'test-e2e',
                    'test-packaging', 'build-desktop',
                ])
                self.assertEqual((source / 'embed.txt').read_text(), 'original')
