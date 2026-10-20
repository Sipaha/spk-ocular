package desktop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDownloadHistoryRestoresMetadataAndNeverDeletesFiles(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "saved.log")
	require.NoError(t, os.WriteFile(file, []byte("file contents stay out of metadata"), 0600))
	h := newDownloadHistory(directory)
	id := h.begin(file, "logs")
	h.progress(id, 12)
	h.finish(id, file, "complete")
	active := h.begin(filepath.Join(directory, "pending"), "files")
	h.progress(active, 23)
	h.close()
	h = newDownloadHistory(directory)
	defer h.close()
	snapshot := h.snapshot()
	require.False(t, snapshot.StorageError)
	require.Len(t, snapshot.Records, 2)
	require.Equal(t, "interrupted", snapshot.Records[0].Status)
	require.Empty(t, snapshot.Records[0].Path)
	require.Equal(t, "complete", snapshot.Records[1].Status)
	require.Equal(t, file, snapshot.Records[1].Path)
	data, err := os.ReadFile(filepath.Join(directory, "downloads", "history.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "file contents")
	h.clearCompleted()
	require.Empty(t, h.snapshot().Records)
	data, err = os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "file contents stay out of metadata", string(data))
}

func TestDownloadHistoryOnlyOpensKnownCompletedItems(t *testing.T) {
	h := newDownloadHistory("")
	defer h.close()
	file := filepath.Join(t.TempDir(), "name with spaces;$(not-a-command).log")
	require.NoError(t, os.WriteFile(file, []byte("text"), 0600))
	id := h.begin(file, "logs")
	var launches int
	h.launch = func(_ context.Context, _ string, args []string) error {
		launches++
		require.Contains(t, args, file)
		return nil
	}
	require.Error(t, h.open(t.Context(), id, false))
	require.Error(t, h.open(t.Context(), file, false))
	require.Zero(t, launches)
	h.finish(id, file, "complete")
	require.NoError(t, h.open(t.Context(), id, false))
	require.Equal(t, 1, launches)
	var revealed []string
	h.reveal = func(_ context.Context, path string) error { revealed = append(revealed, path); return nil }
	require.NoError(t, h.open(t.Context(), id, true))
	require.Equal(t, []string{file}, revealed)
	require.NoError(t, os.Remove(file))
	require.Error(t, h.open(t.Context(), id, false))
	require.Equal(t, 1, launches)
	for _, test := range []struct {
		os   string
		name string
		args []string
	}{
		{"linux", "xdg-open", []string{file}},
		{"darwin", "open", []string{"--", file}},
		{"windows", "rundll32.exe", []string{"url.dll,FileProtocolHandler", file}},
	} {
		name, args := downloadCommand(test.os, file)
		require.Equal(t, test.name, name)
		require.Equal(t, test.args, args)
	}
}

func TestDownloadHistoryBoundsHistoryRetainsActiveAndSerializesConcurrentUpdates(t *testing.T) {
	h := newDownloadHistory("")
	defer h.close()
	active := h.begin("/pending", "files")
	for range downloadHistoryLimit + 10 {
		id := h.begin("/done", "logs")
		h.finish(id, "/done", "complete")
	}
	snapshot := h.snapshot()
	require.Len(t, snapshot.Records, downloadHistoryLimit)
	require.Equal(t, active, snapshot.Records[len(snapshot.Records)-1].ID)
	var workers sync.WaitGroup
	for i := range 20 {
		workers.Go(func() { h.progress(active, int64(i)); _ = h.snapshot() })
	}
	workers.Wait()
	h.clearCompleted()
	require.Len(t, h.snapshot().Records, 1)
	h.finish(active, "", "cancelled")
	h.finish(active, "/late", "complete")
	require.Equal(t, "cancelled", h.snapshot().Records[0].Status)
	require.Empty(t, h.snapshot().Records[0].Path)
}

func TestDownloadHistoryPreservesUnreadableAndAnotherProcessesHistory(t *testing.T) {
	directory := t.TempDir()
	h := newDownloadHistory(directory)
	id := h.begin("/done", "logs")
	h.finish(id, "/done", "complete")
	other := newDownloadHistory(directory)
	require.True(t, other.snapshot().StorageError)
	other.begin("/other", "logs")
	other.close()
	h.close()
	path := filepath.Join(directory, "downloads", "history.json")
	require.NoError(t, os.WriteFile(path, []byte("broken history"), 0600))
	h = newDownloadHistory(directory)
	require.True(t, h.snapshot().StorageError)
	h.begin("/new", "logs")
	h.clearCompleted()
	h.close()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "broken history", string(data))
}

