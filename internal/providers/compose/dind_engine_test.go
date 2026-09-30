package compose

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// The Engine client's writes on the real daemon, on a throwaway container
// of the test: exec with its first size, exit codes (a real one, a command
// that never started), stats, and stop/start/restart/remove with 304 and
// the refusal to remove a running container.
func TestDindEngineWrites(t *testing.T) {
	host := dindHost(t)
	const name = "ocular-test-writes"
	dindDocker(t, host, "rm", "-f", name)
	id := strings.TrimSpace(dindDocker(t, host, "run", "-d", "--name", name, "busybox:latest", "sleep", "3600"))
	t.Cleanup(func() { dindDocker(t, host, "rm", "-f", name) })
	cl, err := engine.New(engine.Config{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	size := engine.ConsoleSize{Rows: 40, Cols: 132}
	execOut := func(cmd []string, input string) (string, engine.ExecInspect) {
		t.Helper()
		xid, err := cl.CreateExec(ctx, id, engine.ExecConfig{Cmd: cmd, Tty: true, Size: size})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := cl.StartExec(ctx, xid, true, size)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		// a command that never started refuses it ("exec process is not
		// started"): the size is checked by stty below
		_ = cl.SetInitialSize(ctx, xid, size)
		if input != "" {
			if _, err := io.WriteString(conn, input); err != nil {
				t.Fatal(err)
			}
		}
		out, _ := io.ReadAll(bufio.NewReader(conn))
		var st engine.ExecInspect
		deadline := time.Now().Add(10 * time.Second)
		for {
			st, err = cl.InspectExec(ctx, xid)
			if err == nil && !st.Running || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil || st.Running {
			t.Fatalf("exec did not end: %+v %v", st, err)
		}
		return string(out), st
	}
	dindVerify(t, host)
	out, st := execOut([]string{"sh"}, "stty size; exit 3\n")
	if !strings.Contains(out, "40 132") {
		t.Errorf("the first size is not the TTY's: %q", out)
	}
	if st.ExitCode == nil || *st.ExitCode != 3 || st.Pid == 0 {
		t.Errorf("exit 3: %+v", st)
	}
	out, st = execOut([]string{"/nonexistent"}, "")
	if st.ExitCode == nil || *st.ExitCode != 127 || st.Pid != 0 || !strings.Contains(out, "no such file") {
		t.Errorf("a command that never started: %+v %q", st, out)
	}

	one, err := cl.ContainerStats(ctx, id, true)
	if err != nil || one.Read.IsZero() || one.MemoryStats.Usage <= 0 || one.CPUStats.OnlineCPUs <= 0 {
		t.Errorf("one-shot stats: %+v %v", one, err)
	}
	two, err := cl.ContainerStats(ctx, id, false)
	if err != nil || two.PreCPUStats.SystemCPUUsage == 0 {
		t.Errorf("two-point stats have no precpu: %+v %v", two, err)
	}

	dindVerify(t, host)
	if err := cl.RemoveContainer(ctx, id); engine.ClassOf(err) != provider.ClassConflict {
		t.Errorf("removing a running container: %v", err)
	}
	if nm, err := cl.StartContainer(ctx, id); err != nil || !nm {
		t.Errorf("start of a running container: %v %v", nm, err)
	}
	if nm, err := cl.StopContainer(ctx, id, 1); err != nil || nm {
		t.Errorf("stop: %v %v", nm, err)
	}
	if nm, err := cl.StopContainer(ctx, id, 1); err != nil || !nm {
		t.Errorf("repeated stop: %v %v", nm, err)
	}
	if err := cl.RestartContainer(ctx, id, 1); err != nil {
		t.Errorf("restart of a stopped container: %v", err)
	}
	if c, err := cl.InspectContainer(ctx, id); err != nil || !c.State.Running {
		t.Errorf("restart did not start it: %+v %v", c.State, err)
	}
	if nm, err := cl.StopContainer(ctx, id, 0); err != nil || nm {
		t.Errorf("stop t=0: %v %v", nm, err)
	}
	dindVerify(t, host)
	if err := cl.RemoveContainer(ctx, id); err != nil {
		t.Errorf("remove: %v", err)
	}
	if _, err := cl.InspectContainer(ctx, id); !engine.IsNotFound(err) {
		t.Errorf("removed container still inspects: %v", err)
	}
}

// A terminal in a chosen replica of a service of the fixture: the shell
// runs in exactly that container with the terminal's first size, the exit
// code comes back; a missing command is refused with the daemon's text; a
// closed terminal ends the run promptly.
func TestDindExec(t *testing.T) {
	host := dindHost(t)
	s := dindSession(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	ref := core.Ref{Provider: ProviderID, Target: "context:dind", Scope: "ocular-fixture", Kind: KindServices, Name: "ocular-fixture/logger"}
	info, err := s.ExecInfo(ctx, ref)
	if err != nil || len(info.Instances) != 2 {
		t.Fatalf("logger replicas: %+v %v", info, err)
	}
	second := info.Instances[1]
	dindVerify(t, host)
	h, err := s.PrepareExec(ctx, ref, provider.ExecRequest{Instance: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if d := h.Describe(); d.Instance != second.Title || d.Endpoint == "" {
		t.Errorf("describe: %+v", d)
	}
	r, w := io.Pipe()
	var out syncBuf
	done := make(chan struct{})
	var st provider.ExitStatus
	go func() {
		defer close(done)
		st, err = h.Run(ctx, provider.Terminal{Stdin: r, Stdout: &out, Sizes: newSizes(provider.TermSize{Cols: 100, Rows: 30})})
	}()
	_, _ = io.WriteString(w, "echo host=$(hostname); stty size; exit 3\n")
	<-done
	if err != nil || st != (provider.ExitStatus{Code: 3, Known: true}) {
		t.Fatalf("run: %+v %v\n%s", st, err, out.String())
	}
	if !strings.Contains(out.String(), "host="+second.ID[:12]) || !strings.Contains(out.String(), "30 100") {
		t.Errorf("not in the chosen replica or not its size: %q", out.String())
	}

	dindVerify(t, host)
	missing, err := s.PrepareExec(ctx, ref, provider.ExecRequest{Instance: second.ID, Command: []string{"/nonexistent"}})
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Close()
	var mout syncBuf
	_, err = missing.Run(ctx, provider.Terminal{Stdin: strings.NewReader(""), Stdout: &mout, Sizes: newSizes(provider.TermSize{Cols: 80, Rows: 24})})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Class != provider.ClassInvalid || !strings.Contains(mout.String(), "no such file") {
		t.Errorf("a missing command: %v %q", err, mout.String())
	}

	dindVerify(t, host)
	again, err := h.Again()
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	rctx, stop := context.WithCancel(ctx)
	r2, w2 := io.Pipe()
	defer w2.Close()
	var out2 syncBuf
	ended := make(chan error, 1)
	go func() {
		_, err := again.Run(rctx, provider.Terminal{Stdin: r2, Stdout: &out2, Sizes: newSizes(provider.TermSize{Cols: 80, Rows: 24})})
		ended <- err
	}()
	_, _ = io.WriteString(w2, "echo ready\n")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out2.String(), "ready") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a closed terminal: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run outlived its context")
	}
}

// Usage of the fixture's logger replicas and of the service: CPU in cores
// from the daemon's two points, memory > 0, the sum of both replicas.
func TestDindMetrics(t *testing.T) {
	host := dindHost(t)
	s := dindSession(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ref := core.Ref{Provider: ProviderID, Target: "context:dind", Scope: "ocular-fixture", Kind: KindServices, Name: "ocular-fixture/logger"}
	info, err := s.ExecInfo(ctx, ref) // the running replicas
	if err != nil || len(info.Instances) != 2 {
		t.Fatalf("logger replicas: %+v %v", info, err)
	}
	q := provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	ids := []string{info.Instances[0].ID, info.Instances[1].ID}
	t0 := time.Now()
	m, err := s.Metrics(ctx, q, ids)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("two containers sampled in %s, window %s", time.Since(t0), m.Window)
	var cpu, mem float64
	for _, id := range ids {
		u, ok := m.Values[id]
		if !ok || u.CPU == nil || u.Memory == nil || *u.CPU < 0 || *u.CPU > 16 || *u.Memory <= 0 {
			t.Fatalf("usage of %s: %+v", id, u)
		}
		cpu += *u.CPU
		mem += *u.Memory
	}
	sq := provider.Query{Kind: KindServices, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	sm, err := s.Metrics(ctx, sq, []string{"ocular-fixture/logger"})
	if err != nil {
		t.Fatal(err)
	}
	u := sm.Values["ocular-fixture/logger"]
	if u.CPU == nil || u.Memory == nil || u.CPUPartial || u.MemoryPartial {
		t.Fatalf("service usage: %+v", u)
	}
	t.Logf("members: %.4f cores, %.0f bytes; service: %.4f cores, %.0f bytes", cpu, mem, *u.CPU, *u.Memory)
	// another second: the sum is of the same order, not equal
	if *u.Memory < mem/2 || *u.Memory > mem*2 {
		t.Errorf("service memory %v vs members %v", *u.Memory, mem)
	}
}
