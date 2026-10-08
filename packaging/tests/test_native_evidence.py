import importlib.util
import json
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('native_evidence', Path(__file__).parents[1] / 'native-evidence.py')
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


class NativeEvidence(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=evidence.ROOT, text=True).strip()
        for platform in ('linux', 'windows', 'darwin'):
            for arch in ('amd64', 'arm64'):
                directory = self.root / f'native-diagnostics-{platform}-{arch}'
                directory.mkdir()
                report = dict(platform=platform, arch=arch, version='1.0.1', commit=self.commit,
                              production=True, fixtureSynthetic=True, ownedWindow=True)
                (directory / 'report.json').write_text(json.dumps(report))
                data = b'\x89PNG\r\n\x1a\n' + struct.pack('>I', 13) + b'IHDR' + struct.pack('>II', 1024, 768) + bytes(4096)
                (directory / f'{platform}-{arch}.png').write_bytes(data)
                (directory / 'app.log').write_text('private data excluded')
        self.output = self.root / 'NATIVE-VERIFICATION.zip'

    def test_complete_evidence_excludes_logs_and_has_six_matching_reports(self):
        evidence.package(self.root, self.output, '1.0.1')
        with zipfile.ZipFile(self.output) as archive:
            self.assertEqual(len(archive.namelist()), 13)
            manifest = json.loads(archive.read('manifest.json'))
            self.assertEqual(len(manifest['reports']), 6)
            self.assertFalse(any(name.endswith('.log') for name in archive.namelist()))

    def test_wrong_source_version_platform_or_production_is_refused(self):
        path = self.root / 'native-diagnostics-linux-amd64/report.json'
        original = json.loads(path.read_text())
        for key, value in [('commit', 'wrong'), ('version', '0.0.0'), ('platform', 'windows'),
                           ('arch', 'arm64'), ('production', False), ('fixtureSynthetic', False), ('ownedWindow', False)]:
            with self.subTest(key=key):
                path.write_text(json.dumps(dict(original, **{key: value})))
                with self.assertRaisesRegex(ValueError, 'metadata mismatch'):
                    evidence.package(self.root, self.output, '1.0.1')

    def test_missing_report_or_screenshot_and_invalid_dimensions_are_refused(self):
        directory = self.root / 'native-diagnostics-darwin-arm64'
        image = directory / 'darwin-arm64.png'
        original = image.read_bytes()
        image.unlink()
        with self.assertRaisesRegex(ValueError, 'Missing native screenshot'):
            evidence.package(self.root, self.output, '1.0.1')
        image.write_bytes(b'not PNG' + bytes(4096))
        with self.assertRaisesRegex(ValueError, 'Invalid native PNG'):
            evidence.package(self.root, self.output, '1.0.1')
        image.write_bytes(original[:16] + struct.pack('>II', 799, 599) + original[24:])
        with self.assertRaisesRegex(ValueError, 'too small'):
            evidence.package(self.root, self.output, '1.0.1')
        image.write_bytes(original)
        (directory / 'report.json').unlink()
        with self.assertRaisesRegex(ValueError, 'Missing actual native report'):
            evidence.package(self.root, self.output, '1.0.1')
