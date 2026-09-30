#!/usr/bin/env bash
# Seeds the Compose fixture into the isolated test daemon (idempotent):
# busybox loaded from the user's docker without network, the external
# network and volume, projects ocular-fixture and ocular-other, and a one-off
# container of ocular-fixture. Refuses unless dind_verify passes.
set -euo pipefail
. "$(dirname "$0")/dind-lib.sh"
dind_verify
F="$ROOT/scripts/dind-fixture"
if ! dd image inspect busybox:latest >/dev/null 2>&1; then
  docker image inspect busybox:latest >/dev/null 2>&1 || die "busybox:latest is not in your docker: docker pull busybox:latest"
  docker save busybox:latest | dd load >/dev/null
fi
dd network inspect ocular-ext-net >/dev/null 2>&1 || dd network create ocular-ext-net >/dev/null
dd volume inspect ocular-ext-vol >/dev/null 2>&1 || dd volume create ocular-ext-vol >/dev/null
dd compose -f "$F/compose.yaml" up -d --quiet-pull >/dev/null 2>&1 || dd compose -f "$F/compose.yaml" up -d
dd compose -f "$F/other.yaml" up -d >/dev/null 2>&1 || dd compose -f "$F/other.yaml" up -d
if [ -z "$(dd ps -aq --filter name='^ocular-fixture-oneoff$')" ]; then
  dd compose -f "$F/compose.yaml" run -d --name ocular-fixture-oneoff logger >/dev/null 2>&1
fi
echo "ocular-dind seeded: ocular-fixture, ocular-other"
