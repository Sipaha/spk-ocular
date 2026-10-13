package desktop

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"runtime"
	"syscall"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/provider"
)

// Only these non-secret codes cross the history persistence/UI boundary.
func safeDownloadFailure(reason string) string {
	switch reason {
	case "permission", "no_space", "destination_exists", "destination_unavailable", "source_unavailable", "connection", "timeout", "incomplete", "limit", "unsafe_archive", "invalid_archive", "write":
		return reason
	default:
		return "unknown"
	}
}

type downloadFailureError struct {
	reason string
	err    error
}

func (e *downloadFailureError) Error() string { return e.err.Error() }
func (e *downloadFailureError) Unwrap() error { return e.err }
func withDownloadFailure(reason string, err error) error {
	if err == nil {
		return nil
	}
	return &downloadFailureError{reason: downloadFailureReason(err, reason), err: err}
}
func downloadFailureReason(err error, fallback string) string {
	var marked *downloadFailureError
	if errors.As(err, &marked) {
		return safeDownloadFailure(marked.reason)
	}
	if errors.Is(err, fs.ErrPermission) {
		return "permission"
	}
	if errors.Is(err, fs.ErrNotExist) {
		if fallback == "write" || fallback == "destination_unavailable" {
			return "destination_unavailable"
		}
		return safeDownloadFailure(fallback)
	}
	if errors.Is(err, fs.ErrExist) {
		return "destination_exists"
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) || runtime.GOOS == "windows" && (errors.Is(err, syscall.Errno(112)) || errors.Is(err, syscall.Errno(39))) {
		return "no_space"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "timeout"
		}
		return "connection"
	}
	var coded *api.CodedError
	if errors.As(err, &coded) {
		return downloadClassReason(coded.Code, fallback)
	}
	var remote *provider.Error
	if errors.As(err, &remote) {
		return downloadClassReason(string(remote.Class), fallback)
	}
	return safeDownloadFailure(fallback)
}
func downloadClassReason(class, fallback string) string {
	switch class {
	case "forbidden", "unauthorized":
		return "permission"
	case "not_found", "gone", "conflict":
		return "source_unavailable"
	case "timeout":
		return "timeout"
	case "limit":
		return "limit"
	case "unavailable", "disconnected":
		return "connection"
	default:
		return safeDownloadFailure(fallback)
	}
}
