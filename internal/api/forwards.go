package api

import (
	"context"
	"errors"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/provider"
)

// EventForwardsChanged: the tunnels changed (a state at once, counters at
// most once a second); call ListForwards.
const EventForwardsChanged = "forwards_changed"

type StartForwardRequest struct {
	Ref core.Ref `json:"ref"`
	// Port: ForwardInfo's port number.
	Port int `json:"port"`
	// LocalPort: 0 = the remote port when it is ≥ 1024 and free, otherwise
	// any; an explicit port that is taken is a conflict.
	LocalPort int `json:"localPort,omitempty"`
	// Scheme: "http"/"https" lets the UI offer "Open".
	Scheme string `json:"scheme,omitempty"`
}

func newForwards(em *events.Emitter) *forwards.Manager {
	return forwards.NewManager(forwards.Options{OnChange: func() {
		em.Emit(events.Event{Type: EventForwardsChanged})
	}})
}

func (s *Service) forwarder(ctx context.Context, ref core.Ref) (provider.PortForwarder, error) {
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	pf, ok := e.sess.(provider.PortForwarder)
	if !ok {
		return nil, coded(CodeUnsupported, errors.New("ports cannot be forwarded on this target"))
	}
	return pf, nil
}

func (s *Service) ForwardInfo(ctx context.Context, ref core.Ref) (core.ForwardInfo, error) {
	pf, err := s.forwarder(ctx, ref)
	if err != nil {
		return core.ForwardInfo{}, err
	}
	info, err := pf.ForwardInfo(ctx, ref)
	if err != nil {
		return core.ForwardInfo{}, fromProvider(err)
	}
	return info, nil
}

// StartForward prepares the target, listens and connects once; the tunnel
// belongs to the app (it outlives the session) until StopForward or exit.
func (s *Service) StartForward(ctx context.Context, req StartForwardRequest) (forwards.Info, error) {
	if req.Port < 1 || req.Port > 65535 {
		return forwards.Info{}, coded(CodeBadRequest, errors.New("the port must be 1..65535"))
	}
	if req.LocalPort < 0 || req.LocalPort > 65535 {
		return forwards.Info{}, coded(CodeBadRequest, errors.New("the local port must be 1..65535"))
	}
	if req.Scheme != "" && req.Scheme != "http" && req.Scheme != "https" {
		return forwards.Info{}, coded(CodeBadRequest, errors.New("the scheme must be http or https"))
	}
	pf, err := s.forwarder(ctx, req.Ref)
	if err != nil {
		return forwards.Info{}, err
	}
	h, err := pf.PrepareForward(ctx, req.Ref, provider.ForwardRequest{Port: req.Port})
	if err != nil {
		return forwards.Info{}, fromProvider(err)
	}
	info, err := s.fwd.Start(ctx, h, forwards.StartRequest{LocalPort: req.LocalPort, Scheme: req.Scheme})
	if err != nil {
		return forwards.Info{}, forwardError(err)
	}
	return info, nil
}

func (s *Service) StopForward(_ context.Context, id string) error {
	if err := s.fwd.Stop(id); err != nil {
		return forwardError(err)
	}
	return nil
}

func (s *Service) ListForwards(context.Context) ([]forwards.Info, error) {
	return s.fwd.List(), nil
}

func forwardError(err error) *CodedError {
	switch {
	case errors.Is(err, forwards.ErrLimit):
		return &CodedError{Code: CodeLimit, Detail: err.Error()}
	case errors.Is(err, forwards.ErrClosed), errors.Is(err, forwards.ErrStopped):
		return coded(CodeGone, err)
	case errors.Is(err, forwards.ErrNotFound):
		return coded(CodeNotFound, err)
	case errors.Is(err, context.DeadlineExceeded):
		return &CodedError{Code: string(provider.ClassUnavailable), Detail: "timed out connecting"}
	}
	return fromProvider(err)
}
