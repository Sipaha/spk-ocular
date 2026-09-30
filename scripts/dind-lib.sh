# Shared by the dind-*.sh scripts: the isolated test Docker Engine
# ("ocular-dind", docker:29-dind in the user's docker). The user's own daemon
# runs the user's containers: the scripts touch exactly one container on it —
# the one whose id build/ocular-dind.id records — and everything else happens
# inside that dind daemon, after dind_verify has proved the endpoint is it.
# shellcheck shell=bash
unset HTTPS_PROXY HTTP_PROXY https_proxy http_proxy ALL_PROXY all_proxy
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIND_NAME=ocular-dind
DIND_LABEL=ocular.test=dind
DIND_IMAGE=docker:29-dind
DIND_PORT="${DIND_PORT:-23750}"
DIND_HOST="tcp://127.0.0.1:$DIND_PORT"
DIND_ID_FILE="$ROOT/build/ocular-dind.id"

die() { echo "dind: $*" >&2; exit 1; }

# dind_verify: the recorded container exists, is ours (name, label), runs,
# publishes exactly 127.0.0.1:$DIND_PORT, and the daemon answering on that
# port is inside it (/info Name = the container's hostname). Anything else —
# refuse: a mutation could otherwise land on some other daemon.
dind_verify() {
  [ -s "$DIND_ID_FILE" ] || die "no recorded container ($DIND_ID_FILE): run make dind-up"
  local id; id=$(cat "$DIND_ID_FILE")
  local facts
  facts=$(docker inspect -f '{{.Name}}|{{index .Config.Labels "ocular.test"}}|{{.State.Running}}|{{.Config.Hostname}}|{{range $p, $b := .HostConfig.PortBindings}}{{$p}}={{range $b}}{{.HostIp}}:{{.HostPort}},{{end}};{{end}}' "$id" 2>/dev/null) ||
    die "recorded container $id is gone: run make dind-up"
  local name label running hostname ports
  IFS='|' read -r name label running hostname ports <<<"$facts"
  [ "$name" = "/$DIND_NAME" ] || die "recorded container $id is named $name, not $DIND_NAME"
  [ "$label" = "${DIND_LABEL#*=}" ] || die "container $id has no label $DIND_LABEL"
  [ "$running" = true ] || die "container $id is not running: run make dind-up"
  [ "$ports" = "2375/tcp=127.0.0.1:$DIND_PORT,;" ] || die "container $id publishes '$ports', expected 2375/tcp on 127.0.0.1:$DIND_PORT only"
  local served
  served=$(curl -sf --noproxy '*' --max-time 5 "http://127.0.0.1:$DIND_PORT/info" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Name"])') ||
    die "no Docker Engine on 127.0.0.1:$DIND_PORT"
  [ "$served" = "$hostname" ] || die "127.0.0.1:$DIND_PORT is served by '$served', not by container $id ($hostname)"
}

# dind_wait: the daemon inside answers _ping (it starts in ~2 s).
dind_wait() {
  local i
  for i in $(seq 1 60); do
    curl -sf --noproxy '*' --max-time 2 "http://127.0.0.1:$DIND_PORT/_ping" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  die "the daemon in $DIND_NAME did not answer on 127.0.0.1:$DIND_PORT"
}

# dd: the docker CLI against the dind daemon (never the user's).
dd() { docker -H "$DIND_HOST" "$@"; }
