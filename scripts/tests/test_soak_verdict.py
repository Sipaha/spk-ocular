"""Regression coverage for causal plateau detection and post-plateau growth."""
import csv
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("soak_verdict", ROOT / "scripts/soak-verdict.py")
SOAK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SOAK)


def series(minutes, value, step=10):
    return [(t, value(t)) for t in range(0, minutes * 60 + 1, step)]


class PlateauTests(unittest.TestCase):
    def evaluate(self, points, baseline=200):
        return SOAK.after_load(points, baseline)

    def test_flat_requires_confirmation_then_ten_minutes(self):
        short = self.evaluate(series(12, lambda t: 120))
        complete = self.evaluate(series(13, lambda t: 120))
        self.assertEqual(short["status"], "INCOMPLETE")
        self.assertEqual(complete["status"], "ACCEPTED")
        self.assertEqual(complete["control_start_seconds"], 180)
        self.assertEqual(complete["control_seconds"], 600)

    def test_continued_unloading_is_incomplete_not_pass_or_failure(self):
        self.assertEqual(self.evaluate(series(20, lambda t: 190 - t / 60))["status"], "INCOMPLETE")

    def test_slow_cumulative_decline_restarts_control(self):
        self.assertEqual(self.evaluate(series(20, lambda t: 190 - t / 300))["status"], "INCOMPLETE")

    def test_unloading_then_plateau_starts_control_later(self):
        result = self.evaluate(series(20, lambda t: max(120, 180 - t / 6)))
        self.assertEqual(result["status"], "ACCEPTED")
        self.assertGreater(result["control_start_seconds"], 360)
        self.assertGreaterEqual(result["control_seconds"], 600)

    def test_confirmation_is_not_counted_as_control_time(self):
        result = self.evaluate(series(12, lambda t: max(120, 180 - t / 6)))
        self.assertEqual(result["status"], "INCOMPLETE")

    def test_new_drop_invalidates_an_earlier_complete_window(self):
        result = self.evaluate(series(18, lambda t: 120 if t < 900 else 110))
        self.assertEqual(result["status"], "INCOMPLETE")
        self.assertTrue(result["control_start_seconds"] is None or result["control_start_seconds"] > 900)

    def test_new_drop_can_be_followed_by_a_new_full_control(self):
        result = self.evaluate(series(30, lambda t: 120 if t < 900 else 110))
        self.assertEqual(result["status"], "ACCEPTED")
        self.assertGreater(result["control_start_seconds"], 900)

    def test_zero_mean_short_noise_is_tolerated(self):
        result = self.evaluate(series(14, lambda t: 120 + (0.4 if t % 20 else -0.4)))
        self.assertEqual(result["status"], "ACCEPTED")

    def test_rebound_failure_cannot_be_hidden_by_later_plateau(self):
        points = series(20, lambda t: 120 if t < 60 else 131)
        self.assertEqual(self.evaluate(points)["status"], "NOT ACCEPTED")

    def test_net_growth_failure_cannot_be_hidden_by_restart(self):
        points = series(20, lambda t: 120 if t < 300 else 126)
        self.assertEqual(self.evaluate(points)["status"], "NOT ACCEPTED")

    def test_return_budget_still_applies_during_unloading(self):
        self.assertEqual(self.evaluate(series(20, lambda t: max(120, 301 - t)))["status"], "NOT ACCEPTED")

    def test_sampling_gap_invalidates_control(self):
        points = [p for p in series(18, lambda t: 120) if not 600 < p[0] < 720]
        self.assertEqual(self.evaluate(points)["status"], "INCOMPLETE")

    def test_minute_sampling_is_supported(self):
        self.assertEqual(self.evaluate(series(14, lambda t: 120, step=60))["status"], "ACCEPTED")

    def test_missing_baseline_cannot_certify_full_run(self):
        self.assertEqual(self.evaluate(series(14, lambda t: 120), baseline=None)["status"], "INCOMPLETE")

    def test_duplicate_or_reversed_times_are_invalid(self):
        for points in [[(0, 120), (0, 120)], [(10, 120), (0, 120)]]:
            with self.assertRaises(ValueError):
                self.evaluate(points)

    def test_mean_is_time_weighted_not_sample_weighted(self):
        self.assertAlmostEqual(SOAK.interval_mean([(0, 0), (10, 10), (60, 60)], 0, 60), 30)
        self.assertAlmostEqual(SOAK.interval_mean([(0, 0), (10, 10), (60, 60)], 5, 55), 30)
        self.assertIsNone(SOAK.interval_mean([(0, 0), (120, 120)], 0, 60))


class VerdictCLITests(unittest.TestCase):
    def evaluate(self, *, load_growth=0, minutes=14, phase="settling", warm=True):
        scratch = ROOT.parent / ".tmp"
        scratch.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="soak-plateau-", dir=scratch) as directory:
            path = Path(directory) / "samples.csv"
            with path.open("w", newline="") as file:
                writer = csv.writer(file)
                writer.writerow(["time", "phase", "private_mb", "swap_mb", "pids"])
                writer.writerow([0, "pods-ns", 200, 0, "fixture"])
                end_load = 1260 if warm else 60
                if warm:
                    writer.writerow([1200, "pods-ns", 200, 0, "fixture"])
                writer.writerow([end_load, "pods-ns", 200 + load_growth, 0, "fixture"])
                for time, value in series(minutes, lambda t: 120):
                    writer.writerow([end_load + 10 + time, phase, value, 0, "fixture"])
            return subprocess.run([sys.executable, str(ROOT / "scripts/soak-verdict.py"), str(path)],
                                  capture_output=True, text=True, check=True).stdout

    def test_settling_is_post_load_not_a_loaded_baseline(self):
        output = self.evaluate()
        self.assertIn("VERDICT ACCEPTED", output)
        self.assertNotIn("settling       base", output)

    def test_loaded_phase_budget_still_applies(self):
        self.assertIn("VERDICT NOT ACCEPTED", self.evaluate(load_growth=101))

    def test_ten_minutes_without_prior_confirmation_are_insufficient(self):
        self.assertIn("VERDICT INCOMPLETE", self.evaluate(minutes=10))

    def test_missing_warmup_is_incomplete_not_crash(self):
        self.assertIn("VERDICT INCOMPLETE", self.evaluate(warm=False))


if __name__ == "__main__":
    unittest.main()
