package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFilesDeleteGuardsIdentityAndNeverFollowsSymlinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.Mkdir(target, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(target, "kept"), []byte("preserve"), 0600))
	for _, kind := range []string{"file", "directory", "link", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			source := filepath.Join(root, kind+" ; $(literal)")
			switch kind {
			case "file":
				require.NoError(t, os.WriteFile(source, []byte("owned"), 0600))
			case "directory":
				require.NoError(t, os.Mkdir(source, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(source, "child"), nil, 0600))
				require.NoError(t, os.Symlink(target, filepath.Join(source, "link")))
			case "link":
				require.NoError(t, os.Symlink(target, source))
			case "dangling":
				require.NoError(t, os.Symlink(filepath.Join(root, "absent"), source))
			}
			req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p", UID: "uid"}, Instance: "pod/uid", Path: source, Command: "delete-info"}
			info, err := s.Files(ctx, req)
			require.NoError(t, err)
			require.Len(t, info.Version, 64)
			req.Command = "delete"
			req.Expect = info.Version
			_, err = s.Files(context.Background(), req)
			require.True(t, IsCoded(err, "forbidden"))
			_, err = s.Files(ctx, req)
			require.True(t, IsCoded(err, CodeBadRequest))
			req.ConfigRev = "stale"
			_, err = s.Files(ctx, req)
			require.True(t, IsCoded(err, CodeConflict))
			req.ConfigRev = info.ConfigRev
			_, err = s.Files(ctx, req)
			require.NoError(t, err)
			_, err = os.Lstat(source)
			require.True(t, os.IsNotExist(err))
			content, err := os.ReadFile(filepath.Join(target, "kept"))
			require.NoError(t, err)
			require.Equal(t, "preserve", string(content))
			_, err = s.Files(ctx, req)
			require.ErrorContains(t, err, "no longer exists")
		})
	}
	source := filepath.Join(root, "replaced")
	require.NoError(t, os.WriteFile(source, []byte("old"), 0600))
	req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p", UID: "uid"}, Instance: "pod/uid", Path: source, Command: "delete-info"}
	info, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.NoError(t, os.Rename(source, source+"-old"))
	require.NoError(t, os.WriteFile(source, []byte("replacement"), 0600))
	req.Command = "delete"
	req.Expect = info.Version
	req.ConfigRev = info.ConfigRev
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "Source changed")
	content, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(content))
	for _, path := range []string{"/", "/any/.."} {
		req.Path = path
		_, err = s.Files(ctx, req)
		require.True(t, IsCoded(err, CodeBadRequest))
	}
}
