package compose

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

// logSink records a log stream.
type logSink struct {
	mu      sync.Mutex
	sources map[int]string // id → label
	keys    map[int]string
	lines   map[int][]provider.LogLine
	states  map[int][]provider.LogState
	ready   bool
	block   chan struct{} // non-nil: Lines blocks until closed or ctx
	seq     []string      // "<label>: <text>" in delivery order
	ch      chan struct{}
}

func newLogSink() *logSink {
	return &logSink{sources: map[int]string{}, keys: map[int]string{}, lines: map[int][]provider.LogLine{}, states: map[int][]provider.LogState{}, ch: make(chan struct{}, 1)}
}

func (s *logSink) poke() {
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

func (s *logSink) Source(id int, key, label, _ string) error {
	s.mu.Lock()
	s.sources[id], s.keys[id] = label, key
	s.mu.Unlock()
	s.poke()
	return nil
}

func (s *logSink) Lines(id int, ls []provider.LogLine) error {
	s.mu.Lock()
	if _, ok := s.sources[id]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("lines of an unannounced source %d", id)
	}
	block := s.block
	s.lines[id] = append(s.lines[id], ls...)
	for _, l := range ls {
		s.seq = append(s.seq, s.sources[id]+": "+l.Text)
	}
	s.mu.Unlock()
	s.poke()
	if block != nil {
		<-block
	}
	return nil
}

func (s *logSink) State(id int, st provider.LogState) error {
	s.mu.Lock()
	if _, ok := s.sources[id]; !ok && id != 0 {
		s.mu.Unlock()
		return fmt.Errorf("a state of an unannounced source %d", id)
	}
	s.states[id] = append(s.states[id], st)
	s.mu.Unlock()
	s.poke()
	return nil
}

func (s *logSink) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *logSink) Ready() error {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	s.poke()
	return nil
}

// texts are the lines of the source labelled label.
func (s *logSink) texts(label string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, l := range s.sources {
		if l == label {
			for _, line := range s.lines[id] {
				out = append(out, line.Text)
			}
		}
	}
	return out
}

func (s *logSink) lastState(label string) provider.LogState {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, l := range s.sources {
		if l == label && len(s.states[id]) > 0 {
			return s.states[id][len(s.states[id])-1]
		}
	}
	return provider.LogState{}
}

func (s *logSink) hadState(label, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, l := range s.sources {
		if l == label {
			for _, st := range s.states[id] {
				if st.State == state {
					return true
				}
			}
		}
	}
	return false
}

func (s *logSink) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case <-s.ch:
		case <-deadline:
			s.mu.Lock()
			t.Fatalf("timed out waiting for %s; sources %v, lines %v, states %v", what, s.sources, s.lines, s.states)
			s.mu.Unlock()
		}
	}
}

func eq(a []string, b ...string) bool {
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}

// logEnv is a fake Engine with one running Compose container "p-web-1".
type logEnv struct {
	*testEnv
	c  engine.ContainerInspect
	t0 time.Time
}

func newLogEnv(t *testing.T) *logEnv {
	e := newTestEnv(t)
	c := composeContainer(id(1), "p", "web", "running")
	c.Name = "/p-web-1"
	c.HostConfig.LogConfig.Type = "json-file"
	e.fe.PutContainer(c)
	return &logEnv{testEnv: e, c: c, t0: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)}
}

func (e *logEnv) at(sec int) time.Time { return e.t0.Add(time.Duration(sec) * time.Second) }

// stream runs StreamLogs in the background; it returns the sink and a
// stop that waits for StreamLogs to return and gives its error.
func (e *logEnv) stream(ref core.Ref, q provider.LogQuery) (*logSink, func() error) {
	sk := newLogSink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.s.StreamLogs(ctx, ref, q, sk) }()
	var once sync.Once
	var err error
	stop := func() error {
		once.Do(func() {
			cancel()
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				err = errors.New("StreamLogs did not return after cancel")
			}
		})
		return err
	}
	e.t.Cleanup(func() { _ = stop() })
	return sk, stop
}

func (e *logEnv) containerRef() core.Ref {
	return core.Ref{Provider: ProviderID, Kind: KindContainers, Scope: "p", Name: e.c.ID, UID: e.c.ID}
}

func follow() provider.LogQuery { return provider.LogQuery{Follow: true, TailLines: 100} }

func TestLogsContainerBacklogThenLive(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "out 1")
	e.fe.Journal(e.c.ID, 2, e.at(2), "err 1")
	e.fe.Journal(e.c.ID, 1, e.at(3), "out 2")
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool {
		return sk.isReady() && eq(sk.texts("p-web-1"), "out 1", "out 2") && eq(sk.texts("p-web-1 (stderr)"), "err 1")
	})
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 2, e.at(4), "err 2")
	e.fe.Journal(e.c.ID, 1, e.at(5), "out 3")
	sk.waitFor(t, "live lines", func() bool {
		return eq(sk.texts("p-web-1"), "out 1", "out 2", "out 3") && eq(sk.texts("p-web-1 (stderr)"), "err 1", "err 2")
	})
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for id, ls := range sk.lines {
		for _, l := range ls {
			if l.TS == "" {
				t.Fatalf("source %d line %q without a timestamp", id, l.Text)
			}
		}
	}
}

// One channel: only that stream is asked for and shown.
func TestLogsChannelStderrOnly(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "out 1")
	e.fe.Journal(e.c.ID, 2, e.at(2), "err 1")
	q := follow()
	q.Channel = channelStderr
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "stderr alone", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "err 1") })
	for _, r := range e.fe.Requests() {
		if strings.HasSuffix(r.Path, "/logs") && r.Query.Get("stdout") == "1" {
			t.Fatalf("stdout was asked for: %v", r.Query)
		}
	}
}

