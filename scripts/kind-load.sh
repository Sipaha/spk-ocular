#!/usr/bin/env bash
# Scale fixture for measurements (separate from the functional seed):
#   namespace ocular-load with N_CM ConfigMaps (90% ~200 B, 10% ~60 KiB —
#   the big ones check that values stay out of list caches) and N_POD pods
#   with an unused schedulerName (stay Pending: no scheduling, no image pulls).
# Usage: kind-load.sh <kind kubeconfig> [up|down]
set -euo pipefail
KC="${1:?usage: kind-load.sh <kind kubeconfig> [up|down]}"
MODE="${2:-up}"
N_CM="${N_CM:-5000}"
N_POD="${N_POD:-3000}"
CTX="kind-ocular-dev"
k() { kubectl --kubeconfig "$KC" --context "$CTX" "$@"; }
[ "$(k config current-context)" = "$CTX" ] || { echo "kubeconfig $KC is not the kind test cluster" >&2; exit 1; }

if [ "$MODE" = down ]; then
  k delete namespace ocular-load --wait=false >/dev/null 2>&1 || true
  exit 0
fi
k create namespace ocular-load --dry-run=client -o yaml | k apply -f - >/dev/null
big=$(head -c 45000 /dev/urandom | base64 -w0)
gen_cm() {
  for i in $(seq "$1" "$2"); do
    if (( i % 10 == 0 )); then v="$big"; else v="value-$i"; fi
    printf -- '---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm-%05d, namespace: ocular-load, labels: {load: "1"}}\ndata: {key: "%s", other: "x"}\n' "$i" "$v"
  done
}
gen_pod() {
  for i in $(seq "$1" "$2"); do
    printf -- '---\napiVersion: v1\nkind: Pod\nmetadata: {name: pending-%05d, namespace: ocular-load, labels: {load: "1", app: pending}}\nspec:\n  schedulerName: ocular-nobody\n  containers: [{name: app, image: "busybox:1.36", command: ["sleep", "3600"]}]\n' "$i"
  done
}
batch=500
for ((s=1; s<=N_CM; s+=batch)); do gen_cm "$s" $((s+batch-1 < N_CM ? s+batch-1 : N_CM)) | k apply --server-side -f - >/dev/null; done
for ((s=1; s<=N_POD; s+=batch)); do gen_pod "$s" $((s+batch-1 < N_POD ? s+batch-1 : N_POD)) | k apply --server-side -f - >/dev/null; done
cm=$(k -n ocular-load get configmaps -l load=1 --no-headers | wc -l)
pods=$(k -n ocular-load get pods -l load=1 --no-headers | wc -l)
echo "ocular-load: $cm configmaps, $pods pods"
[ "$cm" -eq "$N_CM" ] && [ "$pods" -eq "$N_POD" ]
