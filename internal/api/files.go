package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const fileLimit = 2 << 20

type FilesRequest struct {
	ConfigRev string   `json:"configRev"`
	Ref       core.Ref `json:"ref"`
	Instance  string   `json:"instance"`
	Channel   string   `json:"channel"`
	Command   string   `json:"command"`
	Path      string   `json:"path"`
	Text      string   `json:"text"`
	Expect    string   `json:"expect"`
}
type FileEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Symlink   bool   `json:"symlink"`
	Target    string `json:"target,omitempty"`
}
type FilesResponse struct {
	ConfigRev string      `json:"configRev"`
	Path      string      `json:"path"`
	Entries   []FileEntry `json:"entries,omitempty"`
	Text      string      `json:"text"`
	Version   string      `json:"version,omitempty"`
}

type fileBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *fileBuffer) Len() int       { return b.buf.Len() }
func (b *fileBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *fileBuffer) String() string { return b.buf.String() }
func (b *fileBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("container file output exceeds the 2 MiB limit")
	}
	return b.buf.Write(p)
}
func fileVersion(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Files is UI-only. Commands pin the same resource identity as terminals and
// never interpolate paths or contents into shell source.
func (s *Service) Files(ctx context.Context, req FilesRequest) (FilesResponse, error) {
	out := FilesResponse{Path: path.Clean(req.Path)}
	if !fromUI(ctx) {
		return out, coded("forbidden", errors.New("container files are available only to the application UI"))
	}
	if !strings.HasPrefix(req.Path, "/") || strings.ContainsRune(req.Path, 0) || len(req.Path) > 4096 {
		return out, coded(CodeBadRequest, errors.New("an absolute container path is required"))
	}
	if len(req.Text) > fileLimit || !utf8.ValidString(req.Text) || strings.ContainsRune(req.Text, 0) {
		return out, coded(CodeBadRequest, errors.New("only UTF-8 text files up to 2 MiB can be edited"))
	}
	script := ""
	tools := ""
	args := []string{out.Path}
	switch req.Command {
	case "list":
		script = `set -eu; cd "$1"; for p in ./* ./.[!.]* ./..?*; do [ -e "$p" ] || [ -L "$p" ] || continue; kind=f; [ ! -d "$p" ] || kind=d; link=0; [ ! -L "$p" ] || link=1; printf '%s\000%s\000%s\000' "$kind" "$link" "${p#./}"; if [ "$link" = 1 ]; then command -v readlink >/dev/null 2>&1 || { echo 'Container Files: required utility readlink is missing from this image' >&2; exit 127; }; readlink "$p"; fi; printf '\000'; done`
	case "resolve":
		tools = "readlink"
		script = `set -eu; [ -e "$1" ] || { echo 'Symbolic link target does not exist or is inaccessible' >&2; exit 1; }; readlink -f "$1"`
	case "read":
		tools = "head"
		script = `set -eu; [ -f "$1" ] || { echo 'Not a regular file' >&2; exit 1; }; head -c 2097153 "$1"`
	case "write":
		tools = "head mktemp sha256sum wc tr cat rm"
		if len(req.Expect) != 64 {
			return out, coded(CodeBadRequest, errors.New("the original file version is required"))
		}
		script = `set -eu; [ -f "$1" ] || { echo 'Not a regular file' >&2; exit 1; }; tmp=$(mktemp "${1}.ocular.XXXXXX"); trap 'rm -f "$tmp"' EXIT HUP INT TERM; head -c "$3" > "$tmp"; [ "$(wc -c < "$tmp" | tr -d ' ')" = "$3" ] || { echo 'Incomplete file input' >&2; exit 1; }; actual=$(sha256sum < "$1"); [ "${actual%% *}" = "$2" ] || { echo 'File changed externally; reopen it before saving' >&2; exit 1; }; cat "$tmp" > "$1"`
		args = append(args, req.Expect, fmt.Sprint(len(req.Text)))
	default:
		return out, coded(CodeBadRequest, errors.New("unknown file operation"))
	}
	if req.Instance == "" {
		return out, coded(CodeBadRequest, errors.New("select a container instance first"))
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ex, err := s.execer(ctx, req.Ref)
	if err != nil {
		return out, err
	}
	if tools != "" {
		script = `for tool in ` + tools + `; do command -v "$tool" >/dev/null 2>&1 || { printf 'Container Files: required utility %s is missing from this image\n' "$tool" >&2; exit 127; }; done; ` + script
	}
	argv := append([]string{"sh", "-c", script, "ocular-files"}, args...)
	h, err := ex.PrepareExec(ctx, req.Ref, provider.ExecRequest{Instance: req.Instance, Channel: req.Channel, Command: argv})
	if err != nil {
		return out, fromProvider(err)
	}
	defer h.Close()
	out.ConfigRev = s.live(h.Describe()).ConfigRev
	if req.ConfigRev != "" && req.ConfigRev != out.ConfigRev {
		return out, coded(CodeConflict, errors.New("container connection changed; close and reopen Files"))
	}
	if req.Command == "write" && req.ConfigRev == "" {
		return out, coded(CodeBadRequest, errors.New("the original connection revision is required"))
	}
	stdout := &fileBuffer{limit: fileLimit}
	stderr := &fileBuffer{limit: 8192}
	status, err := h.Run(ctx, provider.Terminal{Raw: true, Stdin: strings.NewReader(req.Text), Stdout: stdout, Stderr: stderr})
	if err != nil {
		return out, fromProvider(fmt.Errorf("container file operation failed (a POSIX sh is required in the image): %w", err))
	}
	if !status.Known || status.Code != 0 {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = "container file operation failed (the write outcome may be unknown)"
		}
		return out, coded(CodeBadRequest, errors.New(detail))
	}
	switch req.Command {
	case "list":
		parts := bytes.Split(stdout.Bytes(), []byte{0})
		out.Entries = []FileEntry{}
		if len(parts)%4 != 1 {
			return out, errors.New("invalid directory response")
		}
		for i := 0; i < len(parts)-1; i += 4 {
			if !utf8.Valid(parts[i+2]) || !utf8.Valid(parts[i+3]) {
				return out, errors.New("directory contains non-UTF-8 names")
			}
			out.Entries = append(out.Entries, FileEntry{Name: string(parts[i+2]), Directory: string(parts[i]) == "d", Symlink: string(parts[i+1]) == "1", Target: strings.TrimSuffix(string(parts[i+3]), "\n")})
		}
	case "resolve":
		resolved := strings.TrimSuffix(stdout.String(), "\n")
		if !utf8.ValidString(resolved) || !strings.HasPrefix(resolved, "/") || strings.ContainsRune(resolved, 0) || len(resolved) > 4096 {
			return out, errors.New("invalid symbolic link target")
		}
		out.Path = resolved
	case "read":
		b := stdout.Bytes()
		if !utf8.Valid(b) || bytes.Contains(b, []byte{0}) {
			return out, coded(CodeUnsupported, errors.New("this is a binary or non-UTF-8 file"))
		}
		out.Text = string(b)
		out.Version = fileVersion(b)
	case "write":
		out.Text = req.Text
		out.Version = fileVersion([]byte(req.Text))
	}
	return out, nil
}

var _ io.Writer = (*fileBuffer)(nil)
