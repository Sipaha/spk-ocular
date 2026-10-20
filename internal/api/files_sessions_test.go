package api

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type fileActionProvider struct {
	*openable
	prepared, commands, closed atomic.Int32
}

func (p *fileActionProvider) Open(ctx context.Context, id string) (provider.Session, error) {
	s, err := p.openable.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	return &fileActionSession{fakeSession: s.(*fakeSession), owner: p}, nil
}

type fileActionSession struct {
	*fakeSession
	owner *fileActionProvider
}

func (s *fileActionSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{{ID: "nodes", Title: "Nodes", Actions: []core.ActionDescriptor{{ID: "node-files", Title: "Files", Files: true, NoAgents: true, Single: true}}}}
}
func (s *fileActionSession) PrepareAction(_ context.Context, r core.Ref, _ string, p core.ActionParams) (core.ActionPlan, error) {
	return core.ActionPlan{Where: core.LiveTarget{Ref: r, ConfigHash: s.hash}, Params: p, Expect: "review"}, nil
}
func (s *fileActionSession) RunAction(context.Context, provider.ActionRun) (core.ActionResult, error) {
	panic("reviewed files must not use ordinary RunAction")
}
func (s *fileActionSession) PrepareFilesAction(context.Context, provider.ActionRun) (provider.FilesHandle, error) {
	s.owner.prepared.Add(1)
	return &fileActionHandle{session: s}, nil
}

type fileActionHandle struct{ session *fileActionSession }

func (h *fileActionHandle) Describe() core.LiveTarget {
	return core.LiveTarget{ConfigHash: h.session.hash}
}
func (h *fileActionHandle) PrepareCommand(_ context.Context, argv []string) (provider.ExecHandle, error) {
	h.session.owner.commands.Add(1)
	return &filesHandle{sess: &filesSession{h.session.fakeSession}, argv: argv}, nil
}
func (h *fileActionHandle) Close() error { h.session.owner.closed.Add(1); return nil }

func TestReviewedNodeFilesRoundTripDownloadAndAdmissionGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local POSIX fixture; protocol tested separately")
	}
	p := &fileActionProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, err := s.ListTargets(ctx)
	require.NoError(t, err)
	_, err = s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "worker", UID: "node-uid"}
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: ref, Action: "node-files"})
	require.NoError(t, err)
	_, err = s.RunAction(t.Context(), runFor(plan, nil))
	require.True(t, IsCoded(err, "forbidden"))
	require.Zero(t, p.prepared.Load())
	bad := runFor(plan, nil)
	bad.ConfigRev = "stale"
	_, err = s.RunAction(ctx, bad)
	require.True(t, IsCoded(err, CodeConflict))
	require.Zero(t, p.prepared.Load())
	result, err := s.RunAction(ctx, runFor(plan, nil))
	require.NoError(t, err)
	require.NotNil(t, result.Files)
	require.NotEmpty(t, result.Files.PreparedID)
	require.Zero(t, p.commands.Load())
	root := t.TempDir()
	path := filepath.Join(root, "node ; $(no-execution).txt")
	require.NoError(t, os.WriteFile(path, []byte("host text\n"), 0600))
	req := FilesRequest{PreparedID: result.Files.PreparedID, Ref: ref, Command: "read", Path: path}
	_, err = s.Files(t.Context(), req)
	require.True(t, IsCoded(err, "forbidden"))
	require.Zero(t, p.commands.Load())
	wrong := req
	wrong.Ref.UID = "replacement"
	_, err = s.Files(ctx, wrong)
	require.True(t, IsCoded(err, CodeGone))
	require.Zero(t, p.commands.Load())
	wrong = req
	wrong.ConfigRev = "stale"
	_, err = s.Files(ctx, wrong)
	require.True(t, IsCoded(err, CodeConflict))
	require.Zero(t, p.commands.Load())
	doc, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "host text\n", doc.Text)
	req.ConfigRev = doc.ConfigRev
	req.Command = "write"
	req.Expect = doc.Version
	req.Text = "updated host text\n"
	_, err = s.Files(ctx, req)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, req.Text, string(raw))
	req.Text = "must not overwrite"
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "changed externally")
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "updated host text\n", string(raw))
	var archive bytes.Buffer
	require.NoError(t, s.DownloadFiles(ctx, req, &archive))
	reader := tar.NewReader(&archive)
	header, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, "./"+filepath.Base(path), header.Name)
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, string(raw), string(content))
	req.Command = "create"
	req.Path = filepath.Join(root, "new-node-file")
	req.Text = ""
	req.Expect = ""
	created, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, fileVersion(nil), created.Version)
	st, err := os.Stat(req.Path)
	require.NoError(t, err)
	require.Zero(t, st.Size())
	req.Command = "close"
	_, err = s.Files(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.closed.Load())
	_, err = s.Files(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.closed.Load())
	req.Command = "read"
	_, err = s.Files(ctx, req)
	require.True(t, IsCoded(err, CodeGone))
	require.EqualValues(t, 1, p.prepared.Load())
}
