import hashlib
from pathlib import Path
import struct
import sys
import tempfile
import unittest
import zipfile

sys.path.insert(0, str(Path(__file__).parents[1]))
import portable
import verify


class PortableContracts(unittest.TestCase):
    def test_dmg_has_filesystem_headroom_and_does_not_follow_applications(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            self.assertEqual(portable.dmg_size(root), '128m')
            with (root / 'application').open('wb') as output:
                output.truncate(100 * 1024 * 1024 + 1)
            (root / 'Applications').symlink_to(root, target_is_directory=True)
            self.assertEqual(portable.dmg_size(root), '265m')

    def test_pe_architecture_and_gui_subsystem_are_read_from_binary(self):
        for arch, machine in [('amd64', 0x8664), ('arm64', 0xAA64)]:
            data = bytearray(256)
            data[:2] = b'MZ'
            struct.pack_into('<I', data, 60, 64)
            data[64:68] = b'PE\0\0'
            struct.pack_into('<H', data, 68, machine)
            struct.pack_into('<H', data, 64 + 24 + 68, 2)
            self.assertEqual(portable.binary_arch(data, 'windows', 2), arch)
            with self.assertRaisesRegex(ValueError, 'subsystem'):
                portable.binary_arch(data, 'windows', 3)
        with self.assertRaises(ValueError):
            portable.binary_arch(b'not a binary', 'windows')

    def test_macho_architecture(self):
        for arch, cpu in [('amd64', 0x01000007), ('arm64', 0x0100000C)]:
            self.assertEqual(portable.binary_arch(b'\xcf\xfa\xed\xfe' + struct.pack('<I', cpu), 'darwin'), arch)
        with self.assertRaises(ValueError):
            portable.binary_arch(b'MZxxxxxx', 'darwin')

    def test_zip_is_reproducible_and_retains_executable_permissions(self):
        with tempfile.TemporaryDirectory() as scratch:
            one, two = Path(scratch) / 'one.zip', Path(scratch) / 'two.zip'
            entries = [('tool.exe', b'program', 0o755), ('LICENSE', b'license', 0o644)]
            portable.zip_archive(one, entries, 1700000000)
            portable.zip_archive(two, list(reversed(entries)), 1700000000)
            self.assertEqual(one.read_bytes(), two.read_bytes())
            with zipfile.ZipFile(one) as archive:
                self.assertEqual(archive.namelist(), ['LICENSE', 'tool.exe'])
                self.assertEqual((archive.getinfo('tool.exe').external_attr >> 16) & 0o777, 0o755)

    def test_msi_version_is_bounded_without_losing_semver_in_artifact_name(self):
        self.assertEqual(portable.msi_version('1.2.3-dev.42'), '1.2.3')
        for value in ['256.0.0', '1.256.0', '1.0.65536']:
            with self.assertRaises(ValueError):
                portable.msi_version(value)

    def test_release_requires_all_six_platforms(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            for platform in verify.PLATFORMS:
                for arch in ['amd64', 'arm64']:
                    for name in verify.assets('1.2.3', arch, platform):
                        data = name.encode()
                        (root / name).write_bytes(data)
                        (root / (name + '.sha256')).write_text(f'{hashlib.sha256(data).hexdigest()}  {name}\n')
            name = 'NATIVE-VERIFICATION.zip'
            data = b'native evidence fixture'
            (root / name).write_bytes(data)
            (root / (name + '.sha256')).write_text(f'{hashlib.sha256(data).hexdigest()}  {name}\n')
            self.assertEqual(len(verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64'], verify.PLATFORMS).splitlines()), 21)
            for platform in verify.PLATFORMS:
                path = root / verify.assets('1.2.3', 'arm64', platform)[0]
                data = path.read_bytes()
                path.unlink()
                with self.assertRaisesRegex(ValueError, 'incomplete'):
                    verify.verify_checksums(root, '1.2.3', ['amd64', 'arm64'], verify.PLATFORMS)
                path.write_bytes(data)
