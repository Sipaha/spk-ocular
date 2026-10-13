package desktop

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spk/spk-ocular/internal/privatefs"
)

// stagedLogExport leaves the chosen destination untouched until complete.
// Abort, expiry and failed writes remove its owner-only sibling temporary file.
type stagedLogExport struct {
	mu          sync.Mutex
	file        *os.File
	destination string
	temporary   string
	bytes       int64
	limit       int64
	closed      bool
}

func newStagedLogExport(destination string, limit int64) (*stagedLogExport, error) {
	f, err := privatefs.CreateTemp(filepath.Dir(destination), ".ocular-log-download-*")
	if err != nil {
		return nil, err
	}
	return &stagedLogExport{file: f, destination: destination, temporary: f.Name(), limit: limit}, nil
}
func (s *stagedLogExport) Write(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("log export is closed")
	}
	if int64(len(text)) > s.limit-s.bytes {
		s.abortLocked()
		return withDownloadFailure("limit", errors.New("log export exceeds its byte limit"))
	}
	n, err := io.Copy(s.file, strings.NewReader(text))
	s.bytes += n
	if err != nil {
		s.abortLocked()
	}
	return err
}
func (s *stagedLogExport) abortLocked() {
	if s.closed {
		return
	}
	s.closed = true
	_ = s.file.Close()
	_ = os.Remove(s.temporary)
}
func (s *stagedLogExport) Abort() { s.mu.Lock(); defer s.mu.Unlock(); s.abortLocked() }
func (s *stagedLogExport) Complete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("log export is closed")
	}
	defer s.abortLocked()
	if err := s.file.Sync(); err != nil {
		return err
	}
	if err := s.file.Close(); err != nil {
		return err
	}
	return os.Rename(s.temporary, s.destination)
}

// writeLogExport retains the bounded buffered-export entry point.
func writeLogExport(destination, text string) error {
	staged, err := newStagedLogExport(destination, 96<<20)
	if err != nil {
		return err
	}
	defer staged.Abort()
	if err = staged.Write(text); err != nil {
		return err
	}
	return staged.Complete()
}
