#!/usr/bin/env bash
# Functional fixtures for the disposable kind cluster (make kind-up).
# Refuses to touch anything but the kind-ocular-dev context of the given kubeconfig.
set -euo pipefail
KC="${1:?usage: kind-seed.sh <kind kubeconfig>}"
CTX="kind-ocular-dev"
k() { kubectl --kubeconfig "$KC" --context "$CTX" "$@"; }
[ "$(k config current-context)" = "$CTX" ] || { echo "kubeconfig $KC is not the kind test cluster" >&2; exit 1; }

k apply -f - <<'YAML'
apiVersion: v1
kind: Namespace
metadata: {name: ocular-demo}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: ocular-demo}
spec:
  replicas: 3
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec:
      containers:
      - {name: nginx, image: "nginx:1.27-alpine", ports: [{containerPort: 80}]}
---
apiVersion: v1
kind: Service
metadata: {name: web, namespace: ocular-demo}
spec:
  selector: {app: web}
  ports: [{port: 80, targetPort: 80}]
---
apiVersion: v1
kind: Pod
metadata: {name: crashloop, namespace: ocular-demo, labels: {app: broken}}
spec:
  containers:
  - {name: app, image: "busybox:1.36", command: ["sh", "-c", "echo starting; sleep 2; exit 1"]}
---
apiVersion: v1
kind: Pod
metadata: {name: bad-image, namespace: ocular-demo}
spec:
  containers:
  - {name: app, image: "registry.invalid/ocular/does-not-exist:1"}
---
apiVersion: v1
kind: Pod
metadata: {name: unschedulable, namespace: ocular-demo}
spec:
  nodeSelector: {ocular.test/never: "true"}
  containers:
  - {name: app, image: "busybox:1.36", command: ["sleep", "3600"]}
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: db, namespace: ocular-demo}
spec:
  replicas: 2
  serviceName: db
  selector: {matchLabels: {app: db}}
  template:
    metadata: {labels: {app: db}}
    spec:
      containers:
      - {name: db, image: "busybox:1.36", command: ["sleep", "3600"]}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: web, namespace: ocular-demo}
spec:
  rules:
  - host: web.ocular.test
    http:
      paths:
      - {path: /, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: web-config, namespace: ocular-demo}
data: {nginx.conf: "server {}", LOG_LEVEL: debug}
---
apiVersion: v1
kind: Secret
metadata: {name: web-credentials, namespace: ocular-demo}
stringData: {username: admin, password: not-a-real-password}
YAML
k -n ocular-demo rollout status deploy/web --timeout=180s
k -n ocular-demo rollout status sts/db --timeout=180s