// The container stops (the follow ends) and starts again: its log goes on
// after what was delivered — no line twice, none lost, equal timestamps
// included.
func TestLogsStopStartContinuesWithoutRepeats(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	e.fe.Journal(e.c.ID, 1, e.at(2), "b1")
	e.fe.Journal(e.c.ID, 2, e.at(2), "b2") // same time, other stream
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == 2 })
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 1, e.at(2), "b3") // live, still the same time

	// stop
	e.c.State = engine.ContainerState{Status: "exited", ExitCode: 137}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	e.fe.Emit(containerEvent("die", e.c))
	sk.waitFor(t, "waiting", func() bool { return sk.lastState("p-web-1").State == provider.LogWaiting })
	if st := sk.lastState("p-web-1"); !strings.Contains(st.Message, "code 137") {
		t.Fatalf("waiting says %q", st.Message)
	}
	// start: new lines, one with the cursor's time
	e.fe.Journal(e.c.ID, 1, e.at(2), "b4")
	e.fe.Journal(e.c.ID, 1, e.at(3), "c")
	e.c.State = engine.ContainerState{Status: "running", Running: true}
	e.fe.PutContainer(e.c)
	e.fe.ResumeLogs(e.c.ID)
	e.fe.Emit(containerEvent("start", e.c))
	sk.waitFor(t, "the continuation", func() bool {
		return eq(sk.texts("p-web-1"), "a", "b1", "b3", "b4", "c") && eq(sk.texts("p-web-1 (stderr)"), "b2")
	})
	if sk.hadState("p-web-1", provider.LogGap) {
		t.Fatal("a clean resume reported a gap")
	}
	// the resume asked from the cursor, not the whole journal
	var last enginefake.Request
	for _, r := range e.fe.Requests() {
		if strings.HasSuffix(r.Path, "/logs") {
			last = r
		}
	}
	if last.Query.Get("since") == "" || last.Query.Get("tail") != "all" {
		t.Fatalf("the resume request: %v", last.Query)
	}
}

// The journal was rotated while the container was stopped: the replay does
// not match what was delivered — a gap, then what is there.
func TestLogsRotationIsAGap(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	e.fe.Journal(e.c.ID, 1, e.at(2), "b")
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == 2 })
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	e.fe.Emit(containerEvent("die", e.c))
	sk.waitFor(t, "waiting", func() bool { return sk.lastState("p-web-1").State == provider.LogWaiting })
	e.fe.Journal(e.c.ID, 1, e.at(5), "c")
	e.fe.RotateJournal(e.c.ID, e.at(3)) // "b" and before are gone
	e.c.State = engine.ContainerState{Status: "running", Running: true}
	e.fe.PutContainer(e.c)
	e.fe.ResumeLogs(e.c.ID)
	e.fe.Emit(containerEvent("start", e.c))
	sk.waitFor(t, "c after a gap", func() bool {
		return eq(sk.texts("p-web-1"), "a", "b", "c") && sk.hadState("p-web-1", provider.LogGap)
	})
}

// A removed container's logs end; the stream is over.
func TestLogsContainerRemovedEnds(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	sk, stop := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == 1 })
	e.fe.RemoveContainer(e.c.ID)
	e.fe.EndLogs(e.c.ID)
	e.fe.Emit(containerEvent("destroy", e.c))
	sk.waitFor(t, "ended", func() bool { return sk.lastState("p-web-1").State == provider.LogEnded })
	if err := stop(); err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}
}

// A service's logs: members' backlogs merged by time; a member created
// later joins from its start; a removed one ends while the rest go on.
func TestLogsServiceMembersComeAndGo(t *testing.T) {
	e := newLogEnv(t)
	c2 := composeContainer(id(2), "p", "web", "running")
	c2.Name, c2.Config.Labels[LabelNumber] = "/p-web-2", "2"
	e.fe.PutContainer(c2)
	oneoff := composeContainer(id(3), "p", "web", "running")
	oneoff.Name, oneoff.Config.Labels[LabelOneoff] = "/p-web-run-1", "True"
	e.fe.PutContainer(oneoff)
	e.fe.Journal(e.c.ID, 1, e.at(1), "one 1")
	e.fe.Journal(c2.ID, 1, e.at(2), "two 1")
	e.fe.Journal(e.c.ID, 1, e.at(3), "one 2")
	e.fe.Journal(oneoff.ID, 1, e.at(1), "not a member")
	ref := core.Ref{Provider: ProviderID, Kind: KindServices, Scope: "p", Name: "p/web", UID: "p/web"}
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(ref, q)
	sk.waitFor(t, "both members", func() bool {
		return sk.isReady() && eq(sk.texts("p-web-1"), "one 1", "one 2") && eq(sk.texts("p-web-2"), "two 1")
	})
	sk.mu.Lock()
	var order []string
	for id := 1; id <= 2; id++ {
		order = append(order, sk.sources[id])
	}
	sk.mu.Unlock()
	if !eq(order, "p-web-1", "p-web-2") || len(sk.texts("p-web-run-1")) != 0 {
		t.Fatalf("sources %v: members in replica order, no one-off", order)
	}

	c4 := composeContainer(id(4), "p", "web", "running")
	c4.Name, c4.Config.Labels[LabelNumber] = "/p-web-3", "3"
	e.fe.PutContainer(c4)
	e.fe.Journal(c4.ID, 1, e.at(10), "three 1")
	e.fe.Emit(containerEvent("create", c4))
	sk.waitFor(t, "the new member from its start", func() bool { return eq(sk.texts("p-web-3"), "three 1") })

	e.fe.RemoveContainer(c2.ID)
	e.fe.EndLogs(c2.ID)
	e.fe.Emit(containerEvent("destroy", c2))
	sk.waitFor(t, "the removed member ended", func() bool { return sk.lastState("p-web-2").State == provider.LogEnded })
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 1, e.at(11), "one 3")
	sk.waitFor(t, "the others go on", func() bool { return eq(sk.texts("p-web-1"), "one 1", "one 2", "one 3") })
}

