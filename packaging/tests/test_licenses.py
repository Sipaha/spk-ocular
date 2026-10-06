"""Exercise missing/proprietary input failures and preservation of nested notices."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1]))
import licenses
import release


class LicenseInputs(unittest.TestCase):
    def test_missing_license_does_not_silently_drop_a_component(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            (root / 'NOTICE').write_text('An attribution is not a license grant.\n')
            with self.assertRaisesRegex(ValueError, 'Missing upstream license'):
                licenses.component_files(root, {root})

    def test_preserves_upstream_notice_and_embedded_native_license(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            package = root / 'internal/loader'
            native = package / 'native'
            native.mkdir(parents=True)
            (root / 'LICENSE').write_text('upstream license\n')
            (root / 'NOTICE').write_text('upstream authors\n')
            (native / 'LICENSE.txt').write_text('native code license\n')
            (package / 'notice.go').write_text('package loader\n')
            files = licenses.component_files(root, {package})
            text = licenses.render_component('example v1.2.3', files)
            for notice in ('upstream license', 'upstream authors', 'native code license'):
                self.assertIn(notice, text)
            self.assertNotIn('package loader', text)
            self.assertEqual(files, licenses.component_files(root, {package}))

    def test_unknown_frontend_license_stops_collection(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            (root / 'scripts').mkdir()
            (root / 'scripts/collect-licenses.mjs').write_bytes((release.ROOT / 'web/scripts/collect-licenses.mjs').read_bytes())
            package = root / 'node_modules/restricted'
            package.mkdir(parents=True)
            (root / 'package.json').write_text(json.dumps({'dependencies': {'restricted': '1.0.0'}}))
            (package / 'package.json').write_text(json.dumps({'name': 'restricted', 'version': '1.0.0', 'license': 'Proprietary'}))
            (package / 'LICENSE').write_text('All rights reserved.\n')
            result = subprocess.run(['node', 'scripts/collect-licenses.mjs'], cwd=root, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Review new frontend license', result.stderr)

    def test_frontend_mit_metadata_without_license_text_is_rejected(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            (root / 'scripts').mkdir()
            (root / 'scripts/collect-licenses.mjs').write_bytes((release.ROOT / 'web/scripts/collect-licenses.mjs').read_bytes())
            package = root / 'node_modules/incomplete'
            package.mkdir(parents=True)
            (root / 'package.json').write_text(json.dumps({'dependencies': {'incomplete': '1.0.0'}}))
            (package / 'package.json').write_text(json.dumps({'name': 'incomplete', 'version': '1.0.0', 'license': 'MIT'}))
            (package / 'NOTICE').write_text('Copyright Example.\n')
            result = subprocess.run(['node', 'scripts/collect-licenses.mjs'], cwd=root, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Missing license text', result.stderr)

    def test_committed_notice_is_a_required_distribution_document(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            self.assertIn(root / 'THIRD-PARTY-NOTICES.txt', release.document_paths(root))

    def test_wails_license_fallback_rejects_a_different_module_version(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            (root / 'web').mkdir()
            (root / 'LICENSE').write_text('MIT license text\n')
            (root / 'web/license-inputs.json').write_text(json.dumps({'schema': 1, 'packages': [
                {'name': '@wailsio/runtime', 'version': '3.0.0-beta.26', 'license': 'MIT', 'files': []},
            ]}))
            graph = {('github.com/wailsapp/wails/v3', 'v3.0.0-beta.25'): {'root': root, 'dirs': set()}}
            with patch.object(licenses, 'ROOT', root), patch.object(licenses, 'package_graph', return_value=(graph, set())), patch.object(licenses.subprocess, 'check_output', side_effect=[str(root), 'go1.26.1']):
                with self.assertRaisesRegex(ValueError, 'versions differ'):
                    licenses.collect()

    def test_stale_notice_fails_check_without_overwriting_it(self):
        with tempfile.TemporaryDirectory() as scratch:
            output = Path(scratch) / 'THIRD-PARTY-NOTICES.txt'
            output.write_text('stale\n')
            with patch.object(licenses, 'OUTPUT', output), patch.object(licenses, 'collect', return_value=('current\n', 1, 1)), patch.object(sys, 'argv', ['licenses.py', '--check']):
                with self.assertRaisesRegex(ValueError, 'stale'):
                    licenses.main()
            self.assertEqual(output.read_text(), 'stale\n')
