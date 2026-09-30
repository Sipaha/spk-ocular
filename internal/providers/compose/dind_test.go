package compose

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// Real-daemon tests on the isolated test daemon (make test-dind): its
// endpoint comes in OCULAR_DIND_HOST, and every mutation is preceded by
// OCULAR_DIND_VERIFY (scripts/dind-verify.sh), which refuses unless the
// endpoint is served by the recorded ocular-dind container.

func dindHost(t *testing.T) string {
	t.Helper()
	h := os.Getenv("OCULAR_DIND_HOST")
	if h == "" {
		t.Skip("OCULAR_DIND_HOST not set (make test-dind)")
	}
	return h
}

// dindVerify proves the endpoint is still the test daemon.
func dindVerify(t *testing.T, host string) {
	t.Helper()
	script := os.Getenv("OCULAR_DIND_VERIFY")
	if script == "" {
		t.Fatal("OCULAR_DIND_VERIFY not set: refusing to change a daemon that is not proven to be the test one")
	}
	out, err := exec.Command("bash", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != host {
		t.Fatalf("the test daemon check failed (%v): %s", err, out)
	}
}

// dindDocker runs the docker CLI against the test daemon after the check.
func dindDocker(t *testing.T, host string, args ...string) string {
	t.Helper()
	dindVerify(t, host)
	cmd := exec.Command("docker", append([]string{"-H", host}, args...)...)
	cmd.Env = noProxyEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func noProxyEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "DOCKER_HOST", "DOCKER_CONTEXT":
			continue
		}
		env = append(env, kv)
	}
	return env
}

func dindSession(t *testing.T, host string) *session {
	t.Helper()
	cl, err := engine.New(engine.Config{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession("context:dind", "h", cl)
	t.Cleanup(s.Close)
	return s
}

// dindProject writes a compose file for a project of the test's own and
// removes the project (volumes too) when the test ends.
func dindProject(t *testing.T, host, name, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte("name: "+name+"\n"+yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dindDocker(t, host, "compose", "-f", file, "down", "-v", "--remove-orphans", "-t", "0")
	})
	return file
}

const dindApp = `services:
  web:
    image: busybox:latest
    command: ["sleep", "infinity"]
    deploy: {replicas: 2}
  job:
    image: busybox:latest
    command: ["true"]
`

// compose up / restart / down, seen live by the services and containers
// views of the project and by the projects view.
func TestDindComposeLifecycle(t *testing.T) {
	host := dindHost(t)
	s := dindSession(t, host)
	name := fmt.Sprintf("ocular-t%d", time.Now().UnixNano()%1_000_000)
	file := dindProject(t, host, name, dindApp)
	one := core.ScopeSel{Mode: core.ScopeOne, Name: name}

	services, containers, projects := newSink(), newSink(), newSink()
	for _, v := range []struct {
		q  provider.Query
		sk *sink
	}{
		{provider.Query{Kind: KindServices, Scope: one}, services},
		{provider.Query{Kind: KindContainers, Scope: one}, containers},
		{provider.Query{Kind: KindProjects, Scope: core.ScopeSel{Mode: core.ScopeAll}}, projects},
	} {
		stop, err := s.Watch(v.q, v.sk)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stop)
		v.sk.waitFor(t, "ready", ready)
	}
	if n := services.count(); n != 0 {
		t.Fatalf("the project has %d services before up", n)
	}

	dindDocker(t, host, "compose", "-f", file, "up", "-d")
	services.waitFor(t, "web running, job completed", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		web, job := rows[name+"/web"], rows[name+"/job"]
		return st.State == provider.StatusReady && web.Health.State == core.HealthOK && len(web.Cells) > 2 && web.Cells[2].Text == "2/2" &&
			job.Health.Reason == "Completed"
	})
	containers.waitFor(t, "three containers", func(rows map[string]core.Row, _ provider.ViewStatus) bool { return len(rows) == 3 })
	projects.waitFor(t, "the project", func(rows map[string]core.Row, _ provider.ViewStatus) bool { _, ok := rows[name]; return ok })
	ids := map[string]bool{}
	containers.mu.Lock()
	for id := range containers.rows {
		ids[id] = true
	}
	containers.mu.Unlock()

	dindDocker(t, host, "compose", "-f", file, "restart", "-t", "0", "web")
	containers.waitFor(t, "the same containers after restart", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		if len(rows) != 3 {
			return false
		}
		for id, r := range rows {
			if !ids[id] {
				return false
			}
			if r.Ref.Title != name+"-job-1" && r.Health.State != core.HealthOK && r.Health.Reason != "RestartedByPolicy" {
				return false
			}
		}
		return true
	})

	dindDocker(t, host, "compose", "-f", file, "down", "-t", "0")
	services.waitFor(t, "no services", func(rows map[string]core.Row, _ provider.ViewStatus) bool { return len(rows) == 0 })
	containers.waitFor(t, "no containers", func(rows map[string]core.Row, _ provider.ViewStatus) bool { return len(rows) == 0 })
	projects.waitFor(t, "no project", func(rows map[string]core.Row, _ provider.ViewStatus) bool { _, ok := rows[name]; return !ok })
}

