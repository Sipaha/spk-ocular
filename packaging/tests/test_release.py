import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('release', Path(__file__).parents[1] / 'release.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseInputs(unittest.TestCase):
    def test_versions_are_safe_filenames_and_valid_semver(self):
        for value in ['0.1.0', 'v1.2.3', '1.2.3-rc.1', '0.1.0-dev.42']:
            self.assertEqual(release.version(value), value.removeprefix('v'))
        for value in ['latest', 'v1.2', '1.02.3', '1.2.3-01', '1.2.3-rc.01', '../1.2.3', '1.2.3\n', '1.2.3;echo bad', '1.2.3+meta']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                release.version(value)

    def test_archive_is_reproducible_and_does_not_capture_host_ownership(self):
        import tarfile
        with tempfile.TemporaryDirectory() as scratch:
            a, b = Path(scratch) / 'a.tar.gz', Path(scratch) / 'b.tar.gz'
            entries = [('tool', b'example', 0o755), ('LICENSE', b'license', 0o644)]
            release.archive(a, entries, 1234)
            release.archive(b, list(reversed(entries)), 1234)
            self.assertEqual(a.read_bytes(), b.read_bytes())
            with tarfile.open(a) as tar:
                self.assertEqual(tar.getnames(), ['LICENSE', 'tool'])
                self.assertEqual(tar.getmember('tool').mode, 0o755)
                for member in tar:
                    self.assertEqual((member.uid, member.gid, member.mtime), (0, 0, 1234))


class AssetManifest(unittest.TestCase):
    def test_missing_or_corrupt_architecture_prevents_publication(self):
        import hashlib
        import sys
        sys.path.insert(0, str(Path(__file__).parents[1]))
        import verify
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            for arch in ['amd64', 'arm64']:
                for name in verify.assets('1.2.3', arch):
                    data = name.encode()
                    (root / name).write_bytes(data)
                    (root / (name + '.sha256')).write_text(f'{hashlib.sha256(data).hexdigest()}  {name}\n')
            self.assertEqual(len(verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64']).splitlines()), 8)
            name = verify.assets('1.2.3', 'arm64')[0]
            (root / name).write_bytes(b'corrupted')
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64'])
            (root / name).unlink()
            with self.assertRaisesRegex(ValueError, 'incomplete'):
                verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64'])
