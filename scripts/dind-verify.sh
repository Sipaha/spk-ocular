#!/usr/bin/env bash
# Exits 0 and prints the endpoint only when 127.0.0.1:$DIND_PORT is served by
# the recorded ocular-dind container (see dind-lib.sh). Tests call it before
# any mutation.
set -euo pipefail
. "$(dirname "$0")/dind-lib.sh"
dind_verify
echo "$DIND_HOST"