// A member created while the backlog is read is not missed.
func TestLogsNewMemberDuringBacklog(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "one 1")
	release := make(chan struct{})
	var once sync.Once
	e.fe.AddHook(func(_ http.ResponseWriter, r *http.Request, path string) bool {
		if path == "/containers/"+e.c.ID+"/logs" && r.URL.Query().Get("follow") != "1" {
			once.Do(func() { <-release })
		}
		return false
	})
	ref := core.Ref{Provider: ProviderID, Kind: KindServices, Scope: "p", Name: "p/web"}
	sk, _ := e.stream(ref, follow())
	for e.fe.Count("/containers/"+e.c.ID+"/logs") == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	c2 := composeContainer(id(2), "p", "web", "running")
	c2.Name = "/p-web-2"
	e.fe.PutContainer(c2)
	e.fe.Journal(c2.ID, 1, e.at(5), "two 1")
	e.fe.Emit(containerEvent("create", c2))
	time.Sleep(50 * time.Millisecond)
	close(release)
	sk.waitFor(t, "both", func() bool {
		return sk.isReady() && eq(sk.texts("p-web-1"), "one 1") && eq(sk.texts("p-web-2"), "two 1")
	})
}

// TTY and non-TTY members together: the TTY member has one source with
// everything; the others have stdout and stderr.
func TestLogsMixedTTYMembers(t *testing.T) {
	e := newLogEnv(t)
	c2 := composeContainer(id(2), "p", "web", "running")
	c2.Name, c2.Config.Tty = "/p-web-2", true
	e.fe.PutContainer(c2)
	e.fe.Journal(e.c.ID, 1, e.at(1), "plain out")
	e.fe.Journal(e.c.ID, 2, e.at(2), "plain err")
	e.fe.Journal(c2.ID, 1, e.at(3), "tty out")
	ref := core.Ref{Provider: ProviderID, Kind: KindServices, Scope: "p", Name: "p/web"}
	info, err := e.s.LogInfo(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Aggregate || info.DefaultChannel != provider.ChannelAll || len(info.Channels) != 2 {
		t.Fatalf("mixed service info %+v", info)
	}
	sk, _ := e.stream(ref, follow())
	sk.waitFor(t, "all sources", func() bool {
		return sk.isReady() && eq(sk.texts("p-web-1 (stderr)"), "plain err") && eq(sk.texts("p-web-1"), "plain out") && eq(sk.texts("p-web-2 (tty)"), "tty out")
	})
}

func TestLogsInfo(t *testing.T) {
	e := newLogEnv(t)
	info, err := e.s.LogInfo(t.Context(), e.containerRef())
	if err != nil {
		t.Fatal(err)
	}
	if info.Aggregate || info.Previous || info.DefaultChannel != provider.ChannelAll || len(info.Channels) != 2 || info.ChannelLabel == nil || info.ChannelLabel.Key != "compose.logs.stream" {
		t.Fatalf("container info %+v", info)
	}
	tty := composeContainer(id(5), "p", "shell", "running")
	tty.Config.Tty = true
	e.fe.PutContainer(tty)
	info, err = e.s.LogInfo(t.Context(), core.Ref{Kind: KindContainers, Name: tty.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Channels) != 1 || info.DefaultChannel != channelStdout {
		t.Fatalf("tty info %+v", info)
	}
	if _, err := e.s.LogInfo(t.Context(), core.Ref{Kind: KindContainers, Name: e.c.ID, UID: "other"}); !isClass(err, provider.ClassGone) {
		t.Fatalf("another incarnation: %v", err)
	}
	if _, err := e.s.LogInfo(t.Context(), core.Ref{Kind: KindServices, Name: "p/none"}); !isClass(err, provider.ClassNotFound) {
		t.Fatalf("a service without containers: %v", err)
	}
	if _, err := e.s.LogInfo(t.Context(), core.Ref{Kind: KindNetworks, Name: "n"}); !isClass(err, provider.ClassUnsupported) {
		t.Fatalf("networks: %v", err)
	}
}

func isClass(err error, c provider.ErrorClass) bool {
	var pe *provider.Error
	return errors.As(err, &pe) && pe.Class == c
}

func TestLogsRefusals(t *testing.T) {
	e := newLogEnv(t)
	sk := newLogSink()
	if err := e.s.StreamLogs(t.Context(), e.containerRef(), provider.LogQuery{Previous: true, TailLines: 10}, sk); !isClass(err, provider.ClassUnsupported) {
		t.Fatalf("previous: %v", err)
	}
	if err := e.s.StreamLogs(t.Context(), e.containerRef(), provider.LogQuery{Channel: "stdin", TailLines: 10}, sk); !isClass(err, provider.ClassInvalid) {
		t.Fatalf("a bad channel: %v", err)
	}
	for _, ref := range []core.Ref{
		{Provider: ProviderID, Kind: KindContainers, Scope: "q", Name: e.c.ID},
		{Provider: ProviderID, Kind: KindServices, Scope: "q", Name: "p/web"},
	} {
		if err := e.s.StreamLogs(t.Context(), ref, provider.LogQuery{TailLines: 10}, sk); !isClass(err, provider.ClassNotFound) {
			t.Fatalf("another project's %s: %v", ref.Kind, err)
		}
		if _, err := e.s.LogInfo(t.Context(), ref); !isClass(err, provider.ClassNotFound) {
			t.Fatalf("info of another project's %s: %v", ref.Kind, err)
		}
	}
	gone := e.containerRef()
	gone.Name = id(9)
	if err := e.s.StreamLogs(t.Context(), gone, follow(), sk); !isClass(err, provider.ClassNotFound) {
		t.Fatalf("no such container: %v", err)
	}
}

// Without follow the answer is the window, then ended.
func TestLogsOnce(t *testing.T) {
	e := newLogEnv(t)
	for i := 1; i <= 5; i++ {
		e.fe.Journal(e.c.ID, 1, e.at(i), fmt.Sprintf("l%d", i))
	}
	sk := newLogSink()
	if err := e.s.StreamLogs(t.Context(), e.containerRef(), provider.LogQuery{TailLines: 2, Channel: channelStdout}, sk); err != nil {
		t.Fatal(err)
	}
	if !sk.isReady() || !eq(sk.texts("p-web-1"), "l4", "l5") || sk.lastState("p-web-1").State != provider.LogEnded {
		t.Fatalf("once: %v %v", sk.texts("p-web-1"), sk.lastState("p-web-1"))
	}
}

// The log driver none keeps nothing: said, not waited on forever.
func TestLogsDriverNone(t *testing.T) {
	e := newLogEnv(t)
	e.c.HostConfig.LogConfig.Type = "none"
	e.fe.PutContainer(e.c)
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the driver error", func() bool {
		st := sk.lastState("p-web-1")
		return st.State == provider.LogError && st.Class == provider.ClassUnsupported
	})
}

