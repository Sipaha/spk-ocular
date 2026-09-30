#!/usr/bin/env python3
"""Soak verdict by the user's criterion of 2026-09-30:
1. short swings are fine;
2. in each same-named phase of the scenario Private_Dirty stays within
   +100 MB of that phase's level in the first cycle after warm-up (20 min);
3. after the load stops (phase done/idle) memory comes back (within the same
   budget of the quiet phases' level) and stands flat (>= 10 min of points,
   range <= 10 MB, last - first <= 5 MB).
Usage: scripts/soak-verdict.py samples.csv  (a soak-sample.sh CSV; phases "cN name",
after the scenario — "done"/"idle"/"after")
"""
import csv
import statistics
import sys

WARM_MIN = 20
BUDGET = 100.0
QUIET = {"problems-all", "problems-ns", "pods-ns", "pod-details", "pods-all", "other-target", "back"}

rows = list(csv.DictReader(open(sys.argv[1])))
t0 = int(rows[0]["time"])
pts = []
for r in rows:
    ph = r["phase"].split(" ", 1)[-1] if r["phase"][:1] == "c" and " " in r["phase"] else r["phase"]
    pts.append(((int(r["time"]) - t0) / 60, ph, float(r["private_mb"]), float(r["swap_mb"]), r["pids"]))

load = [p for p in pts if p[1] not in ("done", "idle", "after")]
last_load = max(p[0] for p in load)
after = [p for p in pts if p[1] in ("done", "idle", "after") and p[0] > last_load]
print(f"samples {len(pts)}, span {pts[-1][0]:.0f} min, load points {len(load)}, after-load points {len(after)}")
print("pids", {p[4] for p in pts}, "max swap", max(p[3] for p in pts))

ok = True
base = {}
worst = []
for ph in sorted({p[1] for p in load}):
    series = [(m, v) for m, _, v, _, _ in (p for p in load if p[1] == ph)]
    post = [(m, v) for m, v in series if m >= WARM_MIN]
    if not post:
        continue
    b = post[0][1]
    base[ph] = b
    top = max(post, key=lambda x: x[1])
    last = post[-1][1]
    ex = top[1] - b
    worst.append(ex)
    flag = "OK" if ex <= BUDGET else "OVER"
    ok &= ex <= BUDGET
    print(f"  {ph:14s} base {b:6.1f} (min {post[0][0]:4.0f})  max {top[1]:6.1f} (min {top[0]:4.0f}, +{ex:5.1f})  last {last:6.1f}  {flag}")
qb = statistics.median([base[p] for p in base if p in QUIET]) if any(p in QUIET for p in base) else None
print(f"phases within +{BUDGET:.0f} MB: {ok}; max excess +{max(worst):.1f} MB; quiet baseline {qb}")

if len(after) < 2 or after[-1][0] - after[0][0] < 10:
    print("after load: NOT MEASURED (need >= 10 min of points)", [(round(m), v) for m, _, v, _, _ in after])
    after_ok = None
else:
    vals = [v for _, _, v, _, _ in after]
    rng = max(vals) - min(vals)
    drift = vals[-1] - vals[0]
    back = max(vals) <= qb + BUDGET
    flat = rng <= 10 and drift <= 5
    after_ok = back and flat
    print(f"after load: {after[-1][0] - after[0][0]:.0f} min, {min(vals):.1f}..{max(vals):.1f} MB (range {rng:.1f}, first->last {drift:+.1f}); back within budget {back}; flat {flat}")
verdict = "ACCEPTED" if ok and after_ok else ("NOT ACCEPTED" if not ok or after_ok is False else "INCOMPLETE")
print("VERDICT", verdict)
