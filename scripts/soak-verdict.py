#!/usr/bin/env python3
"""Evaluate loaded growth, then unloading and a separately timed plateau.

Usage: scripts/soak-verdict.py samples.csv (soak-sample.sh CSV).
The owner clarified on 2026-10-15 that the ten-minute control window must
start after unloading ends, not simply accept a negative ten-minute drift.
"""
import csv
import statistics
import sys

WARM_MIN = 20
BUDGET = 100.0
MEAN_SECONDS = 60
CONFIRM_SECONDS = 60
CONTROL_SECONDS = 600
MAX_GAP_SECONDS = 60
MEAN_STEP_MB = 0.5
MEAN_DROP_MB = 1.0
QUIET = {"problems-all", "problems-ns", "pods-ns", "pod-details", "pods-all", "other-target", "back"}
AFTER = {"done", "idle", "after", "settling"}


def interval_mean(points, start, end):
    """Time-weighted mean with linear interpolation; never bridge a sampling gap."""
    if not points or points[0][0] > start or points[-1][0] < end:
        return None
    area = 0.0
    for (t0, v0), (t1, v1) in zip(points, points[1:]):
        left, right = max(start, t0), min(end, t1)
        if left >= right:
            continue
        if t1 <= t0 or t1 - t0 > MAX_GAP_SECONDS:
            return None
        a = v0 + (v1 - v0) * (left - t0) / (t1 - t0)
        b = v0 + (v1 - v0) * (right - t0) / (t1 - t0)
        area += (a + b) * (right - left) / 2
    return area / (end - start)


def upward_excursion(values):
    low, rebound = values[0], 0.0
    for value in values:
        rebound = max(rebound, value - low)
        low = min(low, value)
    return rebound


def after_load(points, quiet_baseline):
    """Seconds/Private_Dirty pairs, returning incomplete until a plateau is measured.

    Confirmation is causal: its one minute does not count toward the subsequent
    ten-minute control. Renewed unloading discards the earlier control window;
    growth failures remain sticky across settling and all candidate windows.
    """
    result = {"status": "INCOMPLETE", "control_start_seconds": None,
              "control_seconds": 0.0, "reason": "need confirmed plateau and ten subsequent minutes"}
    if not points:
        return result
    if any(b[0] <= a[0] for a, b in zip(points, points[1:])):
        raise ValueError("sample times must be strictly increasing")
    vals = [v for _, v in points]
    result["max_upward_excursion_MB"] = upward_excursion(vals)
    # Do not hide growing memory by continually restarting the plateau timer.
    if upward_excursion(vals) > 10:
        return {**result, "status": "NOT ACCEPTED", "reason": "post-load upward excursion exceeds 10 MB"}
    if quiet_baseline is not None and max(vals) > quiet_baseline + BUDGET:
        return {**result, "status": "NOT ACCEPTED", "reason": "post-load return budget exceeded"}
    candidate = None
    candidate_means = []
    control_index = None
    anchor_mean = None
    for i, (time, _) in enumerate(points):
        previous = interval_mean(points[:i + 1], time - 2 * MEAN_SECONDS, time - MEAN_SECONDS)
        recent = interval_mean(points[:i + 1], time - MEAN_SECONDS, time)
        gap = i > 0 and time - points[i - 1][0] > MAX_GAP_SECONDS
        if previous is None or recent is None or gap:
            candidate, candidate_means, control_index, anchor_mean = None, [], None, None
            continue
        delta = recent - previous
        unloading = delta < -MEAN_STEP_MB
        if control_index is not None:
            unloading |= recent < anchor_mean - MEAN_DROP_MB
            if unloading:
                candidate, candidate_means, control_index, anchor_mean = None, [], None, None
            else:
                control_values = [v for _, v in points[control_index:i + 1]]
                if control_values[-1] - control_values[0] > 5:
                    return {**result, "status": "NOT ACCEPTED", "reason": "control-window net growth exceeds 5 MB"}
                continue
        if abs(delta) > MEAN_STEP_MB:
            candidate, candidate_means = None, []
            continue
        if candidate is None:
            candidate = time
            candidate_means = []
        candidate_means.append(recent)
        if max(candidate_means) - min(candidate_means) > MEAN_DROP_MB:
            candidate, candidate_means = time, [recent]
        if time - candidate >= CONFIRM_SECONDS:
            control_index, anchor_mean = i, recent
    if control_index is not None:
        start = points[control_index][0]
        result.update(control_start_seconds=start, control_seconds=points[-1][0] - start)
        result["net_growth_MB"] = points[-1][1] - points[control_index][1]
        if result["control_seconds"] >= CONTROL_SECONDS:
            if quiet_baseline is None:
                result["reason"] = "plateau measured, but no post-warm-up quiet baseline"
            else:
                result.update(status="ACCEPTED", reason="confirmed plateau followed by ten minutes within growth budgets")
    return result


def main(path):
    with open(path) as file:
        rows = list(csv.DictReader(file))
    if not rows:
        raise ValueError("no samples")
    t0 = float(rows[0]["time"])
    pts = []
    for row in rows:
        phase = row["phase"]
        if phase.startswith("c") and " " in phase:
            phase = phase.split(" ", 1)[-1]
        pts.append((float(row["time"]) - t0, phase, float(row["private_mb"]), float(row["swap_mb"])))
    if any(b[0] <= a[0] for a, b in zip(pts, pts[1:])):
        raise ValueError("sample times must be strictly increasing")
    load = [p for p in pts if p[1] not in AFTER]
    last_load = max((p[0] for p in load), default=-1)
    after = [(t, v) for t, ph, v, _ in pts if ph in AFTER and t > last_load]
    print(f"samples {len(pts)}, span {pts[-1][0] / 60:.2f} min, load points {len(load)}, after-load points {len(after)}")
    print("max swap", max(p[3] for p in pts))
    ok, base, worst = True, {}, []
    for phase in sorted({p[1] for p in load}):
        post = [(t, v) for t, ph, v, _ in load if ph == phase and t >= WARM_MIN * 60]
        if not post:
            continue
        baseline = post[0][1]
        base[phase] = baseline
        excess = max(v for _, v in post) - baseline
        worst.append(excess)
        ok &= excess <= BUDGET
        print(f"  {phase:14s} base {baseline:.3f} MB; max excess +{excess:.3f} MB; within budget {excess <= BUDGET}")
    quiet = [base[p] for p in base if p in QUIET]
    qb = statistics.median(quiet) if quiet else None
    print(f"phases within +{BUDGET:.0f} MB: {ok}; quiet baseline {qb}")
    assessment = after_load(after, qb)
    print("after load:", assessment)
    verdict = "NOT ACCEPTED" if not ok else assessment["status"]
    print("VERDICT", verdict)


if __name__ == "__main__":
    main(sys.argv[1])