// Closing a stream whose page does not read (Lines blocks) returns and
// closes the Engine requests.
func TestLogsCloseUnderBackpressure(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	sk, stop := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() })
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	block := make(chan struct{})
	sk.mu.Lock()
	sk.block = block
	sk.mu.Unlock()
	for i := 0; i < 50; i++ {
		e.fe.Journal(e.c.ID, 1, e.at(2+i), "flood")
	}
	sk.waitFor(t, "a blocked delivery", func() bool { return len(sk.texts("p-web-1")) > 1 })
	errc := make(chan error, 1)
	go func() { errc <- stop() }()
	// The sink is blocked: StreamLogs cannot return before it is released
	// (Lines is synchronous); releasing lets it see the cancel.
	time.Sleep(20 * time.Millisecond)
	close(block)
	if err := <-errc; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("StreamLogs after cancel: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.fe.LogFollowers(e.c.ID) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the Engine request stayed open")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st := e.s.Stats(); st["feeds_active"] != 0 {
		t.Fatalf("the logs left a lease: %v", st)
	}
}

// Reading the containers again (Resync) does not look like members
// leaving: the service's logs go on.
func TestLogsSurviveAResync(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	ref := core.Ref{Provider: ProviderID, Kind: KindServices, Scope: "p", Name: "p/web"}
	sk, _ := e.stream(ref, follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == 1 })
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	lists := e.fe.Count("/containers/json")
	if err := e.s.Resync(provider.Query{Kind: KindServices, Scope: core.ScopeSel{Mode: core.ScopeAll}}); err != nil {
		t.Fatal(err)
	}
	for e.fe.Count("/containers/json") == lists {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 1, e.at(2), "b")
	sk.waitFor(t, "the next line", func() bool { return eq(sk.texts("p-web-1"), "a", "b") })
	if sk.hadState("p-web-1", provider.LogEnded) {
		t.Fatal("the member ended during the new reading")
	}
}

// logsHook answers the n-th logs request of the container (1-based,
// counting follow ones when follow) with raw bytes, then ends it (or holds
// it open when hold).
func logsHook(cid string, follow bool, n int, body []byte, hold bool) enginefake.Hook {
	var mu sync.Mutex
	seen := 0
	return func(w http.ResponseWriter, r *http.Request, path string) bool {
		if path != "/containers/"+cid+"/logs" || (r.URL.Query().Get("follow") == "1") != follow {
			return false
		}
		mu.Lock()
		seen++
		mine := seen == n
		mu.Unlock()
		if !mine {
			return false
		}
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		w.(http.Flusher).Flush()
		if hold {
			<-r.Context().Done()
		}
		return true
	}
}

func stamped(t time.Time, text string) []byte {
	return []byte(t.UTC().Format(time.RFC3339Nano) + " " + text)
}

// Review P1: a follow answer cut inside a frame leaves a damaged prefix:
// it is not delivered and does not move the cursor; the reconnect brings
// the whole line (with a gap said).
func TestLogsFrameCutDoesNotSkipTheWholeLine(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	e.fe.Journal(e.c.ID, 1, e.at(2), "the whole second line")
	e.fe.Journal(e.c.ID, 1, e.at(3), "c")
	whole := enginefake.Frame(1, append(stamped(e.at(2), "the whole second line"), '\n'))
	cut := append(enginefake.Frame(1, append(stamped(e.at(1), "a"), '\n')), whole[:8+25]...)
	e.fe.AddHook(logsHook(e.c.ID, false, 1, enginefake.Frame(1, append(stamped(e.at(1), "a"), '\n')), false)) // the backlog: "a"
	e.fe.AddHook(logsHook(e.c.ID, true, 1, cut, false))
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the whole line after the cut", func() bool {
		return eq(sk.texts("p-web-1"), "a", "the whole second line", "c")
	})
	if !sk.hadState("p-web-1", provider.LogGap) {
		t.Fatal("the cut frame was not said")
	}
}

// Review P2: a finite read keeps a clean last line without a newline.
func TestLogsOnceKeepsACleanLastLineWithoutNewline(t *testing.T) {
	e := newLogEnv(t)
	e.fe.SetLogs(e.c.ID, append(enginefake.Frame(1, append(stamped(e.at(1), "first"), '\n')), enginefake.Frame(1, stamped(e.at(2), "last-message"))...))
	sk := newLogSink()
	if err := e.s.StreamLogs(t.Context(), e.containerRef(), provider.LogQuery{TailLines: 10, Channel: channelStdout}, sk); err != nil {
		t.Fatal(err)
	}
	if !eq(sk.texts("p-web-1"), "first", "last-message") {
		t.Fatalf("lines %v", sk.texts("p-web-1"))
	}
}

