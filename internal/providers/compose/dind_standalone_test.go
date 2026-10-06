package compose

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/stretchr/testify/require"
)

func TestDindStandaloneLifecycle(t *testing.T) {
	host := dindHost(t)
	dindVerify(t, host)
	s := dindSession(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	all, project := newSink(), newSink()
	stop, err := s.Watch(provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}, all)
	require.NoError(t, err)
	t.Cleanup(stop)
	stop, err = s.Watch(provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-fixture"}}, project)
	require.NoError(t, err)
	t.Cleanup(stop)
	all.waitFor(t, "initial inventory", ready)
	project.waitFor(t, "selected project", ready)
	name := fmt.Sprintf("ocular-standalone-%d", time.Now().UnixNano())
	containerID := strings.TrimSpace(dindDocker(t, host, "run", "-d", "--name", name, "--label", "ocular.test=standalone", "--stop-timeout", "1", "busybox:latest", "sh", "-c", "echo standalone-log; exec sleep 3600"))
	t.Cleanup(func() {
		if _, err := s.cl.InspectContainer(context.Background(), containerID); engine.IsNotFound(err) {
			return
		}
		dindDocker(t, host, "rm", "-f", containerID)
	})
	all.waitFor(t, "standalone create event", func(rows map[string]core.Row, _ provider.ViewStatus) bool { _, ok := rows[containerID]; return ok })
	project.mu.Lock()
	_, leaked := project.rows[containerID]
	project.mu.Unlock()
	require.False(t, leaked)
	ref := core.Ref{Provider: ProviderID, Target: s.target, Kind: KindContainers, Name: containerID, UID: containerID}
	resource, err := s.Get(ctx, ref)
	require.NoError(t, err)
	require.Empty(t, resource.Ref.Scope)
	require.Equal(t, name, resource.Ref.Title)
	logInfo, err := s.LogInfo(ctx, ref)
	require.NoError(t, err)
	require.NotEmpty(t, logInfo.Channels)
	sink := newLogSink()
	require.NoError(t, s.StreamLogs(ctx, ref, provider.LogQuery{TailLines: 20}, sink))
	require.Contains(t, strings.Join(sink.texts(name), "\n"), "standalone-log")
	metrics, err := s.Metrics(ctx, metricsQuery(KindContainers), []string{containerID})
	require.NoError(t, err)
	require.NotNil(t, metrics.Values[containerID].Memory)
	require.Positive(t, *metrics.Values[containerID].Memory)
	dindVerify(t, host)
	handle, err := s.PrepareExec(ctx, ref, provider.ExecRequest{Command: []string{"sh", "-c", "printf standalone-exec"}})
	require.NoError(t, err)
	defer handle.Close()
	var output syncBuf
	exit, err := handle.Run(ctx, provider.Terminal{Stdin: strings.NewReader(""), Stdout: &output, Sizes: newSizes(provider.TermSize{Rows: 24, Cols: 80})})
	require.NoError(t, err)
	require.True(t, exit.Known)
	require.Zero(t, exit.Code)
	require.Contains(t, output.String(), "standalone-exec")
	forged := ref
	forged.Scope = "ocular-fixture"
	_, err = s.Get(ctx, forged)
	require.Error(t, err)
	_, err = s.PrepareAction(ctx, forged, "stop", core.ActionParams{})
	require.Error(t, err)
	old, err := s.PrepareAction(ctx, ref, "stop", core.ActionParams{})
	require.NoError(t, err)
	dindDocker(t, host, "restart", "-t", "0", containerID)
	dindVerify(t, host)
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: ref, Action: "stop", Expect: old.Expect})
	require.True(t, isClass(err, provider.ClassConflict), "%v", err)
	for _, action := range []string{"stop", "start", "restart", "stop", "delete"} {
		plan, err := s.PrepareAction(ctx, ref, action, core.ActionParams{})
		require.NoError(t, err)
		require.Nil(t, plan.Unavailable)
		dindVerify(t, host)
		result, err := s.RunAction(ctx, provider.ActionRun{Ref: ref, Action: action, Expect: plan.Expect})
		require.NoError(t, err)
		require.Equal(t, core.OutcomeDone, result.Outcome)
	}
	all.waitFor(t, "standalone removal", func(rows map[string]core.Row, _ provider.ViewStatus) bool { _, ok := rows[containerID]; return !ok })
	t.Log("standalone discovery/events, scope isolation, details, logs, real exec, metrics, stale-review refusal and reviewed stop/start/restart/delete passed")
}
