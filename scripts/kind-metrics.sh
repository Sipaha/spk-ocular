#!/usr/bin/env bash
# metrics-server for the disposable kind cluster (kubelet serving certs there
# are self-signed: --kubelet-insecure-tls is for this fixture only).
set -euo pipefail
KC="${1:?usage: kind-metrics.sh <kind kubeconfig>}"
CTX="kind-ocular-dev"
VERSION="v0.8.0"
k() { kubectl --kubeconfig "$KC" --context "$CTX" "$@"; }
[ "$(k config current-context)" = "$CTX" ] || { echo "kubeconfig $KC is not the kind test cluster" >&2; exit 1; }
k apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/$VERSION/components.yaml" >/dev/null
k -n kube-system patch deployment metrics-server --type=json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null 2>&1 || true
k -n kube-system rollout status deploy/metrics-server --timeout=180s
for _ in $(seq 1 60); do
  if k top nodes >/dev/null 2>&1; then echo "metrics available"; exit 0; fi
  sleep 3
done
echo "metrics-server did not produce samples" >&2
exit 1
