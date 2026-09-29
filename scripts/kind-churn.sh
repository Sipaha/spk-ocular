#!/usr/bin/env bash
# A "noisy" cluster for soak runs, on the kind test cluster only:
#   namespace ocular-churn with a bounded set of objects that keep changing —
#   pods deleted and re-created under the same names (new UIDs), a deployment
#   scaled 1<->5, a crash-looping deployment (restarts, BackOff events), a pod
#   logging a line a second, ConfigMaps patched, Warning events re-created
#   under 50 reused names (a fixed number alive: growth would look like a leak).
# Usage: kind-churn.sh <kind kubeconfig> up|run|down|pause|break [seconds]
#   up      create the fixture
#   run     churn for [seconds] (default 3600); prints "ops <epoch> <n>" per minute
#   pause   pause the API server's node container for [seconds] (default 20):
#           delayed/unavailable API — a watch may or may not reconnect
#   break   restart the kube-apiserver container: every watch connection drops
#   down    delete the fixture
# PERIOD (s, default 2) sets the pace. Refuses anything but kind-ocular-dev.
set -euo pipefail
KC="${1:?usage: kind-churn.sh <kind kubeconfig> up|run|down|pause|break [seconds]}"
MODE="${2:?mode}"
ARG="${3:-}"
PERIOD="${PERIOD:-2}"
CTX="kind-ocular-dev"
NODE="ocular-dev-control-plane" # exactly this container, nothing else
NS="ocular-churn"
PODS=10 # churned pod names
EVENTS=50
k() { kubectl --kubeconfig "$KC" --context "$CTX" "$@"; }

[ "$(k config current-context)" = "$CTX" ] || { echo "kubeconfig $KC is not the kind test cluster" >&2; exit 1; }
server=$(k config view --minify -o jsonpath='{.clusters[0].cluster.server}')
case "$server" in https://127.0.0.1:* | https://localhost:*) ;; *) echo "not a local kind API server: $server" >&2; exit 1 ;; esac
[ "$(docker inspect -f '{{.Config.Image}}' "$NODE" 2>/dev/null)" != "" ] || { echo "no container $NODE" >&2; exit 1; }

unpause() { docker unpause "$NODE" >/dev/null 2>&1 || true; }

case "$MODE" in
up)
  k create namespace "$NS" --dry-run=client -o yaml | k apply -f - >/dev/null
  k -n "$NS" create deployment flap --image=nginx:1.27-alpine --replicas=1 --dry-run=client -o yaml | k apply -f - >/dev/null
  k -n "$NS" create deployment crash --image=nginx:1.27-alpine --dry-run=client -o yaml -- sh -c 'sleep 5; exit 1' | k apply -f - >/dev/null
  k -n "$NS" create deployment logger --image=nginx:1.27-alpine --dry-run=client -o yaml -- sh -c 'i=0; while true; do i=$((i+1)); echo "line $i"; sleep 1; done' | k apply -f - >/dev/null
  for i in $(seq 0 19); do k -n "$NS" create configmap "cm-$i" --from-literal=v=0 --dry-run=client -o yaml | k apply -f - >/dev/null; done
  echo "ocular-churn up"
  ;;
run)
  end=$(( $(date +%s) + ${ARG:-3600} ))
  i=0 ops=0 minute=$(( $(date +%s) / 60 ))
  while [ "$(date +%s)" -lt "$end" ]; do
    n=$(( i % PODS ))
    # Same name, new object.
    k -n "$NS" delete pod "churn-$n" --wait=false --ignore-not-found >/dev/null 2>&1 || true
    k -n "$NS" run "churn-$n" --image=nginx:1.27-alpine --restart=Never --labels=app=churn >/dev/null 2>&1 || true
    k -n "$NS" patch configmap "cm-$(( i % 20 ))" --type=merge -p "{\"data\":{\"v\":\"$i\"}}" >/dev/null 2>&1 || true
    if (( i % 10 == 0 )); then
      k -n "$NS" scale deployment flap --replicas=$(( (i / 10) % 2 == 0 ? 5 : 1 )) >/dev/null 2>&1 || true
    fi
    e=$(( i % EVENTS ))
    now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    k -n "$NS" delete event "churn-ev-$e" --ignore-not-found >/dev/null 2>&1 || true
    k -n "$NS" create -f - >/dev/null 2>&1 <<EOF || true
apiVersion: v1
kind: Event
metadata: {name: churn-ev-$e, namespace: $NS}
type: Warning
reason: ChurnWarning
message: "synthetic warning $i"
count: 1
firstTimestamp: "$now"
lastTimestamp: "$now"
involvedObject: {apiVersion: v1, kind: Pod, name: churn-$n, namespace: $NS}
source: {component: kind-churn}
EOF
    i=$((i + 1)) ops=$((ops + 1))
    m=$(( $(date +%s) / 60 ))
    if [ "$m" != "$minute" ]; then echo "ops $(date +%s) $ops"; ops=0 minute=$m; fi
    sleep "$PERIOD"
  done
  ;;
pause)
  trap unpause EXIT INT TERM
  docker pause "$NODE" >/dev/null
  sleep "${ARG:-20}"
  unpause
  ;;
break)
  docker exec "$NODE" sh -c 'crictl ps --name kube-apiserver -q | xargs -r crictl stop' >/dev/null
  # kubelet starts it again; wait until it answers.
  for _ in $(seq 1 120); do k get --raw /readyz >/dev/null 2>&1 && break; sleep 1; done
  ;;
down)
  unpause
  k delete namespace "$NS" --wait=false --ignore-not-found >/dev/null
  ;;
*)
  echo "unknown mode $MODE" >&2
  exit 2
  ;;
esac
