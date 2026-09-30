package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

type LogStreamRequest struct {
	Ref   core.Ref          `json:"ref"`
	Query provider.LogQuery `json:"query"`
}

type LogStreamInfo struct {
	// StreamID: GET <StreamBase>/logs/<StreamID> once, within 30 s.
	StreamID string `json:"streamId"`
}

// SetStreamBase makes StreamBase ask fn (desktop: the lazily started
// loopback server; browser: a path on the main server).
func (s *Service) SetStreamBase(fn func() (string, error)) { s.streamBase = fn }

// Streams is the registry the stream handler serves from.
func (s *Service) Streams() *streams.Registry { return s.streams }

func (s *Service) StreamBase(context.Context) (string, error) {
	if s.streamBase == nil {
		return "", coded(CodeUnsupported, errors.New("streams are not served in this mode"))
	}
	base, err := s.streamBase()
	if err != nil {
		return "", coded(CodeInternal, err)
	}
	return base, nil
}

func (s *Service) logSource(ctx context.Context, ref core.Ref) (*sessionEntry, provider.LogSource, error) {
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return nil, nil, err
	}
	src, ok := e.sess.(provider.LogSource)
	if !ok {
		return nil, nil, coded(CodeUnsupported, errors.New("this target has no logs"))
	}
	return e, src, nil
}

func (s *Service) LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error) {
	_, src, err := s.logSource(ctx, ref)
	if err != nil {
		return core.LogInfo{}, err
	}
	info, err := src.LogInfo(ctx, ref)
	if err != nil {
		return core.LogInfo{}, fromProvider(err)
	}
	return info, nil
}

// OpenLogStream registers a log stream; the work starts when the page
// connects. The stream belongs to the session incarnation: registration
// happens under the lock that closing a session takes, so it can never
// outlive a closed session (CloseOwner then ends it with "gone").
func (s *Service) OpenLogStream(ctx context.Context, req LogStreamRequest) (LogStreamInfo, error) {
	q := req.Query
	if q.TailLines == 0 || q.TailLines < provider.TailAll {
		return LogStreamInfo{}, coded(CodeBadRequest, fmt.Errorf("tailLines must be > 0 or %d (all)", provider.TailAll))
	}
	if q.Previous && q.Follow {
		return LogStreamInfo{}, coded(CodeBadRequest, errors.New("previous logs cannot be followed"))
	}
	e, src, err := s.logSource(ctx, req.Ref)
	if err != nil {
		return LogStreamInfo{}, err
	}
	ref := req.Ref
	id, err := s.registerStream(e, func(ctx context.Context, out *streams.Writer) error {
		return src.StreamLogs(ctx, ref, q, streams.LogSink{W: out})
	})
	if err != nil {
		return LogStreamInfo{}, err
	}
	return LogStreamInfo{StreamID: id}, nil
}

// registerStream adds run for e's incarnation, unless e was closed (or
// replaced) since the caller got it: same lock as closeSessionLocked.
func (s *Service) registerStream(e *sessionEntry, run streams.Func) (string, error) {
	var release func()
	defer func() {
		if release != nil {
			release() // expired streams are closed outside sessMu
		}
	}()
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if s.sessions[ownerKey(e.provider, e.target)] != e {
		return "", coded(CodeGone, errors.New("the session was replaced while opening; retry"))
	}
	id, release, err := s.streams.Register(e.owner, run)
	switch {
	case errors.Is(err, streams.ErrLimit):
		return "", &CodedError{Code: CodeLimit, Detail: err.Error()}
	case err != nil:
		return "", coded(CodeGone, err)
	}
	e.lastUsed = s.now()
	return id, nil
}

// StreamErrorClass classifies a failed stream for its end frame.
func StreamErrorClass(err error) (string, string, *core.Message) {
	ce := fromProvider(err)
	return ce.Code, ce.Detail, ce.Why
}
