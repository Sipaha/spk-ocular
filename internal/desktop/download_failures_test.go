package desktop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/stretchr/testify/require"
)

func TestDownloadFailureClassifiesStructuredCausesWithoutReadingRawText(t *testing.T) {
	for _, test := range []struct {
		err              error
		fallback, reason string
	}{
		{fs.ErrPermission, "write", "permission"}, {syscall.ENOSPC, "write", "no_space"}, {syscall.EDQUOT, "write", "no_space"}, {fs.ErrExist, "write", "destination_exists"}, {fs.ErrNotExist, "write", "destination_unavailable"},
		{context.DeadlineExceeded, "source_unavailable", "timeout"}, {&net.OpError{Op: "read", Net: "tcp", Err: errors.New("private remote address")}, "source_unavailable", "connection"},
		{&api.CodedError{Code: "forbidden", Detail: "credential=DO-NOT-RETAIN"}, "source_unavailable", "permission"},
		{&api.CodedError{Code: "conflict", Detail: "private source"}, "source_unavailable", "source_unavailable"},
		{errors.New("disk full password=not-a-real-secret"), "unknown", "unknown"},
		{withDownloadFailure("unsafe_archive", errors.New("private archive path")), "write", "unsafe_archive"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			require.Equal(t, test.reason, downloadFailureReason(fmt.Errorf("wrapped: %w", test.err), test.fallback))
		})
	}
	require.Equal(t, "unknown", safeDownloadFailure("arbitrary secret text"))
}

func TestDownloadFailureReasonsPersistOnlySafeCodeAndFirstTerminalFailureWins(t *testing.T) {
	directory := t.TempDir()
	h := newDownloadHistory(directory)
	id := h.begin(filepath.Join(directory, "failed.log"), "logs")
	h.finish(id, "", "failed", "permission")
	h.finish(id, "", "failed", "write")
	h.finish(id, filepath.Join(directory, "not-complete"), "complete")
	require.Equal(t, "permission", h.snapshot().Records[0].FailureReason)
	require.Empty(t, h.snapshot().Records[0].Path)
	raw := h.begin(filepath.Join(directory, "unknown.log"), "logs")
	h.finish(raw, "", "failed", "credential=DO-NOT-RETAIN")
	h.close()
	h = newDownloadHistory(directory)
	defer h.close()
	require.Equal(t, "unknown", h.snapshot().Records[0].FailureReason)
	require.Equal(t, "permission", h.snapshot().Records[1].FailureReason)
	data, err := os.ReadFile(filepath.Join(directory, "downloads", "history.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "DO-NOT-RETAIN")
}

func TestDownloadOldAndUntrustedReasonMetadataUsesUnknownFallback(t *testing.T) {
	directory := t.TempDir()
	downloadDir := filepath.Join(directory, "downloads")
	require.NoError(t, os.Mkdir(downloadDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(downloadDir, "history.json"), []byte(`[{"id":"old","name":"old.log","kind":"logs","status":"failed","bytes":0,"startedAt":1},{"id":"untrusted","name":"other.log","kind":"logs","status":"failed","failureReason":"credential=DO-NOT-RETAIN","bytes":0,"startedAt":2}]`), 0600))
	h := newDownloadHistory(directory)
	defer h.close()
	require.False(t, h.snapshot().StorageError)
	for _, r := range h.snapshot().Records {
		require.Equal(t, "unknown", r.FailureReason)
	}
	data, err := os.ReadFile(filepath.Join(downloadDir, "history.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "DO-NOT-RETAIN")
}

func TestDownloadArchivePolicyAndFormatHaveDistinctSafeFailureReasons(t *testing.T) {
	_, err := copyContainerDownload(t.Context(), t.TempDir(), "folder", archiveStream(downloadArchive(t, downloadEntry{name: "../escape", kind: 0})))
	require.Error(t, err)
	require.Equal(t, "unsafe_archive", downloadFailureReason(err, "unknown"))
	_, err = copyContainerDownload(t.Context(), t.TempDir(), "folder", archiveStream([]byte("truncated archive")))
	require.Error(t, err)
	require.Equal(t, "invalid_archive", downloadFailureReason(err, "unknown"))
}
