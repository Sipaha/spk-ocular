//go:build !windows

package agentapi

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/compose"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real agent MCP endpoint, grant/store/session stack and production Docker adapter,
// with a private fake Engine on loopback. No user daemon or DIND is touched.
func TestAgentExecDockerOverSocket(t *testing.T) {
	fe := enginefake.New(t, enginefake.WithTCP(nil))
	c := engine.ContainerInspect{ID: "agent-exec-container", Name: "/test-app", State: engine.ContainerState{Running: true, Status: "running"}, Config: engine.ContainerConfig{Labels: map[string]string{compose.LabelProject: "p", compose.LabelService: "app"}}}
	fe.PutContainer(c)
	fe.SetExec(func(cmd []string, tty bool, rw io.ReadWriter, _ <-chan [2]uint16) int {
		assert.False(t, tty)
		assert.Equal(t, []string{"program", "literal; $(data)"}, cmd)
		input, err := io.ReadAll(rw)
		assert.NoError(t, err)
		assert.Equal(t, "text", string(input))
		_, err = rw.Write(enginefake.Frame(1, []byte("out\x00\xff")))
		assert.NoError(t, err)
		_, err = rw.Write(enginefake.Frame(2, []byte("err\n")))
		assert.NoError(t, err)
		return 9
	})
	home, cfg := t.TempDir(), t.TempDir()
	p := compose.NewWith(func(k string) string {
		switch k {
		case "DOCKER_HOST":
			return fe.Host()
		case "DOCKER_CONFIG":
			return cfg
		}
		return ""
	}, home)
	a := serveAgent(t, p)
	grant := one("p", agentgrant.VerbExec, compose.KindContainers)
	grant.NoConfirm = true
	require.NoError(t, a.svc.SaveAgentGrants(context.Background(), api.SaveAgentGrantsRequest{Provider: "compose", Target: "context:default", Grants: []agentgrant.Grant{grant}}))
	r := core.Ref{Provider: "compose", Target: "context:default", Kind: compose.KindContainers, Scope: "p", Name: c.ID, UID: c.ID}
	var info core.ExecInfo
	require.Equal(t, http.StatusOK, a.call("GetExecInfo", GetExecInfoRequest{Ref: r}, &info))
	require.Len(t, info.Instances, 1)
	req := PrepareExecRequest{Ref: r, Instance: info.Instances[0].ID, Command: []string{"program", "literal; $(data)"}, Stdin: "text", MaxSeconds: 1}
	var prep PrepareExecView
	require.Equal(t, http.StatusOK, a.call("PrepareExec", req, &prep))
	assert.Empty(t, fe.Execs())
	var run RunView
	require.Equal(t, http.StatusOK, a.call("RunExec", RunRequest{PlanID: prep.PlanID}, &run))
	require.NotNil(t, run.Exec)
	assert.Equal(t, []byte("out\x00\xff"), run.Exec.Stdout)
	assert.Equal(t, []byte("err\n"), run.Exec.Stderr)
	require.NotNil(t, run.Exec.ExitCode)
	assert.Equal(t, 9, *run.Exec.ExitCode)
	assert.True(t, run.Exec.Complete)
	require.Len(t, fe.Execs(), 1)
	assert.True(t, fe.Execs()[0].Started)
	// Fresh project membership prevents running an already prepared container
	// after it moves; empty project never inherits a project/All grant.
	require.Equal(t, http.StatusOK, a.call("PrepareExec", req, &prep))
	c.Config.Labels[compose.LabelProject] = "other"
	fe.PutContainer(c)
	assert.Equal(t, http.StatusNotFound, a.call("RunExec", RunRequest{PlanID: prep.PlanID}, nil))
	assert.Len(t, fe.Execs(), 1)
	r.Scope = ""
	assert.Equal(t, http.StatusForbidden, a.call("GetExecInfo", GetExecInfoRequest{Ref: r}, nil))
}
