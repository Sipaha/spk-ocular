//go:build !windows

package agentapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/compose"
)

// dindAgent is a liveAgent on the isolated test daemon (make test-dind):
// its Compose provider sees only DOCKER_HOST = OCULAR_DIND_HOST and an
// empty Docker config. Every docker CLI call is preceded by
// OCULAR_DIND_VERIFY (scripts/dind-verify.sh), which refuses unless the
// endpoint is served by the recorded ocular-dind container.
type dindAgent struct {
	*liveAgent
	host string
}

func newDindAgent(t *testing.T) *dindAgent {
	t.Helper()
	host := os.Getenv("OCULAR_DIND_HOST")
	if host == "" {
		t.Skip("OCULAR_DIND_HOST not set (make test-dind)")
	}
	a := &dindAgent{liveAgent: &liveAgent{t: t}, host: host}
	a.verify()
	cfg, home := t.TempDir(), t.TempDir()
	a.liveAgent = serveAgent(t, compose.NewWith(func(k string) string {
		switch k {
		case "DOCKER_HOST":
			return host
		case "DOCKER_CONFIG":
			return cfg
		}
		return ""
	}, home))
	return a
}

// verify proves the endpoint is still the test daemon.
func (a *dindAgent) verify() {
	a.t.Helper()
	script := os.Getenv("OCULAR_DIND_VERIFY")
	require.NotEmpty(a.t, script, "OCULAR_DIND_VERIFY not set: refusing to change a daemon that is not proven to be the test one")
	out, err := exec.Command("bash", script).CombinedOutput()
	require.NoError(a.t, err, string(out))
	require.Equal(a.t, a.host, strings.TrimSpace(string(out)), "the test daemon check")
}