// Review P2: the daemon's error inside a follow stream is said, not shown
// as streaming.
func TestLogsDaemonErrorInTheStreamIsSaid(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	body := append(enginefake.Frame(1, append(stamped(e.at(1), "a"), '\n')), enginefake.Frame(3, []byte("the log file is corrupt"))...)
	e.fe.AddHook(logsHook(e.c.ID, true, 1, body, false))
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the daemon's error", func() bool {
		return sk.hadState("p-web-1", provider.LogError)
	})
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for _, sts := range sk.states {
		for _, st := range sts {
			if st.State == provider.LogError && !strings.Contains(st.Message, "corrupt") {
				t.Fatalf("error %q", st.Message)
			}
		}
	}
}

// Review P2: stdout and stderr lines reach the sink in arrival order.
func TestLogsFollowKeepsArrivalOrderAcrossStreams(t *testing.T) {
	e := newLogEnv(t)
	body := append(append(
		enginefake.Frame(2, append(stamped(e.at(1), "A"), '\n')),
		enginefake.Frame(1, append(stamped(e.at(2), "B"), '\n'))...),
		enginefake.Frame(2, append(stamped(e.at(3), "C"), '\n'))...)
	e.fe.AddHook(logsHook(e.c.ID, true, 1, body, true))
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "three lines", func() bool { sk.mu.Lock(); defer sk.mu.Unlock(); return len(sk.seq) == 3 })
	sk.mu.Lock()
	defer sk.mu.Unlock()
	if !eq(sk.seq, "p-web-1 (stderr): A", "p-web-1: B", "p-web-1 (stderr): C") {
		t.Fatalf("order %v", sk.seq)
	}
}

// Review P2: a complete line followed by the start of the next frame is
// delivered at once, not held until more bytes come.
func TestLogsCompleteLineBeforeAnIncompleteFrameIsDelivered(t *testing.T) {
	e := newLogEnv(t)
	next := enginefake.Frame(1, append(stamped(e.at(2), "later"), '\n'))
	body := append(enginefake.Frame(1, append(stamped(e.at(1), "now"), '\n')), next[:3]...)
	e.fe.AddHook(logsHook(e.c.ID, true, 1, body, true))
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the complete line", func() bool { return eq(sk.texts("p-web-1"), "now") })
}

// Docker stamps stdout and stderr apart: in the journal a stderr line may
// follow a later-stamped stdout line, and since answers positionally. The
// resume after a restart repeats nothing (real daemon: dind
// TestDindServiceLogsThroughRestart found it).
func TestLogsResumeWithStreamsStampedOutOfOrder(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "out 1")
	e.fe.Journal(e.c.ID, 1, e.at(3), "out 2")
	e.fe.Journal(e.c.ID, 2, e.at(2), "err 1") // stamped before out 2, written after it
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == 2 })
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	e.fe.Emit(containerEvent("die", e.c))
	sk.waitFor(t, "waiting", func() bool { return sk.lastState("p-web-1").State == provider.LogWaiting })
	e.fe.Journal(e.c.ID, 2, e.at(4), "err 2")
	e.c.State = engine.ContainerState{Status: "running", Running: true}
	e.fe.PutContainer(e.c)
	e.fe.ResumeLogs(e.c.ID)
	e.fe.Emit(containerEvent("start", e.c))
	sk.waitFor(t, "err 2", func() bool { return eq(sk.texts("p-web-1 (stderr)"), "err 1", "err 2") })
	if !eq(sk.texts("p-web-1"), "out 1", "out 2") || sk.hadState("p-web-1", provider.LogGap) {
		t.Fatalf("stdout %v, gap %v", sk.texts("p-web-1"), sk.hadState("p-web-1", provider.LogGap))
	}
}

// stopContainer: the container exits and its follows end; the sink shows
// the member waiting.
func (e *logEnv) stopContainer(sk *logSink, label string) {
	e.t.Helper()
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	e.fe.Emit(containerEvent("die", e.c))
	sk.waitFor(e.t, "waiting", func() bool { return sk.lastState(label).State == provider.LogWaiting })
}

func (e *logEnv) startContainer() {
	e.c.State = engine.ContainerState{Status: "running", Running: true}
	e.fe.PutContainer(e.c)
	e.fe.ResumeLogs(e.c.ID)
	e.fe.Emit(containerEvent("start", e.c))
}

// Re-review P2: the container stopped mid-line ("A" was delivered as its
// last line); after the restart the journal continues that line, and the
// replay reads it joined ("AB") with A's time. What follows A is new.
func TestLogsPartialLastLineContinuesAfterRestart(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "first")
	e.fe.JournalPartial(e.c.ID, 1, e.at(2), "A")
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool {
		return sk.isReady() && eq(sk.texts("p-web-1"), "first", "A") && sk.lastState("p-web-1").State == provider.LogWaiting
	})
	e.fe.Journal(e.c.ID, 1, e.at(3), "B")
	e.fe.Journal(e.c.ID, 1, e.at(4), "C")
	e.startContainer()
	sk.waitFor(t, "the rest of the line", func() bool { return eq(sk.texts("p-web-1"), "first", "A", "B", "C") })
	if sk.hadState("p-web-1", provider.LogGap) {
		t.Fatal("a clean continuation reported a gap")
	}
}