// The daemon stops: the views keep their rows and turn stale; it comes
// back: ready again, with what changed meanwhile.
func TestDindDaemonStopStaleRecovery(t *testing.T) {
	host := dindHost(t)
	s := dindSession(t, host)
	s.staleAfter = time.Second
	sk := newSink()
	stop, err := s.Watch(provider.Query{Kind: KindServices, Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-other"}}, sk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	sk.waitFor(t, "ocular-other/idle", hasRows("ocular-other/idle"))

	id := dindContainerID(t)
	dindVerify(t, host)
	userDocker(t, "stop", "-t", "2", id)
	t.Cleanup(func() {
		// Whatever happened, leave the test daemon up and seeded.
		userDocker(t, "start", id)
		waitPing(t, host)
		reseed(t)
	})
	sk.waitWithin(t, 15*time.Second, "stale with the rows", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		return st.State == provider.StatusStale && len(rows) == 1
	})
	userDocker(t, "start", id)
	waitPing(t, host)
	// The daemon's own restart: containers without a restart policy stay
	// exited, which the view must now show.
	// The feed's backoff reaches 8 s while the daemon is away.
	sk.waitWithin(t, 30*time.Second, "ready again", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		r, ok := rows["ocular-other/idle"]
		return st.State == provider.StatusReady && ok && r.Health.State == core.HealthError
	})
}

// dindContainerID is the recorded test daemon container (build/ocular-dind.id).
func dindContainerID(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "build", "ocular-dind.id"))
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		t.Fatalf("no recorded test daemon: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// userDocker changes the recorded dind container itself (stop/start) on
// the user's daemon — nothing else there is ever touched.
func userDocker(t *testing.T, verb, arg string, rest ...string) {
	t.Helper()
	args := append([]string{verb, arg}, rest...)
	id := args[len(args)-1]
	if id != dindContainerID(t) {
		t.Fatalf("refusing docker %v: not the recorded test daemon", args)
	}
	cmd := exec.Command("docker", args...)
	cmd.Env = noProxyEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
}

func waitPing(t *testing.T, host string) {
	t.Helper()
	cl, err := engine.New(engine.Config{Host: host, RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	deadline := time.Now().Add(60 * time.Second)
	for {
		// not t.Context(): cleanups run after it is canceled
		if _, err := cl.Ping(context.Background()); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the test daemon did not come back")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func reseed(t *testing.T) {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "dind-seed.sh"))
	cmd.Env = noProxyEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("re-seeding the test daemon: %v\n%s", err, out)
	}
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// A service's logs on the real daemon: two replicas, stdout and stderr,
// through a restart of the service — no line delivered twice (each line
// has its own timestamp), and lines after the restart arrive.
func TestDindServiceLogsThroughRestart(t *testing.T) {
	host := dindHost(t)
	s := dindSession(t, host)
	name := fmt.Sprintf("ocular-l%d", time.Now().UnixNano()%1_000_000)
	file := dindProject(t, host, name, `services:
  talk:
    image: busybox:latest
    command: ["sh", "-c", "i=0; while true; do i=$$((i+1)); echo out $$i; echo err $$i >&2; sleep 0.2; done"]
    deploy: {replicas: 2}
`)
	dindDocker(t, host, "compose", "-f", file, "up", "-d")
	ref := core.Ref{Provider: ProviderID, Kind: KindServices, Scope: name, Name: name + "/talk"}
	// the service must be observed before its logs are asked for
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := s.LogInfo(t.Context(), ref); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("LogInfo: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	sk := newLogSink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.StreamLogs(ctx, ref, provider.LogQuery{Follow: true, TailLines: 50}, sk) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	r1, r2 := name+"-talk-1", name+"-talk-2"
	sk.waitFor(t, "both replicas, both streams", func() bool {
		return sk.isReady() && len(sk.texts(r1)) > 2 && len(sk.texts(r2)) > 2 && len(sk.texts(r1+" (stderr)")) > 2
	})
	before := len(sk.texts(r1))
	dindDocker(t, host, "compose", "-f", file, "restart", "-t", "1", "talk")
	deadline = time.Now().Add(20 * time.Second)
	for {
		// after the restart the counter starts over: "out 1" again
		texts := sk.texts(r1)
		if len(texts) > before+5 && strings.Contains(strings.Join(texts[before:], "\n"), "out 2") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no lines after the restart: %d before, %d now", before, len(texts))
		}
		time.Sleep(100 * time.Millisecond)
	}
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for id, ls := range sk.lines {
		seen := map[string]bool{}
		for _, l := range ls {
			k := l.TS + " " + l.Text
			if seen[k] {
				t.Fatalf("source %s delivered %q twice", sk.sources[id], k)
			}
			seen[k] = true
		}
	}
}
