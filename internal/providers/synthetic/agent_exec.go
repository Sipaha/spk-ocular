package synthetic

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Scoped finite-command fixture for agent API and confirmation tests. It never
// executes local programs or opens a real container connection.
func (s *session) parcelExecInfo(ref core.Ref) (core.ExecInfo, error) {
	s.p.parcels.mu.Lock()
	defer s.p.parcels.mu.Unlock()
	p := s.p.parcels.findLocked(ref.Name)
	if p == nil || p.zone != ref.Scope || ref.UID != "" && p.ref().UID != ref.UID {
		return core.ExecInfo{}, &provider.Error{Class: provider.ClassGone, Message: "parcel was removed or replaced"}
	}
	return core.ExecInfo{Instances: []core.ExecInstance{{ID: p.name + "/" + p.ref().UID, Title: p.name, Ready: true, DefaultChannel: "main", Channels: []core.ExecChannel{{ID: "main", Title: "main", Running: true}}}}}, nil
}
func (s *session) prepareParcelExec(ref core.Ref, req provider.ExecRequest) (provider.ExecHandle, error) {
	info, err := s.parcelExecInfo(ref)
	if err != nil {
		return nil, err
	}
	if req.Instance != info.Instances[0].ID || req.Channel != "main" || req.Attach || len(req.Command) == 0 {
		return nil, &provider.Error{Class: provider.ClassInvalid, Message: "explicit finite parcel command required"}
	}
	return &parcelExecHandle{s: s, ref: ref, request: req}, nil
}

type parcelExecHandle struct {
	s       *session
	ref     core.Ref
	request provider.ExecRequest
}

func (h *parcelExecHandle) Describe() core.LiveTarget {
	t := liveTarget(h.ref, h.s.hash)
	t.Instance = h.ref.Name
	t.Channel = h.request.Channel
	t.Command = h.request.Command
	return t
}
func (h *parcelExecHandle) Again() (provider.ExecHandle, error) { c := *h; return &c, nil }
func (h *parcelExecHandle) Close()                              {}
func (h *parcelExecHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	if _, err := h.s.parcelExecInfo(h.ref); err != nil {
		return provider.ExitStatus{}, err
	}
	if ctx.Err() != nil {
		return provider.ExitStatus{}, ctx.Err()
	}
	if !t.Raw {
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassUnsupported, Message: "parcel fixture supports only finite non-PTY commands"}
	}
	code := 0
	var err error
	switch h.request.Command[0] {
	case "echo":
		_, err = io.WriteString(t.Stdout, strings.Join(h.request.Command[1:], " ")+"\n")
	case "stdin":
		if t.Stdin != nil {
			_, err = io.Copy(t.Stdout, t.Stdin)
		}
	case "exit":
		if len(h.request.Command) > 1 {
			code, err = strconv.Atoi(h.request.Command[1])
		}
	default:
		_, err = io.WriteString(t.Stderr, "synthetic executable not found\n")
		code = 127
	}
	return provider.ExitStatus{Known: err == nil, Code: code}, err
}