// Re-review P2: a stdout line began (t1), a stderr line was delivered (t2),
// then the stream was cut inside the stdout line's next frame. The resume
// must read from the line's start, not from the latest delivered time.
func TestLogsCutInsideAnInterleavedLineResumesFromItsStart(t *testing.T) {
	e := newLogEnv(t)
	e.fe.JournalPartial(e.c.ID, 1, e.at(1), "A")
	e.fe.Journal(e.c.ID, 2, e.at(2), "E")
	e.fe.Journal(e.c.ID, 1, e.at(3), "B")
	cont := enginefake.Frame(1, append(stamped(e.at(3), "B"), '\n'))
	body := append(append(enginefake.Frame(1, stamped(e.at(1), "A")), enginefake.Frame(2, append(stamped(e.at(2), "E"), '\n'))...), cont[:8+10]...)
	e.fe.AddHook(logsHook(e.c.ID, false, 1, nil, false)) // an empty backlog
	e.fe.AddHook(logsHook(e.c.ID, true, 1, body, false))
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the whole line", func() bool {
		return eq(sk.texts("p-web-1"), "AB") && eq(sk.texts("p-web-1 (stderr)"), "E")
	})
}

// Re-review P2: the backlog window (tail 1) left out a line written before
// it but stamped later; the positional replay starts with that line. The
// delivered line is not repeated.
func TestLogsResumeAfterATailWindowRepeatsNothing(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(3), "A")
	e.fe.Journal(e.c.ID, 2, e.at(2), "B") // written after A, stamped before it
	q := follow()
	q.TailLines = 1
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1 (stderr)"), "B") })
	e.stopContainer(sk, "p-web-1 (stderr)")
	e.fe.Journal(e.c.ID, 2, e.at(4), "C")
	e.startContainer()
	sk.waitFor(t, "C", func() bool { return len(sk.texts("p-web-1 (stderr)")) >= 2 })
	if got := sk.texts("p-web-1 (stderr)"); !eq(got, "B", "C") {
		t.Fatalf("stderr %v", got)
	}
}

// Re-review P2: lines stamped alike, a tail window of the last one: the
// replay's first line has the cursor's time but is not the delivered one.
func TestLogsResumeAfterATailWindowOfEqualStampsRepeatsNothing(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(2), "A")
	e.fe.Journal(e.c.ID, 1, e.at(2), "B")
	q := follow()
	q.TailLines = 1
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "B") })
	e.stopContainer(sk, "p-web-1")
	e.fe.Journal(e.c.ID, 1, e.at(3), "C")
	e.startContainer()
	sk.waitFor(t, "C", func() bool {
		ts := sk.texts("p-web-1")
		return len(ts) > 0 && ts[len(ts)-1] == "C"
	})
	n := 0
	for _, s := range sk.texts("p-web-1") {
		if s == "B" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("B delivered %d times: %v", n, sk.texts("p-web-1"))
	}
}

// Re-review P2: one channel (stderr); the journal has a later-stamped
// stdout line before the stderr ones. The replay repeats nothing.
func TestLogsResumeOfOneChannelRepeatsNothing(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(3), "out")
	e.fe.Journal(e.c.ID, 2, e.at(1), "e1")
	e.fe.Journal(e.c.ID, 2, e.at(2), "e2")
	q := follow()
	q.Channel = channelStderr
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "e1", "e2") })
	e.stopContainer(sk, "p-web-1")
	e.fe.Journal(e.c.ID, 2, e.at(4), "e3")
	e.startContainer()
	sk.waitFor(t, "e3", func() bool { return len(sk.texts("p-web-1")) >= 3 })
	if got := sk.texts("p-web-1"); !eq(got, "e1", "e2", "e3") || sk.hadState("p-web-1", provider.LogGap) {
		t.Fatalf("stderr %v, gap %v", got, sk.hadState("p-web-1", provider.LogGap))
	}
}

// Re-review P2: a rotation cut between two lines stamped alike (A gone, B
// kept) and new lines came with the same stamp: none is lost.
func TestLogsRotationBetweenEqualStampsLosesNothing(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(2), "A")
	e.fe.Journal(e.c.ID, 1, e.at(2), "B")
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "A", "B") })
	e.stopContainer(sk, "p-web-1")
	e.fe.DropJournal(e.c.ID, 1)
	e.fe.Journal(e.c.ID, 1, e.at(2), "C")
	e.fe.Journal(e.c.ID, 1, e.at(3), "D")
	e.startContainer()
	sk.waitFor(t, "C and D", func() bool { return eq(sk.texts("p-web-1"), "A", "B", "C", "D") })
}

// Re-review P2: a read that failed on a stopped container is said and
// retried — the rest of its journal is still there to read; waiting for
// a start would hide both the error and the lines.
func TestLogsFailedReadOfAStoppedContainerIsRetried(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	e.fe.Journal(e.c.ID, 1, e.at(2), "b")
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	e.fe.AddHook(logsHook(e.c.ID, false, 1, enginefake.Frame(1, append(stamped(e.at(1), "a"), '\n')), false))
	e.fe.AddHook(logsHook(e.c.ID, true, 1, enginefake.Frame(3, []byte("the log file is corrupt")), false))
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the error", func() bool { return sk.hadState("p-web-1", provider.LogError) })
	if st := sk.lastState("p-web-1"); st.State != provider.LogError {
		t.Fatalf("the error was replaced by %v", st)
	}
	sk.waitFor(t, "the rest after a retry", func() bool { return eq(sk.texts("p-web-1"), "a", "b") })
}

