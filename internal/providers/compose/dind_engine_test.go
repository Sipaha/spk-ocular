package compose

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"

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
