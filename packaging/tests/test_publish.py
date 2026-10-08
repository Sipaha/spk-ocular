import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1]))
import publish
import verify

spec = importlib.util.spec_from_file_location('validate_tag', Path(__file__).parents[1] / 'validate-tag.py')
validate_tag = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validate_tag)


class TagGate(unittest.TestCase):
    def test_tag_is_the_release_version_without_metadata_files(self):
        for value in ['1.2.3', '1.2.3-rc.1']:
            self.assertEqual(validate_tag.validate('v' + value), value)
        for bad in ['1.2.3', 'v1.2', 'v1.2.3-01', 'v../1.2.3']:
            with self.subTest(tag=bad), self.assertRaises(ValueError):
                validate_tag.validate(bad)


class ChangelogSource(unittest.TestCase):
    def test_exact_version_english_file_and_launcher_fallback(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            for value in ['1.2.3', '1.2.3-rc.1']:
                path = root / 'changelog' / value / 'en.md'
                path.parent.mkdir(parents=True)
                path.write_text(f'# {value}\n\n- Changes for this version.\n')
                (path.parent / 'ru.md').write_text('Localized release description')
                self.assertEqual(publish.release_notes(root, value), path)
            missing = publish.release_notes(root, '2.0.0')
            self.assertTrue(missing.is_relative_to(root / '.agents/tmp'))
            self.assertEqual(missing.read_text(), 'Release 2.0.0\n')


class PublicationGate(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        self.directory = self.root / 'dist/release'
        self.directory.mkdir(parents=True)
        self.version = '1.2.3-rc.1'
        for arch in ['amd64', 'arm64']:
            for name in [name for platform in verify.PLATFORMS for name in verify.assets(self.version, arch, platform)]:
                data = name.encode()
                (self.directory / name).write_bytes(data)
                (self.directory / (name + '.sha256')).write_text(f'{hashlib.sha256(data).hexdigest()}  {name}\n')
        name = 'NATIVE-VERIFICATION.zip'
        data = b'native evidence fixture'
        (self.directory / name).write_bytes(data)
        (self.directory / (name + '.sha256')).write_text(f'{hashlib.sha256(data).hexdigest()}  {name}\n')
        (self.directory / 'SHA256SUMS').write_text(verify.verify_checksums(self.directory, self.version, ['amd64', 'arm64'], verify.PLATFORMS))
        self.assets = [{'name': p.name, 'size': p.stat().st_size} for p in self.directory.iterdir()]
        self.commands = []
        self.existing = {'isDraft': True, 'assets': []}
        self.uploaded = {'isDraft': True, 'assets': self.assets}
        self.fail_upload = False
        self.notes = self.root / 'changelog' / self.version / 'en.md'
        self.notes.parent.mkdir(parents=True)
        self.notes.write_text('Notes for this exact version')

    def run_gh(self, command, **kwargs):
        self.commands.append(command)
        operation = command[2]
        if operation == 'view':
            return subprocess.CompletedProcess(command, int(self.existing is None), json.dumps(self.existing))
        if operation == 'upload' and self.fail_upload:
            raise subprocess.CalledProcessError(1, command)
        return subprocess.CompletedProcess(command, 0)

    def execute(self):
        with patch.object(publish.subprocess, 'run', side_effect=self.run_gh), patch.object(publish.subprocess, 'check_output', return_value=json.dumps(self.uploaded)):
            publish.publish('v' + self.version, self.root)

    def test_incomplete_local_assets_never_contact_github(self):
        (self.directory / verify.assets(self.version, 'arm64')[0]).unlink()
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            self.execute()
        self.assertEqual(self.commands, [])

    def test_published_release_is_untouched(self):
        self.existing['isDraft'] = False
        with self.assertRaisesRegex(ValueError, 'immutable'):
            self.execute()
        self.assertEqual([c[2] for c in self.commands], ['view'])

    def test_unexpected_draft_assets_require_review(self):
        self.existing['assets'] = [{'name': 'old-package.zip', 'size': 100}]
        with self.assertRaisesRegex(ValueError, 'unexpected assets'):
            self.execute()
        self.assertEqual([c[2] for c in self.commands], ['view'])

    def test_failed_upload_leaves_release_draft(self):
        self.fail_upload = True
        with self.assertRaises(subprocess.CalledProcessError):
            self.execute()
        self.assertNotIn('edit', [c[2] for c in self.commands])

    def test_incomplete_or_wrong_remote_assets_prevent_publication(self):
        for assets in [self.assets[:-1], self.assets + [self.assets[0]], [{'name': a['name'], 'size': 0} for a in self.assets]]:
            self.uploaded['assets'] = assets
            with self.subTest(assets=assets), self.assertRaisesRegex(ValueError, 'complete uploaded'):
                self.execute()
            self.assertNotIn('edit', [c[2] for c in self.commands])

    def test_new_and_existing_drafts_publish_only_after_complete_upload(self):
        for existing in [None, {'isDraft': True, 'assets': self.assets[:2]}]:
            self.commands.clear()
            self.existing = existing
            self.execute()
            operations = [c[2] for c in self.commands]
            self.assertEqual(operations, ['view', 'create', 'upload', 'edit'] if existing is None else ['view', 'upload', 'edit'])
            self.assertIn('--draft=false', self.commands[-1])
            self.assertEqual(self.commands[-1][-2:], ['--notes-file', str(self.notes)])
            self.assertIn('--prerelease=true', self.commands[-1])
            if existing is None:
                self.assertIn('--draft', self.commands[1])
                self.assertIn('--verify-tag', self.commands[1])
