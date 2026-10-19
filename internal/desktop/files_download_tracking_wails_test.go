//go:build wails

package desktop

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

type downloadSourceFixture func(context.Context, api.FilesRequest, io.Writer) error

func (f downloadSourceFixture) DownloadFiles(ctx context.Context, req api.FilesRequest, w io.Writer) error {
	return f(ctx, req, w)
}

func TestSelectedContainerFileAndFolderDownloadTrackOnlyCommittedDestinations(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "folder"}[folder], func(t *testing.T) {
			directory := t.TempDir()
			history := newDownloadHistory("")
			defer history.close()
			req := api.FilesRequest{Path: "/app/saved", ConfigRev: "pinned-revision", Ref: core.Ref{Provider: "synthetic", Target: "demo", Kind: "crates", Name: "source", UID: "pinned-uid"}, Instance: "chosen-instance", Channel: "chosen-container"}
			source := downloadSourceFixture(func(_ context.Context, received api.FilesRequest, w io.Writer) error {
				require.Equal(t, req, received)
				archive := tar.NewWriter(w)
				name := "./saved"
				data := []byte("private downloaded payload")
				if folder {
					if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
						return err
					}
					name += "/child.txt"
				}
				if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}); err != nil {
					return err
				}
				if _, err := archive.Write(data); err != nil {
					return err
				}
				return archive.Close()
			})
			manager := &FileDownloads{ctx: t.Context(), history: history, source: source}
			destination, err := manager.downloadSelected(req, directory)
			require.NoError(t, err)
			require.Equal(t, filepath.Join(directory, "saved"), destination)
			items := history.snapshot().Records
			require.Len(t, items, 1)
			require.Equal(t, "complete", items[0].Status)
			require.Equal(t, "files", items[0].Kind)
			require.Equal(t, destination, items[0].Path)
			require.Positive(t, items[0].Bytes)
			path := destination
			if folder {
				path = filepath.Join(path, "child.txt")
			}
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "private downloaded payload", string(data))
			history.clearCompleted()
			require.FileExists(t, path)
		})
	}
}

func TestFailedContainerDownloadHasNoCompletedPathAndLeavesNoPartialFile(t *testing.T) {
	directory := t.TempDir()
	history := newDownloadHistory("")
	defer history.close()
	manager := &FileDownloads{ctx: t.Context(), history: history, source: downloadSourceFixture(func(_ context.Context, _ api.FilesRequest, w io.Writer) error {
		_, _ = w.Write([]byte("broken archive"))
		return errors.New("fixture read failed")
	})}
	destination, err := manager.downloadSelected(api.FilesRequest{Path: "/app/saved"}, directory)
	require.Error(t, err)
	require.Empty(t, destination)
	items := history.snapshot().Records
	require.Len(t, items, 1)
	require.Equal(t, "failed", items[0].Status)
	require.Empty(t, items[0].Path)
	require.Equal(t, "source_unavailable", items[0].FailureReason)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestContainerDownloadExistingDestinationGetsUniqueNameAndPreservesFile(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "saved")
	require.NoError(t, os.WriteFile(file, []byte("preserve"), 0600))
	h := newDownloadHistory(t.TempDir())
	defer h.close()
	called := false
	manager := &FileDownloads{ctx: t.Context(), history: h, source: downloadSourceFixture(func(_ context.Context, _ api.FilesRequest, w io.Writer) error {
		called = true
		archive := tar.NewWriter(w)
		if err := archive.WriteHeader(&tar.Header{Name: "saved", Mode: 0600, Size: 3}); err != nil {
			return err
		}
		if _, err := archive.Write([]byte("new")); err != nil {
			return err
		}
		return archive.Close()
	})}
	destination, err := manager.downloadSelected(api.FilesRequest{Path: "/app/saved"}, directory)
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, filepath.Join(directory, "saved (1)"), destination)
	r := h.snapshot().Records[0]
	require.Equal(t, "complete", r.Status)
	require.Equal(t, destination, r.Path)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
}

func TestDownloadsPanelCancelsBlockedContainerTransfer(t *testing.T) {
	directory := t.TempDir()
	h := newDownloadHistory("")
	started := make(chan struct{})
	manager := &FileDownloads{ctx: t.Context(), history: h, source: downloadSourceFixture(func(ctx context.Context, _ api.FilesRequest, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})}
	result := make(chan error, 1)
	go func() {
		_, err := manager.downloadSelected(api.FilesRequest{Path: "/app/saved"}, directory)
		result <- err
	}()
	<-started
	(&Downloads{history: h}).Cancel(h.snapshot().Records[0].ID)
	require.ErrorIs(t, <-result, context.Canceled)
	require.Equal(t, "cancelled", h.snapshot().Records[0].Status)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
