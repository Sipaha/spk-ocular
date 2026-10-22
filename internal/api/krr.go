package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/krr"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type KRRRequest struct {
	Provider string          `json:"provider,omitempty"`
	Target   string          `json:"target,omitempty"`
	Options  *krr.RunOptions `json:"options,omitempty"`
	Review   *krr.Review     `json:"review,omitempty"`
	Command  string          `json:"command"`
	ID       string          `json:"id,omitempty"`
	Version  string          `json:"version,omitempty"`
	Data     []byte          `json:"data,omitempty"`
}
type KRRRecovery struct {
	Provider  string `json:"provider"`
	Target    string `json:"target"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
type KRRResponse struct {
	Recovery        []KRRRecovery `json:"recovery"`
	RecoveryUnknown bool          `json:"recoveryUnknown"`
	Runs            []krr.Run     `json:"runs"`
	Review          *krr.Review   `json:"review,omitempty"`
	Reports         []krr.Summary `json:"reports"`
	Selected        *krr.Stored   `json:"selected,omitempty"`
}

func (s *Service) KRR(ctx context.Context, r KRRRequest) (KRRResponse, error) {
	if !fromUI(ctx) {
		return KRRResponse{}, coded("forbidden", errors.New("KRR reports are available only to the application UI"))
	}
	if len(r.ID) > 32 || len(r.Version) > 16 || len(r.Data) > krr.MaxBytes {
		return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
	}
	if r.Command == "review" || r.Command == "start" {
		return s.krrExecution(ctx, r)
	}
	if r.Provider != "" || r.Target != "" || r.Options != nil || r.Review != nil {
		return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
	}
	var selected *krr.Stored
	var err error
	switch r.Command {
	case "import":
		if r.ID != "" {
			return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
		}
		var report krr.Stored
		report, err = s.krrReports.Import(ctx, r.Data, r.Version)
		if err == nil {
			selected = &report
		}
	case "status", "get", "forget", "cancel":
		if len(r.Data) != 0 || r.Version != "" {
			return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
		}
		switch r.Command {
		case "status":
			if r.ID != "" {
				err = krr.ErrInvalid
			}
		case "get":
			var report krr.Stored
			report, err = s.krrReports.Get(r.ID)
			if err == nil {
				selected = &report
			}
		case "cancel":
			err = s.krrRuns.Cancel(r.ID)
		case "forget":
			err = s.krrReports.Forget(r.ID)
		}
	default:
		err = krr.ErrInvalid
	}
	if err != nil {
		return KRRResponse{}, coded(CodeBadRequest, err)
	}
	out := s.krrStatus(ctx)
	out.Selected = selected
	return out, nil
}

func (s *Service) krrExecution(ctx context.Context, r KRRRequest) (KRRResponse, error) {
	if r.ID != "" || r.Version != "" || len(r.Data) != 0 || r.Provider != "kubernetes" || r.Target == "" || len(r.Target) > 1024 {
		return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
	}
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	if r.Command == "review" {
		if r.Options == nil || r.Review != nil || !r.Options.Valid() {
			return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
		}
		e, err := s.sessionFor(ctx, r.Provider, r.Target)
		if err != nil {
			return KRRResponse{}, err
		}
		runner, ok := e.sess.(provider.KRRRunner)
		if !ok {
			return KRRResponse{}, coded(CodeUnsupported, krr.ErrInvalid)
		}
		review, err := runner.ReviewKRR(ctx, *r.Options)
		if err != nil {
			return KRRResponse{}, coded(CodeBadRequest, err)
		}
		current, err := s.sessionFor(ctx, r.Provider, r.Target)
		if err != nil || current != e {
			return KRRResponse{}, coded(CodeGone, krr.ErrGone)
		}
		review.ConfigRev = s.configRev(e.hash)
		out := s.krrStatus(ctx)
		out.Review = &review
		return out, nil
	}
	if r.Options != nil || r.Review == nil || r.Review.ConfigRev == "" {
		return KRRResponse{}, coded(CodeBadRequest, krr.ErrInvalid)
	}
	sess, err := s.checkedSession(ctx, r.Provider, r.Target, r.Review.ConfigRev)
	if err != nil {
		return KRRResponse{}, err
	}
	runner, ok := sess.(provider.KRRRunner)
	if !ok {
		return KRRResponse{}, coded(CodeUnsupported, krr.ErrInvalid)
	}
	s.krrAdmissionMu.Lock()
	defer s.krrAdmissionMu.Unlock()
	intents, err := s.store.HelperIntents(ctx)
	if err != nil || len(intents) >= 256 {
		return KRRResponse{}, coded(CodeBadRequest, krr.ErrLimit)
	}
	outstanding := 0
	for _, intent := range intents {
		if intent.Kind == "krr-job" {
			outstanding++
		}
	}
	for _, run := range s.krrRuns.List() {
		if run.State != "running" && run.State != "cancelling" {
			continue
		}
		persisted := false
		for _, intent := range intents {
			if intent.Kind == "krr-job" && intent.Provider == run.Provider && intent.Target == run.Target && intent.Name == run.Name {
				persisted = true
			}
		}
		if !persisted {
			outstanding++
		}
	}
	if outstanding >= 8 {
		return KRRResponse{}, coded(CodeBadRequest, errors.New("KRR has eight outstanding helper intents; complete owned cleanup before another run"))
	}
	execution, err := runner.PrepareKRR(ctx, *r.Review, s.helperJournal)
	if err != nil {
		return KRRResponse{}, coded(CodeBadRequest, err)
	}
	s.sessMu.Lock()
	key := ownerKey(r.Provider, r.Target)
	e := s.sessions[key]
	if e == nil || e.sess != sess || !s.explicitConnectionLocked(key, e) {
		s.sessMu.Unlock()
		return KRRResponse{}, coded(CodeGone, krr.ErrGone)
	}
	connection := s.connections[key].liveCtx
	_, err = s.krrRuns.Start(connection, execution, krr.RunTarget{Provider: r.Provider, Target: r.Target, Namespace: r.Review.Options.Namespace, Name: r.Review.Name})
	s.sessMu.Unlock()
	if err != nil {
		return KRRResponse{}, coded(CodeBadRequest, err)
	}
	return s.krrStatus(ctx), nil
}

func (s *Service) krrStatus(ctx context.Context) KRRResponse {
	out := KRRResponse{Reports: s.krrReports.List(), Runs: s.krrRuns.List(), Recovery: []KRRRecovery{}}
	records, err := s.store.HelperIntents(ctx)
	if err != nil {
		out.RecoveryUnknown = true
		return out
	}
	// Keep bounded truncation explicit; never imply an exhaustive empty backlog.
	if len(records) >= 256 {
		out.RecoveryUnknown = true
	}
	for _, r := range records {
		if r.Kind != "krr-job" {
			continue
		}
		active := false
		for _, run := range out.Runs {
			if run.Provider == r.Provider && run.Target == r.Target && run.Name == r.Name && (run.State == "running" || run.State == "cancelling") {
				active = true
			}
		}
		if !active {
			out.Recovery = append(out.Recovery, KRRRecovery{Provider: r.Provider, Target: r.Target, Namespace: r.Namespace, Name: r.Name})
		}
	}
	for i, run := range out.Runs {
		if !run.CleanupPending || out.RecoveryUnknown {
			continue
		}
		pending := false
		for _, r := range records {
			if r.Kind == "krr-job" && r.Provider == run.Provider && r.Target == run.Target && r.Name == run.Name {
				pending = true
			}
		}
		if !pending {
			out.Runs[i].CleanupPending = false
		}
	}
	return out
}
