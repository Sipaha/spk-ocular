package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spk/spk-ocular/internal/provider"
)

// Error is a classified Engine failure. Status is the HTTP status of the
// Engine's answer (0: there was none — a connection error, a timeout, a
// limit); Message keeps the daemon's own text when it sent one; Err is the
// cause (context.DeadlineExceeded, a net error, …) for errors.Is.
type Error struct {
	Class   provider.ErrorClass
	Status  int
	Message string
	Err     error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Class))
	if e.Status != 0 {
		fmt.Fprintf(&b, " (%d)", e.Status)
	}
	b.WriteString(": ")
	b.WriteString(e.Message)
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// ClassOf is the class of err: "" for nil, the Engine class of an *Error,
// unavailable for context errors, internal otherwise.
func ClassOf(err error) provider.ErrorClass {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return provider.ClassUnavailable
	}
	return provider.ClassInternal
}

// IsNotFound: the Engine answered 404 (no such object).
func IsNotFound(err error) bool { return ClassOf(err) == provider.ClassNotFound }

// statusClass maps an Engine HTTP status to a class.
func statusClass(code int) provider.ErrorClass {
	switch {
	case code == http.StatusNotFound:
		return provider.ClassNotFound
	case code == http.StatusConflict:
		return provider.ClassConflict
	case code == http.StatusUnauthorized:
		return provider.ClassUnauthorized // as for Kubernetes: not authenticated
	case code == http.StatusForbidden:
		return provider.ClassForbidden
	case code == http.StatusBadRequest:
		return provider.ClassInvalid
	case code >= 500:
		return provider.ClassUnavailable
	case code >= 300 && code < 400:
		// redirects are never followed (a proxy or a web server, not an Engine)
		return provider.ClassUnsupported
	case code >= 400:
		return provider.ClassInvalid
	}
	return provider.ClassUnavailable
}

// statusError builds the error of a non-2xx answer from its (already
// bounded) body: the Engine's {"message": …}, else the text, else the status.
func statusError(code int, status string, body []byte) *Error {
	msg := ""
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) == nil && m.Message != "" {
		msg = m.Message
	} else {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		msg = status
	}
	return &Error{Class: statusClass(code), Status: code, Message: msg}
}

func unsupported(format string, args ...any) *Error {
	return &Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf(format, args...)}
}
