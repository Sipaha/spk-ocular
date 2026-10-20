package kubernetes

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
)

func nodeFilesReview(t *testing.T, s *session) core.ActionPlan {
	t.Helper()
	p, err := s.PrepareAction(t.Context(), nodeRef("worker", "node-uid"), "node-files", core.ActionParams{})
	require.NoError(t, err)
	require.Nil(t, p.Unavailable)
	return p
}
func TestNodeFilesReviewIsSeparateFromTerminalAndNeverCreates(t *testing.T) {
	s, c := nodeShellSession(t)
	p := nodeFilesReview(t, s)
	require.True(t, p.Action.Files)
	require.True(t, p.Action.NoAgents)
	require.True(t, p.Action.Single)
	require.False(t, p.Destructive)
	require.False(t, p.Action.Terminal)
	require.Contains(t, text(p), "filesystem")
	require.Contains(t, text(p), "root access")
	_, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(p))
	require.Error(t, err)
	shell := nodeShellPlan(t, s)
	r := nodeShellRun(shell)
	r.Action = "node-files"
	_, err = s.PrepareFilesAction(t.Context(), r)
	require.Error(t, err, "cannot repurpose a signed shell review")
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(p))
	require.NoError(t, err)
	require.Zero(t, countShellWrites(c, "create"))
	require.NoError(t, f.Close())
	require.Zero(t, countShellWrites(c, "delete"))
	_, err = s.PrepareFilesAction(t.Context(), nodeShellRun(p))
	require.Error(t, err, "one-shot review")
	command, err := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	require.NoError(t, err)
	_, err = command.Run(t.Context(), provider.Terminal{})
	require.Error(t, err)
	require.Zero(t, countShellWrites(c, "create"))
}
func TestNodeFilesReusesOnePinnedHelperAndClosesIt(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	s.conn.newExecutor = func(u *url.URL) (remotecommand.Executor, error) {
		assert.Equal(t, []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "-r", "-w", "--", "/bin/sh", "-c", "printf '%s' \"$1\"", "ocular-files", "/etc/file ; $(x)"}, u.Query()["command"])
		assert.Equal(t, "false", u.Query().Get("tty"))
		return &fakeExecutor{play: func(_ context.Context, o remotecommand.StreamOptions) error {
			_, err := io.Copy(o.Stdout, o.Stdin)
			return err
		}}, nil
	}
	s.Close() // A prepared Files session owns its original connection.
	for range 2 {
		command, err := f.PrepareCommand(t.Context(), []string{"sh", "-c", "printf '%s' \"$1\"", "ocular-files", "/etc/file ; $(x)"})
		require.NoError(t, err)
		var out bytes.Buffer
		status, err := command.Run(t.Context(), provider.Terminal{Raw: true, Stdin: strings.NewReader("node file"), Stdout: &out})
		require.NoError(t, err)
		assert.True(t, status.Known)
		assert.Equal(t, "node file", out.String())
		command.Close()
	}
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.Zero(t, countShellWrites(c, "delete"))
	require.NoError(t, f.Close())
	require.Zero(t, countShellWrites(c, "delete"))
	finishHelperIdle(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
	command, _ := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	_, err = command.Run(t.Context(), provider.Terminal{})
	require.Error(t, err)
	require.Equal(t, 1, countShellWrites(c, "create"))
}
func TestNodeFilesRejectsReplacedNodeBeforeAnotherCommand(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	command, _ := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	_, err = command.Run(t.Context(), provider.Terminal{Raw: true})
	require.NoError(t, err)
	n, err := c.Resource(nodeGVR).Get(t.Context(), "worker", metav1.GetOptions{})
	require.NoError(t, err)
	n.SetUID("replacement")
	_, err = c.Resource(nodeGVR).Update(t.Context(), n, metav1.UpdateOptions{})
	require.NoError(t, err)
	command, _ = f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	_, err = command.Run(t.Context(), provider.Terminal{Raw: true})
	require.Error(t, err)
	require.Equal(t, 1, countShellWrites(c, "create"))
}

func TestNodeFilesCloseCancelsRunningCommandAndCleansHelper(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	started, ended := make(chan struct{}), make(chan error, 1)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(ctx context.Context, _ remotecommand.StreamOptions) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}, nil
	}
	command, err := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	require.NoError(t, err)
	go func() { _, err := command.Run(t.Context(), provider.Terminal{Raw: true}); ended <- err }()
	<-started
	require.NoError(t, f.Close())
	require.Error(t, <-ended)
	require.Zero(t, countShellWrites(c, "delete"))
	finishHelperIdle(t, s)
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.Equal(t, 1, countShellWrites(c, "delete"))
}
