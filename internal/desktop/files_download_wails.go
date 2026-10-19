//go:build wails

package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// FileDownloads owns the native directory grant; renderer-provided local paths
// are never accepted. The service only reads the pinned remote container.
type FileDownloads struct {
	app     *application.App
	window  *application.WebviewWindow
	ctx     context.Context
	history *downloadHistory
	source  interface {
		DownloadFiles(context.Context, api.FilesRequest, io.Writer) error
	}
}

func (m *FileDownloads) Download(req api.FilesRequest, title, button string) (string, error) {
	if len(title) > 256 || len(button) > 80 {
		return "", errors.New("invalid directory dialog text")
	}
	initialDirectory, err := paths.Downloads(os.Getenv)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(initialDirectory, 0700); err != nil {
		return "", err
	}
	return m.downloadSelected(req, initialDirectory)
}

func (m *FileDownloads) downloadSelected(req api.FilesRequest, directory string) (string, error) {
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	name := path.Base(path.Clean(req.Path))
	if name == "/" || name == "." {
		name = "root"
	}
	id := m.history.begin(filepath.Join(directory, name), "files")
	m.history.registerCancel(id, cancel)
	destination, err := copyContainerDownload(ctx, directory, path.Base(path.Clean(req.Path)), func(ctx context.Context, w io.Writer) error {
		return m.source.DownloadFiles(api.UIContext(ctx), req, &downloadCountingWriter{Writer: w, history: m.history, id: id})
	}, true)
	status := "complete"
	if err != nil {
		status = "failed"
		if ctx.Err() != nil {
			status = "cancelled"
		}
		if m.ctx.Err() != nil {
			status = "interrupted"
		}
	}
	m.history.finish(id, destination, status, downloadFailureReason(err, "source_unavailable"))
	return destination, err
}

type downloadCountingWriter struct {
	io.Writer
	history *downloadHistory
	id      string
	bytes   int64
}

func (w *downloadCountingWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	w.bytes += int64(n)
	w.history.progress(w.id, w.bytes)
	return n, err
}
