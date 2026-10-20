package api

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

func TestFilesCreatesEmptyFileWithoutOverwritingExistingEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	root := t.TempDir()
	ref := core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p", UID: "uid"}
	req := FilesRequest{Ref: ref, Instance: "pod/uid", Path: root, Command: "list"}
	list, err := s.Files(ctx, req)
	require.NoError(t, err)
	req.Command = "create"
	req.Path = filepath.Join(root, "new file ; $(touch injected).txt")
	_, err = s.Files(context.Background(), req)
	require.True(t, IsCoded(err, "forbidden"))
	_, err = s.Files(ctx, req)
	require.True(t, IsCoded(err, CodeBadRequest), "creation requires captured connection revision")
	req.ConfigRev = "stale"
	_, err = s.Files(ctx, req)
	require.True(t, IsCoded(err, CodeConflict))
	req.ConfigRev = list.ConfigRev
	doc, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Empty(t, doc.Text)
	require.Equal(t, fileVersion(nil), doc.Version)
	stat, err := os.Stat(req.Path)
	require.NoError(t, err)
	require.Zero(t, stat.Size())
	require.True(t, stat.Mode().IsRegular())
	require.NoError(t, os.WriteFile(req.Path, []byte("preserve"), 0600))
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "already exists")
	content, err := os.ReadFile(req.Path)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(content))
	target := req.Path
	for _, name := range []string{"link", "dangling", "directory"} {
		req.Path = filepath.Join(root, name)
		switch name {
		case "link":
			require.NoError(t, os.Symlink(target, req.Path))
		case "dangling":
			require.NoError(t, os.Symlink(filepath.Join(root, "absent-target"), req.Path))
		case "directory":
			require.NoError(t, os.Mkdir(req.Path, 0700))
		}
		_, err = s.Files(ctx, req)
		require.ErrorContains(t, err, "already exists")
	}
	_, err = os.Stat(filepath.Join(root, "absent-target"))
	require.True(t, os.IsNotExist(err))
	req.Path = filepath.Join(root, "bad-content")
	req.Text = "nonempty"
	_, err = s.Files(ctx, req)
	require.True(t, IsCoded(err, CodeBadRequest))
	_, err = os.Stat(req.Path)
	require.True(t, os.IsNotExist(err))
}