func (a *dindAgent) docker(args ...string) string {
	a.t.Helper()
	a.verify()
	cmd := exec.Command("docker", append([]string{"-H", a.host}, args...)...)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "DOCKER_HOST", "DOCKER_CONTEXT":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	out, err := cmd.CombinedOutput()
	require.NoError(a.t, err, "docker %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// TestDindAgentAccessOverTheSocket: an agent on the real socket reads the
// project granted and not another (the seed's ocular-other), a container's
// logs, restarts a container by its grant and is refused a stop that is not
// granted; the target's identity names the daemon.
func TestDindAgentAccessOverTheSocket(t *testing.T) {
	a := newDindAgent(t)
	name := fmt.Sprintf("ocular-agent-%d", time.Now().UnixNano()%1_000_000)
	file := filepath.Join(t.TempDir(), "compose.yaml")
	require.NoError(t, os.WriteFile(file, []byte("name: "+name+`
services:
  web:
    image: busybox:latest
    command: ["sh", "-c", "while true; do echo hello-agent; sleep 1; done"]
`), 0o600))
	t.Cleanup(func() { a.docker("compose", "-f", file, "down", "-v", "--remove-orphans", "-t", "0") })
	a.docker("compose", "-f", file, "up", "-d")

	ctx := context.Background()
	target := TargetRef{Provider: "compose", Target: "context:default"}
	containers := []string{compose.KindContainers}
	require.NoError(t, a.svc.SaveAgentGrants(ctx, api.SaveAgentGrantsRequest{Provider: target.Provider, Target: target.Target, Grants: []agentgrant.Grant{
		one(name, agentgrant.VerbRead), one(name, agentgrant.VerbLogs), one(name, agentgrant.ActionVerb("restart"), containers...),
	}}))
	targets, err := a.st.AgentTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	id := a.docker("info", "-f", "{{.ID}}")
	assert.Contains(t, targets[0].Identity, "daemon:"+id, "the identity names the daemon")

	var access AccessView
	require.Equal(t, http.StatusOK, a.call("Access", AccessRequest{}, &access))
	require.Len(t, access.Targets, 1)
	assert.Equal(t, "active", access.Targets[0].State)

	// The granted project's containers; another project's are refused.
	var v ObjectsView
	require.Eventually(t, func() bool {
		var code int
		v, code = a.objects(ListObjectsRequest{TargetRef: target, Kind: compose.KindContainers, Scope: name})
		return code == http.StatusOK && len(v.Rows) == 1
	}, 30*time.Second, time.Second, "the project's container")
	all, code := a.objects(ListObjectsRequest{TargetRef: target, Kind: compose.KindContainers})
	require.Equal(t, http.StatusOK, code)
	for _, r := range all.Rows {
		assert.Equal(t, name, r.Ref.Scope, "only the project granted: %s", r.Ref.Name)
	}
	_, code = a.objects(ListObjectsRequest{TargetRef: target, Kind: compose.KindContainers, Scope: "ocular-other"})
	assert.Equal(t, http.StatusForbidden, code)
	ctr := v.Rows[0].Ref

	// Another project's container and service named under the granted
	// project's scope: not found there, neither details nor logs.
	otherID := a.docker("ps", "-q", "--no-trunc", "--filter", "label=com.docker.compose.project=ocular-other", "--filter", "label=com.docker.compose.service=idle")
	require.NotEmpty(t, otherID)
	for _, ref := range []core.Ref{
		{Provider: "compose", Target: target.Target, Scope: name, Kind: compose.KindContainers, Name: otherID},
		{Provider: "compose", Target: target.Target, Scope: name, Kind: compose.KindServices, Name: "ocular-other/idle"},
	} {
		assert.NotEqual(t, http.StatusOK, a.call("GetObject", GetObjectRequest{Ref: ref}, nil), "%s", ref.Kind)
		assert.NotEqual(t, http.StatusOK, a.call("GetLogs", GetLogsRequest{Ref: ref, TailLines: 5}, nil), "%s", ref.Kind)
	}

	// The container's logs.
	var tail api.Tail
	require.Eventually(t, func() bool {
		tail = api.Tail{}
		return a.call("GetLogs", GetLogsRequest{Ref: ctr, TailLines: 5}, &tail) == http.StatusOK && len(tail.Lines) > 0
	}, 30*time.Second, time.Second)
	assert.Contains(t, tail.Lines[len(tail.Lines)-1].Text, "hello-agent")
	a.checkLogInterval(ctr, tail)

	// Restart by the grant (a new start time); stop is not granted.
	cid := a.docker("compose", "-f", file, "ps", "-q", "web")
	started := a.docker("inspect", "-f", "{{.State.StartedAt}}", cid)
	var plan PrepareView
	require.Equal(t, http.StatusOK, a.call("PrepareAction", PrepareActionRequest{Ref: ctr, Action: "restart"}, &plan))
	var run RunView
	require.Equal(t, http.StatusOK, a.call("RunAction", RunRequest{PlanID: plan.PlanID}, &run))
	assert.Equal(t, "done", run.State, "%+v", run)
	assert.NotEqual(t, started, a.docker("inspect", "-f", "{{.State.StartedAt}}", cid), "restarted")
	assert.Equal(t, http.StatusForbidden, a.call("PrepareAction", PrepareActionRequest{Ref: ctr, Action: "stop"}, nil))
	assert.Equal(t, "true", a.docker("inspect", "-f", "{{.State.Running}}", cid))
}

func TestDindStandaloneDoesNotInheritProjectGrants(t *testing.T) {
	a := newDindAgent(t)
	name := fmt.Sprintf("ocular-agent-standalone-%d", time.Now().UnixNano())
	containerID := a.docker("run", "-d", "--name", name, "--label", "ocular.test=agent-standalone", "busybox:latest", "sleep", "3600")
	t.Cleanup(func() { a.docker("rm", "-f", containerID) })
	target := TargetRef{Provider: "compose", Target: "context:default"}
	all := agentgrant.Scope{Mode: agentgrant.ScopeAll}
	require.NoError(t, a.svc.SaveAgentGrants(t.Context(), api.SaveAgentGrantsRequest{Provider: target.Provider, Target: target.Target, Grants: []agentgrant.Grant{
		{Scope: all, Verb: agentgrant.VerbRead},
		{Scope: all, Verb: agentgrant.VerbLogs},
		{Scope: all, Verb: agentgrant.ActionVerb("restart"), Kinds: []string{compose.KindContainers}},
		{Scope: agentgrant.Scope{Mode: agentgrant.ScopeCluster}, Verb: agentgrant.VerbRead},
	}}))
	objects, code := a.objects(ListObjectsRequest{TargetRef: target, Kind: compose.KindContainers})
	require.Equal(t, http.StatusOK, code)
	for _, row := range objects.Rows {
		require.NotEqual(t, containerID, row.Ref.Name)
		require.NotEmpty(t, row.Ref.Scope)
	}
	ref := core.Ref{Provider: "compose", Target: target.Target, Kind: compose.KindContainers, Name: containerID, UID: containerID}
	require.Equal(t, http.StatusForbidden, a.call("GetObject", GetObjectRequest{Ref: ref}, nil))
	require.Equal(t, http.StatusForbidden, a.call("GetLogs", GetLogsRequest{Ref: ref, TailLines: 1}, nil))
	ref.Scope = "ocular-fixture"
	require.NotEqual(t, http.StatusOK, a.call("GetObject", GetObjectRequest{Ref: ref}, nil))
	require.NotEqual(t, http.StatusOK, a.call("GetLogs", GetLogsRequest{Ref: ref, TailLines: 1}, nil))
}
