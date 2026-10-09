package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// DownloadFiles streams a tar archive to a trusted UI-owned sink. It never
// accepts a local destination from the renderer or exposes agent access.
func (s *Service) DownloadFiles(ctx context.Context, req FilesRequest, dst io.Writer) error {
	if !fromUI(ctx) {
		return coded("forbidden", errors.New("container files are available only to the application UI"))
	}
	if !strings.HasPrefix(req.Path, "/") || strings.ContainsRune(req.Path, 0) || len(req.Path) > 4096 || req.Instance == "" || req.ConfigRev == "" {
		return coded(CodeBadRequest, errors.New("an absolute path, pinned container and connection revision are required"))
	}
	p := path.Clean(req.Path)
	parent, name := path.Dir(p), path.Base(p)
	if p == "/" {
		parent = "/"
		name = "."
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	ex, err := s.execer(ctx, req.Ref)
	if err != nil {
		return err
	}
	// Prefixing the operand with ./ prevents option injection, including names
	// beginning with a dash. No path is interpolated into shell source.
	script := `set -eu; command -v tar >/dev/null 2>&1 || { echo 'Container Files: required utility tar is missing from this image' >&2; exit 127; }; cd "$1"; [ ! -L "./$2" ] || { echo 'Downloading a symbolic link is not supported' >&2; exit 1; }; [ -f "./$2" ] || [ -d "./$2" ] || { echo 'Not a regular file or directory' >&2; exit 1; }; exec tar -c -f - "./$2"`
	h, err := ex.PrepareExec(ctx, req.Ref, provider.ExecRequest{Instance: req.Instance, Channel: req.Channel, Command: []string{"sh", "-c", script, "ocular-download", parent, name}})
	if err != nil {
		return fromProvider(err)
	}
	defer h.Close()
	if s.live(h.Describe()).ConfigRev != req.ConfigRev {
		return coded(CodeConflict, errors.New("container connection changed; close and reopen Files"))
	}
	stderr := &fileBuffer{limit: 8192}
	status, err := h.Run(ctx, provider.Terminal{Raw: true, Stdout: dst, Stderr: stderr, Stdin: strings.NewReader("")})
	if err != nil {
		return fromProvider(fmt.Errorf("container download failed: %w", err))
	}
	if !status.Known || status.Code != 0 {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = "container download did not complete"
		}
		return errors.New(detail)
	}
	return nil
}
