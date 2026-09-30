#!/usr/bin/env bash
# Samples a running desktop app for a soak: every INTERVAL s for MINUTES,
# one CSV line — time, host MemAvailable, the app tree's Private_Dirty /
# PSS / swapped-out, the app's PIDs, the backend stats (--test-api) and the
# phase label read from PHASE_FILE (what the scenario is doing now).
# Refuses to start when the host is short of memory (MIN_AVAIL_MB, default
# 8192): swapped-out pages leave Private_Dirty and fake a flat curve.
# MODE=phase: one line at the end of each phase instead (when PHASE_FILE's
# label changes; the line carries the phase that ended) — samples at the
# same point of every cycle, so the phases' own peaks do not make noise.
# Usage: soak-sample.sh <app pid> <app data dir> <out.csv>
set -euo pipefail
PID="${1:?app pid}"
DATA="${2:?app data dir}"
OUT="${3:?out.csv}"
INTERVAL="${INTERVAL:-60}"
MINUTES="${MINUTES:-60}"
MIN_AVAIL_MB="${MIN_AVAIL_MB:-8192}"
PHASE_FILE="${PHASE_FILE:-}"
MODE="${MODE:-interval}"
here=$(dirname "$0")

avail_mb() { awk '/^MemAvailable:/ { print int($2 / 1024) }' /proc/meminfo; }
a=$(avail_mb)
if [ "$a" -lt "$MIN_AVAIL_MB" ]; then
  echo "host has $a MB available, fewer than $MIN_AVAIL_MB: the measurement would not be comparable" >&2
  exit 3
fi
info="$DATA/test-api.json"
[ -f "$info" ] || { echo "no $info: start the app with --test-api" >&2; exit 1; }
url=$(sed -E 's/.*"url":"([^"]+)".*/\1/' "$info")
token=$(sed -E 's/.*"token":"([^"]+)".*/\1/' "$info")
fields="views caches_active caches_idle watchers deadlines cache_lists cache_watch_starts cache_initial_syncs streams terminals forwards goroutines heap_inuse sessions"
echo "time,avail_mb,private_mb,pss_mb,swap_mb,pids,phase,${fields// /,}" > "$OUT"
end=$(( $(date +%s) + MINUTES * 60 ))
# sample <phase>: one CSV line.
sample() {
  kill -0 "$PID" 2>/dev/null || { echo "app $PID is gone" >&2; exit 4; }
  mem=$(bash "$here/pss.sh" "$PID")
  total=$(echo "$mem" | tail -1)
  priv=$(echo "$total" | sed -E 's/.*PRIVATE: ([0-9.]+) MB.*/\1/')
  pss=$(echo "$total" | sed -E 's/.*PSS: ([0-9.]+) MB.*/\1/')
  swap=$(echo "$total" | sed -E 's/.*SWAP: ([0-9.]+) MB.*/\1/')
  pids=$(echo "$mem" | awk 'NR > 1 && $5 ~ /^[0-9]+$/ { printf "%s%s", sep, $5; sep = " " }')
  stats=$(curl -fsS -H "Authorization: Bearer $token" "$url/api/_test/stats" || echo '{}')
  row=""
  for f in $fields; do
    v=$(echo "$stats" | sed -nE "s/.*\"$f\":([0-9]+).*/\1/p")
    row="$row,${v:-}"
  done
  echo "$(date +%s),$(avail_mb),$priv,$pss,$swap,$pids,$1$row" >> "$OUT"
}

current_phase() { [ -n "$PHASE_FILE" ] && cat "$PHASE_FILE" 2>/dev/null || true; }

if [ "$MODE" = phase ]; then
  [ -n "$PHASE_FILE" ] || { echo "MODE=phase needs PHASE_FILE" >&2; exit 1; }
  prev=$(current_phase)
  while [ "$(date +%s)" -le "$end" ]; do
    cur=$(current_phase)
    if [ -n "$cur" ] && [ "$cur" != "$prev" ]; then
      [ -n "$prev" ] && sample "$prev"
      prev=$cur
    fi
    sleep 0.5
  done
  exit 0
fi
while [ "$(date +%s)" -le "$end" ]; do
  sample "$(current_phase)"
  sleep "$INTERVAL"
done
