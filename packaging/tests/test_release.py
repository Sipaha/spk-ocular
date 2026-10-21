import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('release', Path(__file__).parents[1] / 'release.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseInputs(unittest.TestCase):
    def test_archives_include_public_metadata_but_exclude_internal_documents(self):
        import tarfile
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            public = ['LICENSE', 'THIRD-PARTY-NOTICES.txt', 'README.md',
                      'AGENTS.md', 'changelog/1.9.0/en.md']
            for name in [*public, 'docs/backlog.md',
                         'docs/images/internal.png', 'internal-docs/strategy.md']:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(name)
            paths = release.document_paths(root)
            self.assertEqual({p.relative_to(root).as_posix() for p in paths}, set(public))
            output = root / 'distribution.tar.gz'
            release.archive(output, [(p.relative_to(root).as_posix(), p, 0o644)
                                     for p in paths], 1234)
            with tarfile.open(output) as archive:
                self.assertEqual(set(archive.getnames()), set(public))

    def test_versions_are_safe_filenames_and_valid_semver(self):
        for value in ['0.1.0', 'v1.2.3', '1.2.3-rc.1', '0.1.0-dev.42']:
            self.assertEqual(release.version(value), value.removeprefix('v'))
        for value in ['latest', 'v1.2', '1.02.3', '1.2.3-01', '1.2.3-rc.01', '../1.2.3', '1.2.3\n', '1.2.3;echo bad', '1.2.3+meta']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                release.version(value)

    def test_isolated_binary_directory_reaches_make_and_nfpm(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            binaries = root / 'private binaries'
            calls = []
            configs = []
            (root / 'source/packaging').mkdir(parents=True)
            (root / 'source/packaging/nfpm.yaml').write_text('contents:\n  - src: build/bin/spk-ocular-release\n')

            def checked(command, **kwargs):
                if command[:2] == ['go', 'env']:
                    return 'linux amd64'
                if command[0] == 'git':
                    return '1234' if '--format=%ct' in command else 'abc123'
                return 'spk-ocular 1.2.0'

            def run(command, **kwargs):
                calls.append((command, kwargs))
                if command[0] == 'nfpm':
                    configs.append(Path(command[3]).read_text())
                    Path(command[-1]).write_bytes(b'package')

            def archive(path, entries, epoch):
                path.write_bytes(b'archive')

            with patch.object(release, 'ROOT', root / 'source'), \
                    patch.object(release.subprocess, 'check_output', side_effect=checked), \
                    patch.object(release.subprocess, 'run', side_effect=run), \
                    patch.object(release, 'elf_arch', return_value='amd64') as elf, \
                    patch.object(release, 'verify_glibc_floor', return_value='2.34') as abi, \
                    patch.object(release, 'document_paths', return_value=[]), \
                    patch.object(release, 'archive', side_effect=archive):
                release.package('1.2.0', 'amd64', binaries)
            self.assertIn(f'BIN_DIR={binaries}', calls[0][0])
            abi.assert_called_once_with(binaries / 'spk-ocular-release')
            self.assertEqual([call.args[0] for call in elf.call_args_list],
                             [binaries / 'spk-ocular-release'])
            for command, kwargs in calls[1:]:
                self.assertEqual(command[0], 'nfpm')
            import json
            self.assertEqual(len(configs), 2)
            for config in configs:
                self.assertIn('src: ' + json.dumps(str(binaries / 'spk-ocular-release')), config)
            self.assertFalse(Path(calls[1][0][3]).exists())

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
            self.assertEqual(len(verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64']).splitlines()), 6)
            name = verify.assets('1.2.3', 'arm64')[0]
            (root / name).write_bytes(b'corrupted')
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64'])
            (root / name).unlink()
            with self.assertRaisesRegex(ValueError, 'incomplete'):
                verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64'])