func TestDownloadOpenProcessFixture(t *testing.T) {
	if os.Getenv("OCULAR_DOWNLOAD_OPEN_FIXTURE") != "1" {
		t.Skip("owned subprocess fixture")
	}
	path := os.Getenv("OCULAR_DOWNLOAD_OPEN_FIXTURE_PATH")
	if err := os.WriteFile(path+".started", []byte("ready"), 0600); err != nil {
		os.Exit(1)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".release"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(path+".stopped", []byte("done"), 0600); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestOpeningDownloadedItemDoesNotKillViewerOnApplicationCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "viewer")
	t.Setenv("OCULAR_DOWNLOAD_OPEN_FIXTURE", "1")
	t.Setenv("OCULAR_DOWNLOAD_OPEN_FIXTURE_PATH", path)
	t.Cleanup(func() { _ = os.WriteFile(path+".release", nil, 0600) })
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, launchDownloadPath(ctx, binary, []string{"-test.run=^TestDownloadOpenProcessFixture$"}))
	exists := func(suffix string) bool { _, err := os.Stat(path + suffix); return err == nil }
	require.Eventually(t, func() bool { return exists(".started") }, time.Second, 10*time.Millisecond)
	cancel()
	require.False(t, exists(".stopped"))
	require.NoError(t, os.WriteFile(path+".release", nil, 0600))
	require.Eventually(t, func() bool { return exists(".stopped") }, time.Second, 10*time.Millisecond)
}

func TestDownloadDismissPreservesFilesAndActiveTransfers(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "saved.log")
	require.NoError(t, os.WriteFile(file, []byte("keep me"), 0600))
	h := newDownloadHistory(directory)
	id := h.begin(file, "logs")
	require.Error(t, h.dismiss(id))
	require.Error(t, h.dismiss(file))
	h.finish(id, file, "complete")
	require.NoError(t, h.dismiss(id))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "keep me", string(data))
	h.close()
	h = newDownloadHistory(directory)
	defer h.close()
	require.Empty(t, h.snapshot().Records)
}

func TestDownloadDeletionRequiresCurrentOneShotConfirmation(t *testing.T) {
	h := newDownloadHistory(t.TempDir())
	defer h.close()
	file := filepath.Join(t.TempDir(), "file ; $(literal).log")
	require.NoError(t, os.WriteFile(file, []byte("owned fixture"), 0600))
	id := h.begin(file, "logs")
	_, err := h.prepareDelete(id)
	require.Error(t, err)
	h.finish(id, file, "complete")
	_, err = h.prepareDelete(file)
	require.Error(t, err)
	preview, err := h.prepareDelete(id)
	require.NoError(t, err)
	require.Equal(t, file, preview.Path)
	require.False(t, preview.Directory)
	require.Error(t, h.deleteFile(id, "wrong-token"))
	h.cancelDelete(id, preview.Token)
	require.Error(t, h.deleteFile(id, preview.Token))
	preview, err = h.prepareDelete(id)
	require.NoError(t, err)
	require.NoError(t, h.deleteFile(id, preview.Token))
	require.NoFileExists(t, file)
	require.Empty(t, h.snapshot().Records)
	require.Error(t, h.deleteFile(id, preview.Token))
}

func TestDownloadDeletionRejectsReplacedItemParentAndExpiredOrClearedPreview(t *testing.T) {
	for _, scenario := range []string{"item", "parent", "expired", "cleared", "dismissed", "modified"} {
		t.Run(scenario, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "parent")
			require.NoError(t, os.Mkdir(directory, 0700))
			file := filepath.Join(directory, "saved.log")
			require.NoError(t, os.WriteFile(file, []byte("original"), 0600))
			h := newDownloadHistory("")
			defer h.close()
			id := h.begin(file, "logs")
			h.finish(id, file, "complete")
			preview, err := h.prepareDelete(id)
			require.NoError(t, err)
			switch scenario {
			case "item":
				require.NoError(t, os.Rename(file, file+".original"))
				require.NoError(t, os.WriteFile(file, []byte("replacement"), 0600))
			case "parent":
				require.NoError(t, os.Rename(directory, directory+".original"))
				require.NoError(t, os.Mkdir(directory, 0700))
				require.NoError(t, os.WriteFile(file, []byte("replacement"), 0600))
			case "expired":
				plan := h.deletes[id]
				plan.expires = time.Now().Add(-time.Second)
				h.deletes[id] = plan
			case "cleared":
				h.clearCompleted()
			case "dismissed":
				require.NoError(t, h.dismiss(id))
			case "modified":
				require.NoError(t, os.WriteFile(file, []byte("changed data"), 0600))
			}
			require.Error(t, h.deleteFile(id, preview.Token))
			require.FileExists(t, file)
			if scenario != "cleared" && scenario != "dismissed" {
				require.Len(t, h.snapshot().Records, 1)
			}
		})
	}
}

func TestDownloadFolderDeletionDoesNotFollowSymlinks(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("preserve outside"), 0600))
	folder := filepath.Join(t.TempDir(), "downloaded")
	require.NoError(t, os.Mkdir(folder, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "owned.txt"), []byte("owned"), 0600))
	if err := os.Symlink(outside, filepath.Join(folder, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	h := newDownloadHistory("")
	defer h.close()
	id := h.begin(folder, "files")
	h.finish(id, folder, "complete")
	require.True(t, h.snapshot().Records[0].Directory)
	preview, err := h.prepareDelete(id)
	require.NoError(t, err)
	require.True(t, preview.Directory)
	require.NoError(t, h.deleteFile(id, preview.Token))
	require.NoDirExists(t, folder)
	data, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "preserve outside", string(data))
	link := filepath.Join(t.TempDir(), "not-a-download")
	require.NoError(t, os.Symlink(outside, link))
	id = h.begin(link, "files")
	h.finish(id, link, "complete")
	_, err = h.prepareDelete(id)
	require.Error(t, err)
	require.FileExists(t, outside)
}
