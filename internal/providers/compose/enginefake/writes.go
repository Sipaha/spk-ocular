package enginefake

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// The fake's writes: exec (create, start with a hijacked stream, resize,
// inspect), container stats and the container actions (start, stop,
// restart, remove) — each changing the model and emitting its events like
// the daemon (304 for a start of a running container or a stop of a
// stopped one, 409 for removing a running one).

// ExecFunc runs an exec's command on its hijacked stream and returns its
// exit code (NotStarted(code): it never started). Resizes arrive on sizes
// (cols, rows) while it runs.
type ExecFunc func(cmd []string, tty bool, rw io.ReadWriter, sizes <-chan [2]uint16) int

// Exec is one exec the fake created.
type Exec struct {
	ID        string
	Container string
	Cmd       []string
	Tty       bool
	Running   bool
	ExitCode  int
	Pid       int
	Started   bool
	// ConsoleSize as created ([rows, cols]); nil: not sent.
	ConsoleSize []uint16
	Sizes       [][2]uint16 // resizes (cols, rows), in order
	sizes       chan [2]uint16
}

// StatsFunc answers a stats request of a container (oneShot: the
// request's one-shot); the JSON is served with 200.
type StatsFunc func(id string, oneShot bool) []byte

type writeState struct {
	mu        sync.Mutex
	execs     map[string]*Exec
	nextExec  int
	execFn    ExecFunc
	statsFn   StatsFunc
	statCount map[string]int
}

func (e *Engine) writes() *writeState {
	e.wOnce.Do(func() { e.w = &writeState{execs: map[string]*Exec{}, statCount: map[string]int{}} })
	return e.w
}

// SetExec sets how execs run (default: EchoShell).
func (e *Engine) SetExec(fn ExecFunc) {
	w := e.writes()
	w.mu.Lock()
	w.execFn = fn
	w.mu.Unlock()
}

// SetStats sets how stats are answered (default: DefaultStats).
func (e *Engine) SetStats(fn StatsFunc) {
	w := e.writes()
	w.mu.Lock()
	w.statsFn = fn
	w.mu.Unlock()
}

// Execs are the execs created so far, in creation order.
func (e *Engine) Execs() []Exec {
	w := e.writes()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Exec, 0, len(w.execs))
	for i := 1; i <= w.nextExec; i++ {
		if x, ok := w.execs["exec"+strconv.Itoa(i)]; ok {
			c := *x
			c.Sizes = append([][2]uint16(nil), x.Sizes...)
			c.ConsoleSize = append([]uint16(nil), x.ConsoleSize...)
			out = append(out, c)
		}
	}
	return out
}

// notStarted marks an ExecFunc's code as "the command never started".
const notStarted = 1 << 16

// NotStarted is what an ExecFunc returns for a command that could not be
// started (like runc: the daemon reports code with no pid).
func NotStarted(code int) int { return code | notStarted }

// EchoShell is the default exec: a missing command (a path starting with
// /nonexistent) fails like runc (text in the stream, code 127, no pid);
// otherwise a prompt "$ ", each line echoed, "exit N" ends with N, the end
// of stdin with 0.
func EchoShell(cmd []string, _ bool, rw io.ReadWriter, _ <-chan [2]uint16) int {
	if len(cmd) > 0 && strings.HasPrefix(cmd[0], "/nonexistent") {
		_, _ = fmt.Fprintf(rw, "OCI runtime exec failed: exec failed: unable to start container process: exec: %q: stat %s: no such file or directory\r\n", cmd[0], cmd[0])
		return NotStarted(127)
	}
	_, _ = io.WriteString(rw, "$ ")
	sc := bufio.NewScanner(rw)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if code, ok := strings.CutPrefix(line, "exit "); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(code))
			return n
		}
		_, _ = io.WriteString(rw, line+"\r\n$ ")
	}
	return 0
}

