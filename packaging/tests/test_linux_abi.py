import unittest
from unittest.mock import patch

from linux_abi import verify_glibc_floor


class LinuxABIFloor(unittest.TestCase):
    def test_both_baseline_and_older_symbol_requirements_are_accepted(self):
        for text, expected in [('Name: GLIBC_2.2.5 Name: GLIBC_2.34', '2.34'),
                               ('Name: GLIBC_2.35 Name: GLIBC_2.17', '2.35')]:
            with self.subTest(text=text), patch('linux_abi.subprocess.check_output', return_value=text):
                self.assertEqual(verify_glibc_floor('native'), expected)

    def test_future_symbol_requirement_cannot_hide_behind_lower_metadata(self):
        for requirement in ['GLIBC_2.38', 'GLIBC_2.35.0.1']:
            with self.subTest(requirement=requirement), patch('linux_abi.subprocess.check_output', return_value='GLIBC_2.34 ' + requirement):
                with self.assertRaisesRegex(ValueError, 'above package floor'):
                    verify_glibc_floor('native')

    def test_uninspected_or_static_executable_is_not_treated_as_native_evidence(self):
        with patch('linux_abi.subprocess.check_output', return_value='No version information found'):
            with self.assertRaisesRegex(ValueError, 'no inspected glibc'):
                verify_glibc_floor('wrong executable')