// Re-review P2: a finite read's error stays the member's last state (not
// replaced by "ended").
func TestLogsOnceKeepsItsError(t *testing.T) {
	e := newLogEnv(t)
	e.fe.AddHook(logsHook(e.c.ID, false, 1, enginefake.Frame(3, []byte("the log file is corrupt")), false))
	sk := newLogSink()
	if err := e.s.StreamLogs(t.Context(), e.containerRef(), provider.LogQuery{TailLines: 10, Channel: channelStdout}, sk); err != nil {
		t.Fatal(err)
	}
	if st := sk.lastState("p-web-1"); st.State != provider.LogError || !strings.Contains(st.Message, "corrupt") {
		t.Fatalf("last state %v", st)
	}
}

// Re-review P2: the backlog budget counts each line's keeping, not only
// its text: many empty lines are bounded too (and said).
func TestLogsBacklogBudgetCountsEmptyLines(t *testing.T) {
	old := backlogBudget
	backlogBudget, backlogMinPerMember = 64<<10, 64<<10
	t.Cleanup(func() { backlogBudget, backlogMinPerMember = old, 512<<10 })
	e := newLogEnv(t)
	for i := range 5000 {
		e.fe.Journal(e.c.ID, 1, e.at(1).Add(time.Duration(i)), "")
	}
	q := provider.LogQuery{TailLines: provider.TailAll, Channel: channelStdout}
	sk := newLogSink()
	if err := e.s.StreamLogs(t.Context(), e.containerRef(), q, sk); err != nil {
		t.Fatal(err)
	}
	if n := len(sk.texts("p-web-1")); n >= 5000 || !sk.hadState("p-web-1", provider.LogTruncated) {
		t.Fatalf("%d lines kept, truncated said: %v", n, sk.hadState("p-web-1", provider.LogTruncated))
	}
}

// Re-review 2, P2: a replay cut between two known records (after dropping
// the later-stamped A) must not move where the delivered history ends: the
// next attempt repeats nothing.
func TestLogsReplayCutBetweenKnownRecordsRepeatsNothing(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(3), "A")
	e.fe.Journal(e.c.ID, 2, e.at(1), "B")
	e.fe.Journal(e.c.ID, 2, e.at(2), "C")
	e.fe.AddHook(logsHook(e.c.ID, true, 1, append(enginefake.Frame(1, append(stamped(e.at(3), "A"), '\n')), enginefake.Frame(3, []byte("the log file is corrupt"))...), false))
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the error", func() bool { return sk.isReady() && sk.hadState("p-web-1", provider.LogError) })
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 2, e.at(4), "D")
	sk.waitFor(t, "D", func() bool {
		ts := sk.texts("p-web-1 (stderr)")
		return len(ts) > 0 && ts[len(ts)-1] == "D"
	})
	if got := sk.texts("p-web-1 (stderr)"); !eq(got, "B", "C", "D") {
		t.Fatalf("stderr %v", got)
	}
	if got := sk.texts("p-web-1"); !eq(got, "A") {
		t.Fatalf("stdout %v", got)
	}
}

// Re-review 2, P2: a replay that drops a record before the partial anchor
// keeps the anchor partial: after the restart only the line's new part
// comes, without a gap.
func TestLogsPartialAnchorSurvivesAReplay(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(3), "A")
	e.fe.JournalPartial(e.c.ID, 2, e.at(2), "B") // written after A, stamped before it
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "waiting", func() bool {
		return sk.isReady() && eq(sk.texts("p-web-1 (stderr)"), "B") && sk.lastState("p-web-1").State == provider.LogWaiting
	})
	e.fe.Journal(e.c.ID, 2, e.at(4), "C")
	e.startContainer()
	sk.waitFor(t, "C", func() bool { return len(sk.texts("p-web-1 (stderr)")) >= 2 })
	if got := sk.texts("p-web-1 (stderr)"); !eq(got, "B", "C") || sk.hadState("p-web-1", provider.LogGap) {
		t.Fatalf("stderr %v, gap %v", got, sk.hadState("p-web-1", provider.LogGap))
	}
}

// Re-review 2, P2: a follow of a stopped container cut inside a frame is
// read again (the journal still holds the whole line), not left waiting
// for a start.
func TestLogsCutReadOfAStoppedContainerIsRetried(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	e.fe.Journal(e.c.ID, 1, e.at(2), "b")
	e.c.State = engine.ContainerState{Status: "exited"}
	e.fe.PutContainer(e.c)
	e.fe.EndLogs(e.c.ID)
	whole := enginefake.Frame(1, append(stamped(e.at(2), "b"), '\n'))
	e.fe.AddHook(logsHook(e.c.ID, false, 1, enginefake.Frame(1, append(stamped(e.at(1), "a"), '\n')), false))
	e.fe.AddHook(logsHook(e.c.ID, true, 1, whole[:len(whole)-1], false))
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "b after a retry", func() bool { return eq(sk.texts("p-web-1"), "a", "b") })
}

// Re-review 3, P2: the anchor was rotated away; the replay ends at the
// first record clearly after it (a gap), and the next resume starts from
// what was delivered since — nothing is shown twice.
func TestLogsRotatedAnchorIsLeftBehind(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(1), "a")
	e.fe.Journal(e.c.ID, 1, e.at(2), "b")
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "a", "b") })
	e.stopContainer(sk, "p-web-1")
	e.fe.RotateJournal(e.c.ID, e.at(3))
	e.fe.Journal(e.c.ID, 1, e.at(5), "c1")
	e.fe.Journal(e.c.ID, 1, e.at(6), "c2")
	e.startContainer()
	sk.waitFor(t, "c1 c2", func() bool { return eq(sk.texts("p-web-1"), "a", "b", "c1", "c2") })
	e.stopContainer(sk, "p-web-1")
	e.fe.Journal(e.c.ID, 1, e.at(7), "d")
	e.startContainer()
	sk.waitFor(t, "d", func() bool { return len(sk.texts("p-web-1")) >= 5 })
	if got := sk.texts("p-web-1"); !eq(got, "a", "b", "c1", "c2", "d") {
		t.Fatalf("stdout %v", got)
	}
	// the last resume asked from c2, not from the rotated b again
	var last enginefake.Request
	for _, r := range e.fe.Requests() {
		if strings.HasSuffix(r.Path, "/logs") {
			last = r
		}
	}
	if want := fmt.Sprintf("%d.%09d", e.at(6).Unix(), 0); last.Query.Get("since") != want {
		t.Fatalf("the last resume since %q, want %q", last.Query.Get("since"), want)
	}
}

