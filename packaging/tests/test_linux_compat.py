import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('linux_compat', Path(__file__).parents[1] / 'linux-compat.py')
compat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compat)


class LinuxCompatibilitySafety(unittest.TestCase):
    def test_failure_diagnostics_are_bounded_and_escape_ci_annotation_content(self):
        with tempfile.TemporaryDirectory() as temporary:
            work = Path(temporary) / 'linux-compatibility/amd64/work'
            work.mkdir(parents=True)
            (work / 'install.log').write_text('x' * 5000 + '\n100% complete\n::warning::fixture')
            output = io.StringIO()
            with patch.dict(compat.os.environ, OCULAR_SCRATCH_DIR=temporary, GITHUB_ACTIONS='true'), \
                    patch('sys.stdout', output), patch('sys.stderr', io.StringIO()):
                compat.failure_diagnostics('amd64', RuntimeError('installation failed'))
            annotation = output.getvalue()
            self.assertIn('installation failed', annotation)
            self.assertIn('100%25 complete%0A::warning::fixture', annotation)
            self.assertEqual(annotation.count('\n'), 1)
            self.assertLess(len(annotation), 4400)

    def test_wrong_native_architecture_is_rejected_before_installing_any_package(self):
        with patch.object(compat, 'native_arch', return_value='amd64'):
            with self.assertRaisesRegex(ValueError, 'must run natively'):
                compat.verify('arm64', '1.6.1', Path('not an installation source'))

    def test_existing_unowned_rootfs_is_refused_without_network_or_mutation(self):
        with tempfile.TemporaryDirectory() as temporary:
            scratch = Path(temporary)
            (scratch / 'rootfs').mkdir()
            with patch.object(compat.urllib.request, 'build_opener') as network:
                with self.assertRaisesRegex(ValueError, 'exact owned fixture identity'):
                    compat.prepare_rootfs(scratch, 'amd64')
                network.assert_not_called()

    def test_owned_rootfs_reuse_requires_exact_architecture_and_pinned_digest(self):
        with tempfile.TemporaryDirectory() as temporary:
            scratch = Path(temporary)
            (scratch / 'rootfs').mkdir()
            marker = dict(arch='amd64', url=compat.rootfs_url('amd64'), sha256=compat.ROOTFS_SHA256['amd64'])
            (scratch / 'rootfs-owner.json').write_text(json.dumps(marker))
            self.assertEqual(compat.prepare_rootfs(scratch, 'amd64'), scratch / 'rootfs')
            with self.assertRaisesRegex(ValueError, 'exact owned fixture identity'):
                compat.prepare_rootfs(scratch, 'arm64')

    def test_setup_root_does_not_require_global_subuid_configuration(self):
        with patch.object(compat.os, 'geteuid', return_value=0):
            args = compat.sandbox(Path('/owned-rootfs'), Path('/owned-work'), Path('/artifacts'), True)
        self.assertEqual(args[0], 'bwrap')
        self.assertIn('--cap-drop', args)
        self.assertNotIn('CAP_SYS_ADMIN', args)
        self.assertNotIn('--unshare-user', args)

    def test_runtime_always_uses_private_network_and_non_root_identity(self):
        with patch.object(compat.os, 'geteuid', return_value=1000):
            args = compat.sandbox(Path('/owned-rootfs'), Path('/owned-work'), Path('/artifacts'), False)
        self.assertIn('--unshare-net', args)
        self.assertIn('--unshare-user', args)
        self.assertEqual(args[args.index('--uid') + 1], '1000')
        self.assertNotIn('--cap-add', args)

    def test_root_runtime_mounts_before_dropping_identity_and_all_capabilities(self):
        with patch.object(compat.os, 'geteuid', return_value=0):
            args = compat.sandbox(Path('/owned-rootfs'), Path('/owned-work'), Path('/artifacts'), False)
        self.assertIn('--unshare-net', args)
        self.assertNotIn('--unshare-user', args)
        self.assertNotIn('CAP_SYS_ADMIN', args)
        self.assertEqual(args[args.index('--ro-bind') + 1], '/owned-rootfs')
        self.assertIn('--reuid=1000', args)
        self.assertIn('--clear-groups', args)
        self.assertIn('--bounding-set=-all', args)
        self.assertIn('--no-new-privs', args)
