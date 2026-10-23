"""Exercise the real Make browser gate with isolated command doubles."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


MAKEFILE = Path(__file__).resolve().parents[2] / 'Makefile'
CONFIGS = ['playwright.config.ts', 'playwright.synth.config.ts', 'playwright.memo.config.ts']


class BrowserGateTests(unittest.TestCase):
    def test_complete_gate_prepares_once_and_propagates_every_suite_failure(self):
        for failing in ('', 'prepare', *CONFIGS):
            with self.subTest(failing=failing), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'tests/e2e').mkdir(parents=True)
                tools = root / 'tools'
                tools.mkdir()
                pnpm = tools / 'pnpm'
                pnpm.write_text(f'#!{sys.executable}\n' + '''
import json, os, pathlib, sys
root = pathlib.Path(os.environ['BROWSER_GATE_ROOT'])
args = sys.argv[1:]
with (root / 'commands.jsonl').open('a') as output:
    output.write(json.dumps(args) + '\\n')
stage = args[-1] if '-c' in args else 'prepare'
raise SystemExit(9 if stage == os.environ['BROWSER_GATE_FAIL'] else 0)
''')
                pnpm.chmod(0o755)
                result = subprocess.run(
                    ['make', '-j1', '-f', str(MAKEFILE), '-o', 'build',
                     'ARCH=amd64', 'VERSION=fixture', 'test-e2e'],
                    cwd=root, env={**os.environ, 'PATH': str(tools) + os.pathsep + os.environ['PATH'],
                                   'BROWSER_GATE_ROOT': str(root), 'BROWSER_GATE_FAIL': failing},
                    text=True, capture_output=True, timeout=15,
                )
                self.assertEqual(result.returncode == 0, not failing, result.stdout + result.stderr)
                commands = [json.loads(line) for line in (root / 'commands.jsonl').read_text().splitlines()]
                if failing == 'prepare':
                    self.assertEqual(commands, [['install', '--frozen-lockfile', '--silent']])
                else:
                    self.assertEqual(commands[:2], [
                        ['install', '--frozen-lockfile', '--silent'],
                        ['exec', 'playwright', 'install', 'chromium'],
                    ])
                    expected = CONFIGS[:CONFIGS.index(failing) + 1] if failing else CONFIGS
                    self.assertEqual(commands[2:], [
                        ['exec', 'playwright', 'test', '-c', config] for config in expected
                    ])
