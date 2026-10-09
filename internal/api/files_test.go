package api

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type filesOpenable struct{ *openable }

func (o *filesOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	sess, err := o.openable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	return &filesSession{sess.(*fakeSession)}, nil
}

type filesSession struct{ *fakeSession }

func (s *filesSession) ExecInfo(context.Context, core.Ref) (core.ExecInfo, error) {
	return core.ExecInfo{}, nil
}
func (s *filesSession) PrepareExec(_ context.Context, _ core.Ref, r provider.ExecRequest) (provider.ExecHandle, error) {
	return &filesHandle{sess: s, argv: r.Command}, nil
}

type filesHandle struct {
	sess *filesSession
	argv []string
}

func (h *filesHandle) Describe() core.LiveTarget           { return core.LiveTarget{ConfigHash: h.sess.hash} }
func (h *filesHandle) Again() (provider.ExecHandle, error) { return h, nil }
func (h *filesHandle) Close()                              {}
func (h *filesHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	if !t.Raw {
		return provider.ExitStatus{}, errors.New("file commands must not use a TTY")
	}
	cmd := exec.CommandContext(ctx, h.argv[0], h.argv[1:]...)
	cmd.Stdin = t.Stdin
	cmd.Stdout = t.Stdout
	cmd.Stderr = t.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return provider.ExitStatus{Known: true, Code: exit.ExitCode()}, nil
	}
	return provider.ExitStatus{Known: err == nil}, err
}

func TestContainerFilesRoundTripAndConflicts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local POSIX shell fixture; container transports have separate provider tests")
	}
	tools := t.TempDir()
	executables := map[string]string{}
	for _, name := range []string{"sh", "readlink", "head", "mktemp", "sha256sum", "wc", "tr", "cat", "rm"} {
		executable, err := exec.LookPath(name)
		require.NoError(t, err)
		executables[name] = executable
		require.NoError(t, os.Symlink(executable, filepath.Join(tools, name)))
	}
	t.Setenv("PATH", tools) // Deliberately no ls, find or stat in the container fixture.
	root := t.TempDir()
	name := "settings ' $(touch injected)\n.env"
	p := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(p, []byte("KEY=привет\r\n"), 0640))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".hidden"), 0750))
	require.NoError(t, os.Symlink(p, filepath.Join(root, "link")))
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p"}, Instance: "pod/uid", Path: root, Command: "list"}
	listing, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Len(t, listing.Entries, 3)
	require.Contains(t, listing.Entries, FileEntry{Name: ".hidden", Directory: true})
	require.Contains(t, listing.Entries, FileEntry{Name: "link", Symlink: true, Target: p})
	req.Command = "resolve"
	req.Path = filepath.Join(root, "link")
	resolved, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, p, resolved.Path)
	req.Command = "read"
	req.Path = p
	req.ConfigRev = listing.ConfigRev
	doc, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "KEY=привет\r\n", doc.Text)
	req.Command = "write"
	req.Expect = doc.Version
	req.Text = "KEY=изменено\r\n"
	saved, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, fileVersion([]byte(req.Text)), saved.Version)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, req.Text, string(b))
	stat, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0640), stat.Mode().Perm())
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "changed externally")
	req.Expect = saved.Version
	req.ConfigRev = "another-configuration"
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "connection changed")
	req.ConfigRev = listing.ConfigRev
	req.Expect = saved.Version
	req.Text = ""
	_, err = s.Files(ctx, req)
	require.NoError(t, err)
	b, err = os.ReadFile(p)
	require.NoError(t, err)
	require.Empty(t, b)
	leftovers, err := filepath.Glob(p + ".ocular.*")
	require.NoError(t, err)
	require.Empty(t, leftovers)
	_, err = s.Files(t.Context(), req)
	require.ErrorContains(t, err, "only to the application UI")
	req.Command = "read"
	require.NoError(t, os.WriteFile(p, []byte{0, 1, 2}, 0640))
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "binary")
	require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("x", fileLimit+1)), 0640))
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "limit")
	require.NoError(t, os.Remove(filepath.Join(tools, "head")))
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "required utility head is missing")
	require.NoError(t, os.Symlink(executables["head"], filepath.Join(tools, "head")))
	req.Path = "/dev/null"
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "regular file")
}

func TestContainerDownloadStreamsBinaryArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX container command fixture")
	}
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	root := t.TempDir()
	p := filepath.Join(root, "-binary ' $(touch injected)")
	content := bytes.Repeat([]byte{0, 255, 128, 13, 10}, 600000)
	require.NoError(t, os.WriteFile(p, content, 0600))
	req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p"}, Instance: "pod/uid", Path: root, Command: "list"}
	listing, err := s.Files(ctx, req)
	require.NoError(t, err)
	req.Path = p
	req.ConfigRev = listing.ConfigRev
	var b bytes.Buffer
	require.NoError(t, s.DownloadFiles(ctx, req, &b))
	tr := tar.NewReader(&b)
	h, err := tr.Next()
	require.NoError(t, err)
	require.Equal(t, "./"+filepath.Base(p), h.Name)
	actual, err := io.ReadAll(tr)
	require.NoError(t, err)
	require.Equal(t, content, actual)
	folder := filepath.Join(root, "folder")
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "empty"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(folder, ".hidden"), []byte("hidden\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "binary.dat"), content, 0600))
	req.Path = folder
	b.Reset()
	require.NoError(t, s.DownloadFiles(ctx, req, &b))
	tr = tar.NewReader(&b)
	entries := map[string][]byte{}
	for {
		h, e := tr.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		require.NoError(t, e)
		body, e := io.ReadAll(tr)
		require.NoError(t, e)
		entries[h.Name] = body
	}
	require.Equal(t, content, entries["./folder/binary.dat"])
	require.Equal(t, []byte("hidden\n"), entries["./folder/.hidden"])
	require.Contains(t, entries, "./folder/empty/")
	req.ConfigRev = "stale"
	require.ErrorContains(t, s.DownloadFiles(ctx, req, io.Discard), "connection changed")
	require.ErrorContains(t, s.DownloadFiles(t.Context(), req, io.Discard), "only to the application UI")
	req.ConfigRev = listing.ConfigRev
	req.Path = filepath.Join(root, "link")
	require.NoError(t, os.Symlink(p, req.Path))
	require.ErrorContains(t, s.DownloadFiles(ctx, req, io.Discard), "symbolic link")
}

func TestContainerFileLinkTargetsAndResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX container command fixture")
	}
	root := t.TempDir()
	name := "target '\n$(touch injected).txt"
	target := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(target, []byte("target"), 0600))
	require.NoError(t, os.Symlink(name, filepath.Join(root, "relative")))
	require.NoError(t, os.Symlink("relative", filepath.Join(root, "chain")))
	require.NoError(t, os.Symlink("missing", filepath.Join(root, "broken")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "folder"), 0700))
	require.NoError(t, os.Symlink("folder", filepath.Join(root, "directory-link")))
	s, _ := newService(t, &filesOpenable{newOpenable("a")})
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	req := FilesRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p"}, Instance: "pod/uid", Path: root, Command: "list"}
	listing, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Contains(t, listing.Entries, FileEntry{Name: "relative", Symlink: true, Target: name})
	require.Contains(t, listing.Entries, FileEntry{Name: "chain", Symlink: true, Target: "relative"})
	require.Contains(t, listing.Entries, FileEntry{Name: "broken", Symlink: true, Target: "missing"})
	require.Contains(t, listing.Entries, FileEntry{Name: "directory-link", Directory: true, Symlink: true, Target: "folder"})
	req.ConfigRev = listing.ConfigRev
	req.Command = "resolve"
	req.Path = filepath.Join(root, "chain")
	resolved, err := s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, target, resolved.Path)
	req.Path = filepath.Join(root, "broken")
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "does not exist or is inaccessible")
	req.Path = filepath.Join(root, "directory-link")
	resolved, err = s.Files(ctx, req)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "folder"), resolved.Path)
	req.ConfigRev = "stale"
	_, err = s.Files(ctx, req)
	require.ErrorContains(t, err, "connection changed")
	_, err = s.Files(t.Context(), req)
	require.ErrorContains(t, err, "only to the application UI")
	_, err = os.Stat(filepath.Join(root, "injected"))
	require.True(t, os.IsNotExist(err))
}
