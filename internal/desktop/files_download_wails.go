//go:build wails

package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"path"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// FileDownloads owns the native directory grant; renderer-provided local paths
// are never accepted. The service only reads the pinned remote container.
type FileDownloads struct {
	app    *application.App
	window *application.WebviewWindow
	ctx    context.Context
	source interface {
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
	dialog := m.app.Dialog.OpenFile()
	dialog.SetOptions(&application.OpenFileDialogOptions{Title: title, ButtonText: button, Directory: initialDirectory, CanChooseDirectories: true, CanChooseFiles: false, CanCreateDirectories: true, Window: m.window})
	directory, err := dialog.PromptForSingleSelection()
	if err != nil || directory == "" {
		return "", err
	}
	return copyContainerDownload(m.ctx, directory, path.Base(path.Clean(req.Path)), func(ctx context.Context, w io.Writer) error {
		return m.source.DownloadFiles(api.UIContext(ctx), req, w)
	})
}