// serveWrite answers the write paths; false: not one of them.
func (e *Engine) serveWrite(w http.ResponseWriter, r *http.Request, path string) bool {
	rest, isCtr := strings.CutPrefix(path, "/containers/")
	execRest, isExec := strings.CutPrefix(path, "/exec/")
	switch {
	case isCtr && r.Method == http.MethodPost && strings.HasSuffix(rest, "/exec"):
		e.createExec(w, r, strings.TrimSuffix(rest, "/exec"))
	case isExec && r.Method == http.MethodPost && strings.HasSuffix(execRest, "/start"):
		e.startExec(w, r, strings.TrimSuffix(execRest, "/start"))
	case isExec && r.Method == http.MethodPost && strings.HasSuffix(execRest, "/resize"):
		e.resizeExec(w, r, strings.TrimSuffix(execRest, "/resize"))
	case isExec && r.Method == http.MethodGet && strings.HasSuffix(execRest, "/json"):
		e.inspectExec(w, strings.TrimSuffix(execRest, "/json"))
	case isCtr && r.Method == http.MethodGet && strings.HasSuffix(rest, "/stats"):
		e.serveStats(w, r, strings.TrimSuffix(rest, "/stats"))
	case isCtr && r.Method == http.MethodPost && strings.HasSuffix(rest, "/start"):
		if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			// like the daemon since API 1.24
			writeError(w, http.StatusBadRequest, "starting container with non-empty request body was deprecated since API v1.22 and removed in v1.24")
			return true
		}
		e.containerAction(w, r, strings.TrimSuffix(rest, "/start"), "start")
	case isCtr && r.Method == http.MethodPost && strings.HasSuffix(rest, "/stop"):
		e.containerAction(w, r, strings.TrimSuffix(rest, "/stop"), "stop")
	case isCtr && r.Method == http.MethodPost && strings.HasSuffix(rest, "/restart"):
		e.containerAction(w, r, strings.TrimSuffix(rest, "/restart"), "restart")
	case isCtr && r.Method == http.MethodDelete && !strings.Contains(rest, "/"):
		e.containerAction(w, r, rest, "remove")
	default:
		return false
	}
	return true
}

// container finds a container by id, id prefix or name.
func (e *Engine) container(ref string) (engine.ContainerInspect, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if raw, ok := e.containers[ref]; ok {
		return decode[engine.ContainerInspect](raw), true
	}
	var found []engine.ContainerInspect
	for id, raw := range e.containers {
		c := decode[engine.ContainerInspect](raw)
		if strings.HasPrefix(id, ref) || strings.TrimPrefix(c.Name, "/") == ref {
			found = append(found, c)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return engine.ContainerInspect{}, false
}

func (e *Engine) createExec(w http.ResponseWriter, r *http.Request, ref string) {
	c, ok := e.container(ref)
	if !ok {
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	switch {
	case c.State.Paused:
		writeError(w, http.StatusConflict, fmt.Sprintf("Container %s is paused, unpause the container before exec", strings.TrimPrefix(c.Name, "/")))
		return
	case !c.State.Running:
		writeError(w, http.StatusConflict, fmt.Sprintf("container %s is not running", c.ID))
		return
	}
	var cfg struct {
		Cmd         []string
		Tty         bool
		ConsoleSize []uint16
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad exec config: "+err.Error())
		return
	}
	ws := e.writes()
	ws.mu.Lock()
	ws.nextExec++
	x := &Exec{ID: "exec" + strconv.Itoa(ws.nextExec), Container: c.ID, Cmd: cfg.Cmd, Tty: cfg.Tty, ConsoleSize: cfg.ConsoleSize, sizes: make(chan [2]uint16, 64)}
	ws.execs[x.ID] = x
	ws.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"Id": x.ID})
}

func (e *Engine) startExec(w http.ResponseWriter, r *http.Request, id string) {
	ws := e.writes()
	ws.mu.Lock()
	x, ok := ws.execs[id]
	fn := ws.execFn
	started := ok && x.Started
	ws.mu.Unlock()
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	case started:
		writeError(w, http.StatusConflict, "exec "+id+" has already started")
		return
	case !strings.EqualFold(r.Header.Get("Upgrade"), "tcp"):
		writeError(w, http.StatusBadRequest, "the fake Engine starts execs only with Upgrade: tcp")
		return
	}
	// the daemon checks the container again at the start
	c, found := e.container(x.Container)
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "No such container: "+x.Container)
		return
	case c.State.Paused:
		writeError(w, http.StatusConflict, fmt.Sprintf("Container %s is paused, unpause the container before exec", c.ID))
		return
	case !c.State.Running:
		writeError(w, http.StatusConflict, fmt.Sprintf("container %s is not running", c.ID))
		return
	}
	if fn == nil {
		fn = EchoShell
	}
	// the start config is read before the switch: its bytes are not stdin
	var start struct {
		Tty         bool
		ConsoleSize []uint16
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&start); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad exec start config: "+err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	ws.mu.Lock()
	x.Started, x.Running, x.Pid = true, true, 4242
	ws.mu.Unlock()
	conn, brw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	ctype := "application/vnd.docker.multiplexed-stream"
	if x.Tty {
		ctype = "application/vnd.docker.raw-stream"
	}
	_, _ = fmt.Fprintf(brw, "HTTP/1.1 101 UPGRADED\r\nContent-Type: %s\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n", ctype)
	_ = brw.Flush()
	rw := struct {
		io.Reader
		io.Writer
	}{brw.Reader, conn}
	code := fn(x.Cmd, x.Tty, rw, x.sizes)
	ws.mu.Lock()
	x.Running, x.ExitCode = false, code&^notStarted
	if code&notStarted != 0 {
		x.Pid = 0
	}
	ws.mu.Unlock()
}