// Re-review 3, P2: records delivered during a replay push the anchor out
// of the ring; it is still known by the next replay (not shown twice).
// (Records past the ring's size written just before the anchor may repeat,
// after a gap: a documented limit.)
func TestLogsAnchorOutlivesTheRing(t *testing.T) {
	e := newLogEnv(t)
	for i := range maxSeen + 50 {
		e.fe.Journal(e.c.ID, 1, e.at(1).Add(time.Duration(i+1)), fmt.Sprintf("x%d", i))
	}
	e.fe.Journal(e.c.ID, 2, e.at(1), "Z") // written after the x-s, stamped before them
	q := follow()
	q.TailLines = 1
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the x-s", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == maxSeen+50 })
	e.stopContainer(sk, "p-web-1 (stderr)")
	e.startContainer()
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 2, e.at(3), "D")
	sk.waitFor(t, "D", func() bool {
		ts := sk.texts("p-web-1 (stderr)")
		return len(ts) > 0 && ts[len(ts)-1] == "D"
	})
	if got := sk.texts("p-web-1 (stderr)"); !eq(got, "Z", "D") {
		t.Fatalf("stderr %v", got)
	}
	// more x-s than the ring holds were written within the stamps' skew
	// before Z: the ones it lost may come again, only after a gap
	if n := len(sk.texts("p-web-1")); n < maxSeen+50 || n > maxSeen+50 && !sk.hadState("p-web-1", provider.LogGap) {
		t.Fatalf("%d x-s, gap %v", n, sk.hadState("p-web-1", provider.LogGap))
	}
}

// Re-review 4, P2: a line assembled over a long time (its start stamped
// long before the lines written meanwhile) is still the anchor: records
// written before its end are not "past it".
func TestLogsLongAssembledAnchorIsMet(t *testing.T) {
	e := newLogEnv(t)
	e.fe.JournalPartial(e.c.ID, 1, e.at(1), "A")
	for i := range maxSeen + 50 {
		e.fe.Journal(e.c.ID, 2, e.at(11).Add(time.Duration(i)), fmt.Sprintf("x%d", i))
	}
	e.fe.Journal(e.c.ID, 1, e.at(21), "B") // ends the line A began
	q := follow()
	q.TailLines = provider.TailAll
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "AB") })
	e.stopContainer(sk, "p-web-1")
	e.startContainer()
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 1, e.at(31), "D")
	sk.waitFor(t, "D", func() bool {
		ts := sk.texts("p-web-1")
		return len(ts) > 0 && ts[len(ts)-1] == "D"
	})
	if got := sk.texts("p-web-1"); !eq(got, "AB", "D") {
		t.Fatalf("stdout %v", got)
	}
}

// Re-review 4, P2: records equal to the anchor that are not next to it
// (X, Y, X) count: the replay ends at the last X, nothing repeats.
func TestLogsAnchorEqualToAnEarlierRecord(t *testing.T) {
	e := newLogEnv(t)
	e.fe.Journal(e.c.ID, 1, e.at(2), "X")
	e.fe.Journal(e.c.ID, 2, e.at(2), "Y")
	e.fe.Journal(e.c.ID, 1, e.at(2), "X")
	sk, _ := e.stream(e.containerRef(), follow())
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && eq(sk.texts("p-web-1"), "X", "X") })
	for e.fe.LogFollowers(e.c.ID) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	e.fe.Journal(e.c.ID, 2, e.at(3), "D")
	sk.waitFor(t, "D", func() bool {
		ts := sk.texts("p-web-1 (stderr)")
		return len(ts) > 0 && ts[len(ts)-1] == "D"
	})
	if !eq(sk.texts("p-web-1"), "X", "X") || !eq(sk.texts("p-web-1 (stderr)"), "Y", "D") || sk.hadState("p-web-1", provider.LogGap) {
		t.Fatalf("stdout %v, stderr %v, gap %v", sk.texts("p-web-1"), sk.texts("p-web-1 (stderr)"), sk.hadState("p-web-1", provider.LogGap))
	}
}

// More equal records in a row than the ring holds (same stamp, stream and
// text): the resume after a restart drops them all, repeats none (Codex,
// P6 round-5 re-review).
func TestLogsARunOfEqualRecordsLongerThanTheRingRepeatsNothing(t *testing.T) {
	e := newLogEnv(t)
	for range maxSeen + 1 {
		e.fe.Journal(e.c.ID, 1, e.at(2), "X")
	}
	q := follow()
	q.Channel = channelStdout
	sk, _ := e.stream(e.containerRef(), q)
	sk.waitFor(t, "the backlog", func() bool { return sk.isReady() && len(sk.texts("p-web-1")) == maxSeen+1 })
	e.stopContainer(sk, "p-web-1")
	e.fe.Journal(e.c.ID, 1, e.at(3), "D")
	e.startContainer()
	sk.waitFor(t, "D", func() bool {
		ts := sk.texts("p-web-1")
		return len(ts) > 0 && ts[len(ts)-1] == "D"
	})
	if n := len(sk.texts("p-web-1")); n != maxSeen+2 || sk.hadState("p-web-1", provider.LogGap) {
		t.Fatalf("%d records (want %d), gap %v", n, maxSeen+2, sk.hadState("p-web-1", provider.LogGap))
	}
}
