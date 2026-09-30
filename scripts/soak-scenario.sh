#!/usr/bin/env bash
# Drives the desktop app under Xvfb in a loop for a soak: the palette
# (Ctrl+K) switches views, namespaces and targets; the first row is opened
# by a click; logs and a terminal are opened from the table (↓ then L / S)
# and their dock tab closed again. Writes what it is doing to PHASE_FILE
# (read by soak-sample.sh). Test tooling: coordinates are those of the
# 1280×820 window at (60,40) that Xvfb without a window manager gives.
# Usage: DISPLAY=:77 soak-scenario.sh <minutes> <phase file>
# Env: NS (default ocular-churn), TARGET (default kind-ocular-dev),
#      ALT_TARGET (a second context title; empty: no target switching),
#      DWELL (s per step, default 20), ROW ("X Y", the first table row),
#      LOG_CLOSE / TERM_CLOSE ("X Y", the close button of the only dock tab —
#      a log tab of deployment logger, a terminal in its pod).
set -euo pipefail
MINUTES="${1:?minutes}"
PHASE="${2:?phase file}"
NS="${NS:-ocular-churn}"
TARGET="${TARGET:-kind-ocular-dev}"
ALT_TARGET="${ALT_TARGET:-}"
DWELL="${DWELL:-20}"
read -r ROW_X ROW_Y <<<"${ROW:-620 133}"
read -r LOG_X LOG_Y <<<"${LOG_CLOSE:-436 535}"
read -r TERM_X TERM_Y <<<"${TERM_CLOSE:-520 535}"
here=$(dirname "$0")
x() { python3 "$here/xinput.py" "$@"; }
phase() { echo "$1" > "$PHASE"; }
# The palette: open, type a command, choose it.
cmd() { x key ctrl+k sleep 0.4 type "$1" sleep 0.6 key Return sleep 1; }
dwell() { sleep "${1:-$DWELL}"; }

cmd ":ctx $TARGET"
end=$(( $(date +%s) + MINUTES * 60 ))
cycle=0
while [ "$(date +%s)" -le "$end" ]; do
  cycle=$((cycle + 1))
  phase "c$cycle problems-all"; cmd ":problems"; cmd ":ns *"; dwell
  phase "c$cycle problems-ns"; cmd ":ns $NS"; dwell
  phase "c$cycle pods-ns"; cmd ":pods"; dwell
  phase "c$cycle pod-details"; x click "$ROW_X" "$ROW_Y"; dwell; x key Escape sleep 0.5
  phase "c$cycle pods-all"; cmd ":ns *"; dwell
  phase "c$cycle kinds"
  for k in deploy sts ds svc ing cm secret node ev; do cmd ":$k"; sleep 4; done
  cmd ":ns $NS"
  for k in deploy svc cm ev; do cmd ":$k"; sleep 4; done
  phase "c$cycle logs-follow"; cmd ":deploy logger"; x key Down sleep 0.5 key l; dwell 40
  x click "$LOG_X" "$LOG_Y" sleep 1
  phase "c$cycle terminal"; cmd ":pods logger"; x key Down sleep 0.5 key s; sleep 6
  x type "ls /" key Return; dwell
  x click "$TERM_X" "$TERM_Y" sleep 2
  if [ -n "$ALT_TARGET" ]; then
    phase "c$cycle other-target"; cmd ":ctx $ALT_TARGET"; cmd ":pods"; dwell
    phase "c$cycle back"; cmd ":ctx $TARGET"; dwell 5
  fi
done
phase "done"
