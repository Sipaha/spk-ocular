//go:build wails

package desktop

import "context"

// Downloads exposes only IDs issued by native download flows. Renderer paths
// cannot authorize OS file operations, and this service is not in the agent API.
type Downloads struct {
	history *downloadHistory
	ctx     context.Context
}

func (d *Downloads) List() DownloadSnapshot { return d.history.snapshot() }
func (d *Downloads) Open(id string) error   { return d.history.open(d.ctx, id, false) }
func (d *Downloads) Reveal(id string) error { return d.history.open(d.ctx, id, true) }
func (d *Downloads) ClearCompleted()        { d.history.clearCompleted() }

func (d *Downloads) Dismiss(id string) error { return d.history.dismiss(id) }
func (d *Downloads) PrepareDelete(id string) (DownloadDeletePreview, error) {
	return d.history.prepareDelete(id)
}
func (d *Downloads) DeleteFile(id, token string) error { return d.history.deleteFile(id, token) }
func (d *Downloads) CancelDelete(id, token string)     { d.history.cancelDelete(id, token) }
