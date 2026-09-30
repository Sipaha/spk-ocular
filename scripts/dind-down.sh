#!/usr/bin/env bash
# Removes the test daemon: exactly the recorded container (checked to be
# ours by name and label), with its anonymous volume. Nothing by name alone.
set -euo pipefail
. "$(dirname "$0")/dind-lib.sh"
[ -s "$DIND_ID_FILE" ] || { echo "dind: nothing recorded"; exit 0; }
id=$(cat "$DIND_ID_FILE")
if facts=$(docker inspect -f '{{.Name}} {{index .Config.Labels "ocular.test"}}' "$id" 2>/dev/null); then
  [ "$facts" = "/$DIND_NAME ${DIND_LABEL#*=}" ] || die "recorded container $id is not ours ($facts): refusing"
  docker rm -f -v "$id" >/dev/null
fi
rm -f "$DIND_ID_FILE"
echo "ocular-dind removed"
