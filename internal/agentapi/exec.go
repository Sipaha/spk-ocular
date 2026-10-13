package agentapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
)

const maxExecs = 4

type activeExec struct {
	ref    core.Ref
	cancel context.CancelCauseFunc
}

// Register before grant admission so a concurrent save cancels admitted work.
func (s *Server) beginExec(ctx context.Context, ref core.Ref) (context.Context, func(), error) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	if len(s.execs) >= maxExecs {
		return nil, nil, &api.CodedError{Code: api.CodeLimit, Detail: "At most four agent exec operations may run at once."}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	op := &activeExec{ref: ref, cancel: cancel}
	s.execs[op] = struct{}{}
	stop := context.AfterFunc(s.reqCtx, func() { cancel(context.Canceled) })
	return ctx, func() { stop(); cancel(context.Canceled); s.execMu.Lock(); delete(s.execs, op); s.execMu.Unlock() }, nil
}

func (s *Server) cancelExecs(providerID, target string) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	for op := range s.execs {
		if providerID == "" || op.ref.Provider == providerID && op.ref.Target == target {
			op.cancel(forbidden("exec access changed or was revoked; check Access before preparing another command"))
		}
	}
}

func (s *Server) openExec(ctx context.Context, c caller, method string, ref core.Ref) (*session, error) {
	if ref.Name == "" || ref.UID == "" {
		return nil, badRequest("ref.name and ref.uid are required for exec")
	}
	x, err := s.open(ctx, c, method, ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	k, err := x.kind(ref.Kind)
	if err == nil && !k.Exec {
		err = &api.CodedError{Code: api.CodeUnsupported, Detail: fmt.Sprintf("%s do not support exec", k.ID)}
	}
	if err == nil {
		_, err = s.check(c, x, method, k, ref, agentgrant.VerbExec, true)
	}
	if err != nil {
		x.done()
		return nil, err
	}
	return x, nil
}

type GetExecInfoRequest struct {
	Ref core.Ref `json:"ref" jsonschema:"required"`
}

type PrepareExecRequest struct {
	Ref        core.Ref `json:"ref" jsonschema:"required" jsonschema_description:"Exact object reference including UID, namespace/project and target."`
	Instance   string   `json:"instance" jsonschema:"required" jsonschema_description:"Explicit instance ID from GetExecInfo, even for a single instance. No automatic replica selection."`
	Channel    string   `json:"channel,omitempty" jsonschema_description:"Explicit running container channel from GetExecInfo for Kubernetes; empty for Docker."`
	Command    []string `json:"command" jsonschema:"required" jsonschema_description:"Explicit argv, 1..64 arguments, total at most 16384 UTF-8 bytes, no NUL. No implicit shell or retry."`
	Stdin      string   `json:"stdin,omitempty" jsonschema_description:"Finite UTF-8 standard input, at most 65536 bytes. Shown for confirmation, never journaled."`
	MaxBytes   int      `json:"maxBytes,omitempty" jsonschema_description:"Combined raw stdout/stderr byte limit, 4096..1048576, default 65536. Returned as base64 to preserve binary output."`
	MaxSeconds int      `json:"maxSeconds,omitempty" jsonschema_description:"Maximum execution duration, 1..120 seconds, default 30. Cancellation stops transport; remote termination is best effort."`
}

type PrepareExecView struct {
	PlanID       string            `json:"planId"`
	Plan         api.AgentExecPlan `json:"plan"`
	Confirmation string            `json:"confirmation"`
}

// ExecResult reports observed exit separately from transport completion. A
// timeout/disconnect may leave the remote command running; never infer success.
type ExecResult struct {
	Stdout     []byte          `json:"stdoutBase64"`
	Stderr     []byte          `json:"stderrBase64"`
	ExitKnown  bool            `json:"exitKnown"`
	ExitCode   *int            `json:"exitCode,omitempty"`
	Complete   bool            `json:"complete"`
	Truncated  bool            `json:"truncated"`
	StopReason string          `json:"stopReason"`
	Error      *api.CodedError `json:"error,omitempty"`
}

func (s *Server) execMethods() {
	register(s, "GetExecInfo", agentgrant.VerbExec, false, "Discover concrete instances and running channels using an explicit exec grant with named kinds. Ref must include UID. This does not start a command or grant read/log access.", s.getExecInfo)
	register(s, "PrepareExec", agentgrant.VerbExec, false, "Validate an explicit instance, channel and finite argv command without starting it. Returns an in-memory one-shot plan valid 10 minutes, bound to object UID and connection revision. Commands can read secrets or change container/host data; confirmation is required unless this exec grant explicitly sets noConfirm.", s.prepareExec)
	register(s, "RunExec", agentgrant.VerbExec, true, "Run a prepared exec plan once, with fresh grant, target identity, connection revision and container membership checks. Confirmation uses GetRun. Raw non-PTY stdout/stderr is base64, bounded together; exitKnown and stopReason distinguish exit, timeout, byte limit and unknown outcome. Direct calls cancel on client disconnect. Confirmed runs have their own bounded lifetime. Revocation/configuration changes cancel transport; remote termination is best effort. No automatic replay, attach or interactive shell.", s.runExec)
}

func (s *Server) getExecInfo(ctx context.Context, c caller, req *GetExecInfoRequest) (*core.ExecInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	ctx, done, err := s.beginExec(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	defer done()
	x, err := s.openExec(ctx, c, "GetExecInfo", req.Ref)
	if err != nil {
		return nil, err
	}
	defer x.done()
	info, err := x.call.AgentExecInfo(req.Ref)
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if err != nil {
		return nil, err
	}
	s.read(c, "GetExecInfo", req.Ref.Provider, req.Ref.Target, req.Ref.Scope, objectOf(req.Ref))
	return &info, nil
}

func validateExec(req *PrepareExecRequest) error {
	if req.Instance == "" || len(req.Instance) > 4096 || len(req.Channel) > 4096 || strings.ContainsRune(req.Instance+req.Channel, 0) {
		return badRequest("an explicit valid instance is required")
	}
	if len(req.Command) < 1 || len(req.Command) > 64 || req.Command[0] == "" {
		return badRequest("command must contain an executable and at most 64 arguments")
	}
	total := 0
	for _, arg := range req.Command {
		total += len(arg)
		if !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return badRequest("command arguments must be UTF-8 without NUL")
		}
	}
	if total > 16384 {
		return badRequest("command arguments exceed 16384 bytes")
	}
	if len(req.Stdin) > 65536 || !utf8.ValidString(req.Stdin) {
		return badRequest("stdin must be UTF-8 and at most 65536 bytes")
	}
	if req.MaxBytes == 0 {
		req.MaxBytes = 65536
	}
	if req.MaxSeconds == 0 {
		req.MaxSeconds = 30
	}
	if req.MaxBytes < 4096 || req.MaxBytes > 1048576 || req.MaxSeconds < 1 || req.MaxSeconds > 120 {
		return badRequest("maxBytes must be 4096..1048576; maxSeconds must be 1..120")
	}
	return nil
}

func (s *Server) prepareExec(ctx context.Context, c caller, req *PrepareExecRequest) (*PrepareExecView, error) {
	if err := validateExec(req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	ctx, done, err := s.beginExec(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	defer done()
	x, err := s.openExec(ctx, c, "PrepareExec", req.Ref)
	if err != nil {
		return nil, err
	}
	defer x.done()
	info, err := x.call.AgentExecInfo(req.Ref)
	if err != nil {
		return nil, err
	}
	found := false
	for _, inst := range info.Instances {
		if inst.ID != req.Instance {
			continue
		}
		found = true
		if len(inst.Channels) > 0 {
			running := false
			for _, ch := range inst.Channels {
				if ch.ID == req.Channel && ch.Running {
					running = true
				}
			}
			if !running {
				return nil, badRequest("select an explicit running channel from GetExecInfo")
			}
		} else if req.Channel != "" {
			return nil, badRequest("this instance has no channels")
		}
	}
	if !found {
		return nil, badRequest("instance is not an offered member of this object; refresh GetExecInfo")
	}
	h, where, err := x.call.PrepareAgentExec(req.Ref, provider.ExecRequest{Instance: req.Instance, Channel: req.Channel, Command: req.Command}, "")
	if err != nil {
		return nil, err
	}
	h.Close()
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	k, err := x.kind(req.Ref.Kind)
	if err != nil {
		return nil, err
	}
	ep := &api.AgentExecPlan{Where: where, Instance: req.Instance, Channel: req.Channel, Command: append([]string{}, req.Command...), Stdin: req.Stdin, MaxBytes: req.MaxBytes, MaxSeconds: req.MaxSeconds}
	p := &plan{agent: c.agent, method: "RunExec", provider: req.Ref.Provider, target: req.Ref.Target, title: x.call.Title(), ref: req.Ref, kind: k, verb: agentgrant.VerbExec, destructive: true, exec: ep}
	confirm, err := s.judge(c, x, "PrepareExec", p)
	if err != nil {
		return nil, err
	}
	confirmation := "none"
	if confirm {
		confirmation = "user"
	}
	s.read(c, "PrepareExec", p.provider, p.target, p.ref.Scope, objectOf(p.ref))
	return &PrepareExecView{PlanID: s.plans.put("plan-", p), Plan: *ep, Confirmation: confirmation}, nil
}

func (s *Server) runExec(ctx context.Context, c caller, req *RunRequest) (*RunView, error) {
	return s.runPlan(ctx, c, "RunExec", req.PlanID)
}

func (s *Server) executeExec(ctx context.Context, p *plan) RunView {
	ctx, done, err := s.beginExec(ctx, p.ref)
	if err != nil {
		return failed(err)
	}
	defer done()
	x, err := s.openExec(ctx, caller{agent: p.agent}, p.method, p.ref)
	if err != nil {
		return failed(err)
	}
	defer x.done()
	ep := p.exec
	h, _, err := x.call.PrepareAgentExec(p.ref, provider.ExecRequest{Instance: ep.Instance, Channel: ep.Channel, Command: ep.Command}, ep.Where.ConfigRev)
	if err != nil {
		return failed(err)
	}
	defer h.Close()
	if ctx.Err() != nil || x.call.Context().Err() != nil {
		return failed(context.Canceled)
	}
	e := store.AuditEntry{Agent: p.agent, Method: p.method, Provider: p.provider, Target: p.target, Scope: p.ref.Scope, Object: objectOf(p.ref), Verb: p.verb, Destructive: true, ExpectHash: expectHash(p), Phase: store.AuditIntent}
	s.audit(e)
	runCtx, cancel := context.WithTimeout(x.call.Context(), time.Duration(ep.MaxSeconds)*time.Second)
	defer cancel()
	sink := &execOutput{limit: ep.MaxBytes, cancel: cancel, stdout: []byte{}, stderr: []byte{}}
	var stdin *strings.Reader
	if ep.Stdin != "" {
		stdin = strings.NewReader(ep.Stdin)
	}
	terminal := provider.Terminal{Raw: true, CloseStdin: true, Stdout: execWriter{sink: sink}, Stderr: execWriter{sink: sink, stderr: true}}
	if stdin != nil {
		terminal.Stdin = stdin
	}
	status, runErr := h.Run(runCtx, terminal)
	result := sink.result(status)
	switch {
	case sink.exceeded():
		result.StopReason = "byte_limit"
		result.Truncated = true
	case ctx.Err() != nil:
		result.StopReason = "cancelled"
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.StopReason = "time_limit"
	case x.call.Context().Err() != nil:
		result.StopReason = "connection_changed"
	case runErr != nil:
		result.StopReason = "error"
	case !status.Known:
		result.StopReason = "unknown"
	default:
		result.StopReason = "exit"
		result.Complete = true
	}
	if runErr != nil && result.StopReason == "error" {
		ce := *errCoded(runErr)
		if len(ce.Detail) > 256 {
			ce.Detail = ce.Detail[:256]
		}
		result.Error = &ce
	}
	if ctx.Err() != nil || x.call.Context().Err() != nil {
		// Revoked/replaced calls cannot return sensitive buffered data after admission ends.
		result.Stdout, result.Stderr = []byte{}, []byte{}
	}
	// Journal metadata only; argv, stdin, output and provider errors may contain secrets.
	e.Phase = store.AuditOutcome
	e.Outcome = "unknown"
	if result.Complete {
		e.Outcome = "done"
	}
	e.Detail = "exec stopped: " + result.StopReason
	s.audit(e)
	return RunView{State: stateDone, Exec: &result}
}

var errExecBudget = errors.New("exec output byte limit reached")

type execOutput struct {
	mu             sync.Mutex
	limit          int
	cancel         context.CancelFunc
	stdout, stderr []byte
	over           bool
}
type execWriter struct {
	sink   *execOutput
	stderr bool
}

func (w execWriter) Write(p []byte) (int, error) {
	s := w.sink
	s.mu.Lock()
	defer s.mu.Unlock()
	left := s.limit - len(s.stdout) - len(s.stderr)
	n := min(len(p), left)
	if w.stderr {
		s.stderr = append(s.stderr, p[:n]...)
	} else {
		s.stdout = append(s.stdout, p[:n]...)
	}
	if n < len(p) {
		s.over = true
		s.cancel()
		return n, errExecBudget
	}
	return n, nil
}
func (s *execOutput) exceeded() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.over }
func (s *execOutput) result(status provider.ExitStatus) ExecResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := ExecResult{Stdout: append([]byte{}, s.stdout...), Stderr: append([]byte{}, s.stderr...), ExitKnown: status.Known}
	if status.Known {
		code := status.Code
		r.ExitCode = &code
	}
	return r
}
