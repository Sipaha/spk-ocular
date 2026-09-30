#!/usr/bin/env bash
# Starts (or creates) the isolated test Docker Engine ocular-dind and records
# its container id in build/ocular-dind.id. A container named ocular-dind
# without the label ocular.test=dind is not ours: refused, never removed.
# Usage: dind-up.sh   (DIND_PORT, default 23750)
set -euo pipefail
. "$(dirname "$0")/dind-lib.sh"
mkdir -p "$ROOT/build"

id=""
if [ -s "$DIND_ID_FILE" ] && docker inspect "$(cat "$DIND_ID_FILE")" >/dev/null 2>&1; then
  id=$(cat "$DIND_ID_FILE")
elif existing=$(docker inspect -f '{{.Id}} {{index .Config.Labels "ocular.test"}}' "$DIND_NAME" 2>/dev/null); then
  read -r eid elabel <<<"$existing"
  [ "$elabel" = "${DIND_LABEL#*=}" ] || die "a container named $DIND_NAME exists without label $DIND_LABEL — not ours; remove or rename it yourself"
  id=$eid # ours (label), only the record was lost
else
  id=$(docker run -d --name "$DIND_NAME" --label "$DIND_LABEL" --privileged \
    -e DOCKER_TLS_CERTDIR= -p "127.0.0.1:$DIND_PORT:2375" "$DIND_IMAGE")
fi
echo "$id" > "$DIND_ID_FILE"
[ "$(docker inspect -f '{{.State.Running}}' "$id")" = true ] || docker start "$id" >/dev/null
dind_wait
dind_verify
echo "ocular-dind up: $DIND_HOST (container ${id:0:12})"
