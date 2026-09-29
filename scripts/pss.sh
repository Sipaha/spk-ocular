#!/usr/bin/env bash
# Memory of a process and all its descendants (Linux), per process and total.
#   PRIVATE = Private_Dirty: memory only this process tree holds and the kernel
#             cannot drop without swapping (heaps, JS, DOM, render buffers) —
#             what running the app actually costs. This is the budgeted metric.
#             (Private_Clean — mostly our own binary's code pages — is left out:
#             the kernel reclaims it freely under pressure.)
#   PSS     = private + a proportional share of shared pages (WebKit/GTK/ICU
#             libraries). It shrinks as more apps use the same libraries, so it
#             is reported for reference only.
# Usage: scripts/pss.sh <pid>
set -euo pipefail
root="${1:?pid}"
pids="$root"
frontier="$root"
while [ -n "$frontier" ]; do
  next=""
  for p in $frontier; do
    kids=$(cat /proc/"$p"/task/*/children 2>/dev/null || true)
    next="$next $kids"
  done
  frontier=$(echo $next)
  pids="$pids $frontier"
done
printf "%10s %10s  %s\n" "PRIVATE" "PSS" "PID CMD"
total_priv=0
total_pss=0
for p in $pids; do
  # A process may exit mid-scan: treat its numbers as 0.
  read -r priv pss < <(LC_ALL=C awk '
    /^Private_Dirty:/         { priv += $2 }
    /^Pss:/                   { pss = $2 }
    END { print priv + 0, pss + 0 }' /proc/"$p"/smaps_rollup 2>/dev/null || echo "0 0")
  cmd=$(tr '\0' ' ' < /proc/"$p"/cmdline 2>/dev/null | cut -c1-80 || true)
  LC_ALL=C awk -v a="$priv" -v b="$pss" -v p="$p" -v c="$cmd" \
    'BEGIN { printf "%7.1f MB %7.1f MB  %s %s\n", a/1024, b/1024, p, c }'
  total_priv=$((total_priv + priv))
  total_pss=$((total_pss + pss))
done
LC_ALL=C awk -v a="$total_priv" -v b="$total_pss" \
  'BEGIN { printf "TOTAL PRIVATE: %.1f MB (budgeted)   TOTAL PSS: %.1f MB (reference)\n", a/1024, b/1024 }'
