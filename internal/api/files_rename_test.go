package api

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

func TestFilesRenamePinsSourceAndDoesNotOverwriteDestinations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	require.NoError(t, os.WriteFile(source, []byte("keep content"), 0600))
	req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p", UID: "uid"}, Instance: "pod/uid", Path: source, Command: "rename-info"}
	info, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Len(t, info.Version, 64)
	req.Command = "rename"
	req.NewName = "destination ; $(literal).txt"
	req.Expect = info.Version
	_, err = s.Files(ctx, req)
	require.True(t, IsCoded(err, CodeBadRequest))
	req.ConfigRev = info.ConfigRev
	for _, name := range []string{"collision", "directory", "link", "dangling"} {
		destination := filepath.Join(root, name)
		switch name {
		case "collision":
			require.NoError(t, os.WriteFile(destination, []byte("destination"), 0600))
		case "directory":
			require.NoError(t, os.Mkdir(destination, 0700))
		case "link":
			require.NoError(t, os.Symlink(source, destination))
		case "dangling":
			require.NoError(t, os.Symlink(filepath.Join(root, "absent"), destination))
		}
		req.NewName = name
		_, err = s.Files(ctx, req)
		require.ErrorContains(t, err, "already exists")
		content, err := os.ReadFile(source)
		require.NoError(t, err)
		require.Equal(t, "keep content", string(content))
	}
	old := source + ".original"
	require.NoError(t, os.Rename(source, old))
	require.NoError(t, os.WriteFile(source, []byte("replacement"), 0600))
	req.NewName = "replacement-must-stay"
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "Source changed")
	_, err = os.Stat(filepath.Join(root, req.NewName))
	require.True(t, os.IsNotExist(err))
	req.Command = "rename-info"
	info, err = s.Files(ctx, req)
	require.NoError(t, err)
	req.Command = "rename"
	req.Expect = info.Version
	req.NewName = "destination ; $(literal).txt"
	result, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, req.NewName), result.Path)
	content, err := os.ReadFile(result.Path)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(content))
	_, err = os.Stat(source)
	require.True(t, os.IsNotExist(err))
	for _, name := range []string{"/path", "..", ".", "", "bad\x00name"} {
		req.NewName = name
		_, err = s.Files(ctx, req)
		require.True(t, IsCoded(err, CodeBadRequest))
	}
}

func TestFilesRenameDirectoriesAndSymlinksPreservesTheirContents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	root := t.TempDir()
	dir := filepath.Join(root, "directory")
	require.NoError(t, os.Mkdir(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "child"), []byte("preserved"), 0600))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(dir, link))
	for _, source := range []string{link, dir} {
		req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p", UID: "uid"}, Instance: "pod/uid", Path: source, Command: "rename-info"}
		info, err := s.Files(ctx, req)
		require.NoError(t, err)
		req.Command = "rename"
		req.NewName = filepath.Base(source) + "-renamed"
		req.Expect = info.Version
		req.ConfigRev = info.ConfigRev
		result, err := s.Files(ctx, req)
		require.NoError(t, err)
		if source == link {
			target, err := os.Readlink(result.Path)
			require.NoError(t, err)
			require.Equal(t, dir, target)
			content, err := os.ReadFile(filepath.Join(dir, "child"))
			require.NoError(t, err)
			require.Equal(t, "preserved", string(content))
		} else {
			content, err := os.ReadFile(filepath.Join(result.Path, "child"))
			require.NoError(t, err)
			require.Equal(t, "preserved", string(content))
		}
	}
}
