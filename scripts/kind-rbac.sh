#!/usr/bin/env bash
# Namespace-limited users for the disposable kind cluster. Writes
#   <outdir>/viewer.kubeconfig   get/list/watch pods in ocular-demo only (no logs, exec, port-forward)
#   <outdir>/nowatch.kubeconfig  get/list pods and read pods/log in ocular-demo, no watch
#   <outdir>/multiviewer.kubeconfig pods and pod metrics in ocular-demo + kube-system only
# with fresh short-lived ServiceAccount tokens and ocular-demo as default namespace.
set -euo pipefail
KC="${1:?usage: kind-rbac.sh <kind kubeconfig> <outdir>}"
OUT="${2:?usage: kind-rbac.sh <kind kubeconfig> <outdir>}"
CTX="kind-ocular-dev"
k() { kubectl --kubeconfig "$KC" --context "$CTX" "$@"; }
[ "$(k config current-context)" = "$CTX" ] || { echo "kubeconfig $KC is not the kind test cluster" >&2; exit 1; }
mkdir -p "$OUT"

k apply -f - <<'YAML'
apiVersion: v1
kind: ServiceAccount
metadata: {name: ocular-viewer, namespace: ocular-demo}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: ocular-nowatch, namespace: ocular-demo}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: ocular-multiviewer, namespace: ocular-demo}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: pods-view, namespace: ocular-demo}
rules:
- {apiGroups: [""], resources: [pods], verbs: [get, list, watch]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: pods-nowatch, namespace: ocular-demo}
rules:
- {apiGroups: [""], resources: [pods], verbs: [get, list]}
- {apiGroups: [""], resources: [pods/log], verbs: [get]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ocular-viewer, namespace: ocular-demo}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: pods-view}
subjects: [{kind: ServiceAccount, name: ocular-viewer, namespace: ocular-demo}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ocular-nowatch, namespace: ocular-demo}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: pods-nowatch}
subjects: [{kind: ServiceAccount, name: ocular-nowatch, namespace: ocular-demo}]
YAML

for ns in ocular-demo kube-system; do
  k apply -f - <<YAML
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: ocular-multi-view, namespace: $ns}
rules:
- {apiGroups: [""], resources: [pods], verbs: [get, list, watch]}
- {apiGroups: ["metrics.k8s.io"], resources: [pods], verbs: [get, list]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ocular-multiviewer, namespace: $ns}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: ocular-multi-view}
subjects: [{kind: ServiceAccount, name: ocular-multiviewer, namespace: ocular-demo}]
YAML
done

server=$(k config view --minify -o jsonpath='{.clusters[0].cluster.server}')
ca=$(k config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
for sa in viewer nowatch multiviewer; do
  token=$(k -n ocular-demo create token "ocular-$sa" --duration 2h)
  cat > "$OUT/$sa.kubeconfig" <<KCFG
apiVersion: v1
kind: Config
clusters: [{name: kind-ocular-dev, cluster: {server: "$server", certificate-authority-data: "$ca"}}]
users: [{name: ocular-$sa, user: {token: "$token"}}]
contexts: [{name: "ocular-$sa", context: {cluster: kind-ocular-dev, user: "ocular-$sa", namespace: ocular-demo}}]
current-context: "ocular-$sa"
KCFG
  chmod 600 "$OUT/$sa.kubeconfig"
done
# Sanity: the RBAC is what the tests assume.
kv() { kubectl --kubeconfig "$OUT/viewer.kubeconfig" "$@"; }
[ "$(kv auth can-i list pods -n ocular-demo)" = yes ]
[ "$(kv auth can-i list pods -A 2>/dev/null || true)" = no ]
[ "$(kv auth can-i list namespaces 2>/dev/null || true)" = no ]
[ "$(kubectl --kubeconfig "$OUT/nowatch.kubeconfig" auth can-i watch pods -n ocular-demo 2>/dev/null || true)" = no ]
[ "$(kv auth can-i get pods --subresource=log -n ocular-demo 2>/dev/null || true)" = no ]
[ "$(kv auth can-i create pods --subresource=exec -n ocular-demo 2>/dev/null || true)" = no ]
[ "$(kv auth can-i create pods --subresource=portforward -n ocular-demo 2>/dev/null || true)" = no ]
[ "$(kubectl --kubeconfig "$OUT/nowatch.kubeconfig" auth can-i get pods --subresource=log -n ocular-demo)" = yes ]
[ "$(kubectl --kubeconfig "$OUT/multiviewer.kubeconfig" auth can-i list pods -n kube-system)" = yes ]
[ "$(kubectl --kubeconfig "$OUT/multiviewer.kubeconfig" auth can-i list pods -A 2>/dev/null || true)" = no ]
echo "rbac kubeconfigs in $OUT"