func (e *Engine) resizeExec(w http.ResponseWriter, r *http.Request, id string) {
	ws := e.writes()
	cols, _ := strconv.Atoi(r.URL.Query().Get("w"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("h"))
	ws.mu.Lock()
	x, ok := ws.execs[id]
	if ok {
		x.Sizes = append(x.Sizes, [2]uint16{uint16(cols), uint16(rows)}) //nolint:gosec // test sizes
		select {
		case x.sizes <- [2]uint16{uint16(cols), uint16(rows)}: //nolint:gosec // test sizes
		default:
		}
	}
	ws.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) inspectExec(w http.ResponseWriter, id string) {
	ws := e.writes()
	ws.mu.Lock()
	x, ok := ws.execs[id]
	var body map[string]any
	if ok {
		// like Moby: no exit code (null) until the process ended
		var code *int
		if x.Started && !x.Running {
			code = &x.ExitCode
		}
		body = map[string]any{"ID": x.ID, "Running": x.Running, "ExitCode": code, "Pid": x.Pid, "ContainerID": x.Container}
	}
	ws.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	}
	writeJSON(w, body)
}

// DefaultStats: a running container's counters grow by 0.5 core-seconds
// of 2 online CPUs' 1 s per sample; memory 64 MiB used, 16 MiB of it
// inactive file cache (cgroup v2). A one-shot answer is one sample without
// precpu; otherwise the daemon takes two (precpu is the first, 1 s
// before), even on the first request. A stopped container's is all zero
// with a zero read time.
func (e *Engine) DefaultStats(id string, oneShot bool) []byte {
	c, _ := e.container(id)
	ws := e.writes()
	ws.mu.Lock()
	ws.statCount[id]++
	if !oneShot {
		ws.statCount[id]++ // the daemon's own second sample
	}
	n := int64(ws.statCount[id])
	ws.mu.Unlock()
	type cpu struct {
		CPUUsage struct {
			TotalUsage int64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage int64 `json:"system_cpu_usage"`
		OnlineCPUs     int   `json:"online_cpus"`
	}
	var cur, pre cpu
	st := map[string]any{"id": c.ID, "name": c.Name, "os_type": "linux", "read": "0001-01-01T00:00:00Z"}
	if c.State.Running {
		cur.CPUUsage.TotalUsage, cur.SystemCPUUsage, cur.OnlineCPUs = n*5e8, n*2e9, 2
		if !oneShot {
			pre.CPUUsage.TotalUsage, pre.SystemCPUUsage, pre.OnlineCPUs = (n-1)*5e8, (n-1)*2e9, 2
		}
		now := time.Now().UTC()
		st["read"] = now.Format(time.RFC3339Nano)
		if !oneShot {
			st["preread"] = now.Add(-time.Second).Format(time.RFC3339Nano)
		}
		st["memory_stats"] = map[string]any{"usage": 64 << 20, "limit": 1 << 30, "stats": map[string]int64{"inactive_file": 16 << 20}}
	} else {
		st["memory_stats"] = map[string]any{}
	}
	st["cpu_stats"], st["precpu_stats"] = cur, pre
	return mustJSON(st)
}

func (e *Engine) serveStats(w http.ResponseWriter, r *http.Request, ref string) {
	c, ok := e.container(ref)
	if !ok {
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	q := r.URL.Query()
	if q.Get("stream") != "false" && q.Get("stream") != "0" {
		writeError(w, http.StatusBadRequest, "the fake Engine answers stats only with stream=false")
		return
	}
	oneShot := q.Get("one-shot") == "true" || q.Get("one-shot") == "1"
	ws := e.writes()
	ws.mu.Lock()
	fn := ws.statsFn
	ws.mu.Unlock()
	var body []byte
	if fn != nil {
		body = fn(c.ID, oneShot)
	} else {
		body = e.DefaultStats(c.ID, oneShot)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// containerAction does start/stop/restart/remove on the model and emits
// the daemon's events. A stop exits with 128 + the stop signal's number
// (SIGKILL with t=0); an AutoRemove container is removed once stopped; a
// paused one cannot be started (409).
func (e *Engine) containerAction(w http.ResponseWriter, r *http.Request, ref, action string) {
	c, ok := e.container(ref)
	if !ok {
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	now := time.Now().UTC()
	var events []string
	switch action {
	case "start":
		if c.State.Paused {
			writeError(w, http.StatusConflict, "cannot start a paused container, try unpause instead")
			return
		}
		if c.State.Running {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		c.State = engine.ContainerState{Status: "running", Running: true, StartedAt: engine.TimeOf(now), Health: c.State.Health}
		events = []string{"start"}
	case "stop":
		if !c.State.Running {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		code := 128 + signalNumber(c.Config.StopSignal)
		if r.URL.Query().Get("t") == "0" {
			code = 137
		}
		c.State = engine.ContainerState{Status: "exited", ExitCode: code, StartedAt: c.State.StartedAt, FinishedAt: engine.TimeOf(now)}
		events = []string{"kill", "die", "stop"}
		if c.HostConfig.AutoRemove {
			e.RemoveContainer(c.ID)
			e.EndLogs(c.ID)
			for _, ev := range append(events, "destroy") {
				e.Emit(containerActionEvent(ev, c))
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	case "restart":
		c.State = engine.ContainerState{Status: "running", Running: true, StartedAt: engine.TimeOf(now), Health: c.State.Health}
		events = []string{"die", "start", "restart"}
	case "remove":
		if c.State.Running {
			writeError(w, http.StatusConflict, fmt.Sprintf("cannot remove container %q: container is running: stop the container before removing or force remove", c.Name))
			return
		}
		e.RemoveContainer(c.ID)
		e.Emit(containerActionEvent("destroy", c))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	e.PutContainer(c)
	if action != "start" {
		e.EndLogs(c.ID)
		if c.State.Running {
			e.ResumeLogs(c.ID)
		}
	} else {
		e.ResumeLogs(c.ID)
	}
	for _, ev := range events {
		e.Emit(containerActionEvent(ev, c))
	}
	w.WriteHeader(http.StatusNoContent)
}

// signalNumber of a stop signal ("" — SIGTERM; unknown ones as SIGTERM).
func signalNumber(sig string) int {
	switch strings.TrimPrefix(strings.ToUpper(sig), "SIG") {
	case "HUP", "1":
		return 1
	case "INT", "2":
		return 2
	case "QUIT", "3":
		return 3
	case "KILL", "9":
		return 9
	case "USR1", "10":
		return 10
	case "USR2", "12":
		return 12
	}
	return 15
}

func containerActionEvent(action string, c engine.ContainerInspect) engine.Event {
	attrs := map[string]string{"name": strings.TrimPrefix(c.Name, "/"), "image": c.Config.Image}
	for k, v := range c.Config.Labels {
		attrs[k] = v
	}
	return engine.Event{Type: "container", Action: action, Actor: engine.EventActor{ID: c.ID, Attributes: attrs}}
}
