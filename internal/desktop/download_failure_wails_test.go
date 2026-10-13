//go:build wails

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeLogFailureRetainsItsReasonAfterCleanupWithoutCommittingPartialData(t *testing.T) {
	for _, scenario := range []string{"limit", "incomplete", "timeout", "untrusted", "cancelled", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDownloadHistory(t.TempDir())
			defer h.close()
			file := filepath.Join(t.TempDir(), "history.log")
			staged, err := newStagedLogExport(file, 3)
			require.NoError(t, err)
			id := h.begin(file, "logs")
			job := &nativeLogExport{staged: staged, downloadID: id, timer: time.NewTimer(time.Hour)}
			m := &LogWindows{history: h, exports: map[string]*nativeLogExport{"opaque": job}}
			switch scenario {
			case "limit":
				require.Error(t, m.AppendLogExport("opaque", "exceeds limit"))
				require.NoError(t, m.FailLogExportReason("opaque", "write"))
			case "incomplete":
				require.NoError(t, m.FailLogExportReason("opaque", "incomplete"))
			case "timeout":
				require.NoError(t, m.endLogExport("opaque", "failed", "timeout"))
			case "untrusted":
				require.NoError(t, m.FailLogExportReason("opaque", "credential=DO-NOT-RETAIN"))
			case "cancelled":
				require.NoError(t, m.CancelLogExport("opaque"))
			case "interrupted":
				m.closeExports()
			}
			r := h.snapshot().Records[0]
			if scenario == "cancelled" || scenario == "interrupted" {
				require.Equal(t, scenario, r.Status)
				require.Empty(t, r.FailureReason)
			} else {
				require.Equal(t, "failed", r.Status)
				reason := scenario
				if scenario == "untrusted" {
					reason = "unknown"
				}
				require.Equal(t, reason, r.FailureReason)
			}
			require.NoFileExists(t, file)
			require.Empty(t, r.Path)
			entries, err := os.ReadDir(filepath.Dir(file))
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}
func TestNativeOversizedChunkCannotCommitAnEmptyFileAfterFailure(t *testing.T) {
	h := newDownloadHistory("")
	defer h.close()
	file := filepath.Join(t.TempDir(), "history.log")
	staged, err := newStagedLogExport(file, 1<<30)
	require.NoError(t, err)
	id := h.begin(file, "logs")
	m := &LogWindows{history: h, exports: map[string]*nativeLogExport{"opaque": {staged: staged, downloadID: id, timer: time.NewTimer(time.Hour)}}}
	require.Error(t, m.AppendLogExport("opaque", strings.Repeat("x", (1<<20)+1)))
	_, err = m.CompleteLogExport("opaque")
	require.Error(t, err)
	require.Equal(t, "limit", h.snapshot().Records[0].FailureReason)
	require.NoFileExists(t, file)
}
