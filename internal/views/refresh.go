package views

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const refreshWait = 90 * time.Second

type refreshJob struct {
	done chan struct{}
	err  error
}

// watchControl keeps a stable view and its rows while a replacement observation
// synchronizes. An epoch guard rejects callbacks already queued by a stopped
// subscription. A refresh candidate becomes visible as one complete reset.
type watchControl struct {
	mu      sync.Mutex
	view    *View
	source  Source
	query   provider.Query
	ctx     context.Context
	cancel  context.CancelFunc
	epoch   uint64
	refresh uint64
	started time.Time
	stop    func()
	closed  bool
	job     *refreshJob
}

type observation struct {
	owner   *watchControl
	epoch   uint64
	staging bool
	rows    map[string]core.Row
	done    chan struct{}
	once    sync.Once
	err     error
}

func newWatchControl(v *View, src Source, q provider.Query) *watchControl {
	ctx, cancel := context.WithCancel(context.Background())
	return &watchControl{view: v, source: src, query: q, ctx: ctx, cancel: cancel, epoch: 1, started: time.Now()}
}

func (c *watchControl) start() error {
	stop, err := c.source.Watch(c.query, &observation{owner: c, epoch: 1})
	if err != nil {
		c.close()
		return err
	}
	c.install(1, stop)
	go c.trackProgress(1)
	return nil
}
func (c *watchControl) install(epoch uint64, stop func()) {
	c.mu.Lock()
	stale := c.closed || c.epoch != epoch
	if !stale {
		c.stop = stop
	}
	c.mu.Unlock()
	if stale && stop != nil {
		stop()
	}
}
func (c *watchControl) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.epoch++
	stop := c.stop
	c.stop = nil
	c.cancel()
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (c *watchControl) request(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrGone
	}
	job := c.job
	if job == nil {
		job = &refreshJob{done: make(chan struct{})}
		c.job = job
		c.epoch++
		c.refresh++
		c.started = time.Now()
		epoch := c.epoch
		stop := c.stop
		c.stop = nil
		st := provider.ViewStatus{State: provider.StatusLoading, Refresh: c.refresh}
		c.view.Apply(provider.Delta{Status: &st})
		go c.run(job, epoch, stop)
		go c.trackProgress(epoch)
	}
	c.mu.Unlock()
	select {
	case <-job.done:
		return job.err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return ErrGone
	}
}

func (c *watchControl) run(job *refreshJob, epoch uint64, oldStop func()) {
	if oldStop != nil {
		oldStop()
	}
	ctx, cancel := context.WithTimeout(c.ctx, refreshWait)
	defer cancel()
	var err error
	if prep, ok := c.source.(provider.RefreshPreparer); ok {
		err = prep.PrepareRefresh(ctx, c.query)
	} else if r, ok := c.source.(provider.Resyncer); ok {
		err = r.Resync(c.query)
	} else {
		err = &provider.Error{Class: provider.ClassUnsupported, Message: "this view cannot be refreshed"}
	}
	// Even after a failed preparation, reattach to the admitted source so that
	// automatic recovery can continue; a failed candidate never clears old rows.
	obs := &observation{owner: c, epoch: epoch, staging: true, rows: map[string]core.Row{}, done: make(chan struct{})}
	if c.ctx.Err() == nil {
		stop, watchErr := c.source.Watch(c.query, obs)
		if watchErr == nil {
			c.install(epoch, stop)
		} else if err == nil {
			err = watchErr
		}
	}
	if err == nil {
		select {
		case <-obs.done:
			err = obs.err
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	c.mu.Lock()
	if !c.closed && c.epoch == epoch && err != nil {
		class := provider.ClassUnavailable
		var pe *provider.Error
		if errors.As(err, &pe) {
			class = pe.Class
		}
		st := provider.ViewStatus{State: provider.StatusStale, Class: class, Message: err.Error(), Refresh: c.refresh}
		if class == provider.ClassRemoved || class == provider.ClassSchemaChanged || class == provider.ClassScopeLimit {
			st.State = provider.StatusError
		}
		c.view.Apply(provider.Delta{Reset: class == provider.ClassScopeLimit, Status: &st})
	}
	job.err = err
	if c.job == job {
		c.job = nil
	}
	close(job.done)
	c.mu.Unlock()
}

func (o *observation) Apply(d provider.Delta) {
	c := o.owner
	if reporter, ok := c.source.(provider.LoadingProgressReporter); ok && d.Status != nil {
		c.mu.Lock()
		started := c.started
		valid := !c.closed && c.epoch == o.epoch
		c.mu.Unlock()
		if !valid {
			return
		}
		status := d.Status.Clone()
		status.DownloadedBytes = reporter.LoadingProgress(c.query, started)
		d.Status = &status
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.epoch != o.epoch {
		return
	}
	if d.Status != nil {
		st := d.Status.Clone()
		st.Refresh = c.refresh
		d.Status = &st
	}
	if !o.staging {
		c.view.Apply(d)
		return
	}
	if d.Reset {
		o.rows = map[string]core.Row{}
	}
	for _, r := range d.Upserts {
		o.rows[r.ID] = r
	}
	for _, id := range d.Deletes {
		delete(o.rows, id)
	}
	if d.Status == nil {
		return
	}
	st := *d.Status
	if st.State == provider.StatusLoading {
		c.view.Apply(provider.Delta{Status: &st})
		return
	}
	if st.State == provider.StatusError {
		o.err = &provider.Error{Class: st.Class, Message: st.Message}
		if st.Class != provider.ClassRemoved && st.Class != provider.ClassSchemaChanged && st.Class != provider.ClassScopeLimit {
			st.State = provider.StatusStale
		}
		c.view.Apply(provider.Delta{Reset: st.Class == provider.ClassScopeLimit, Status: &st})
		o.once.Do(func() { close(o.done) })
		return
	}
	// Ready or stale means the source has delivered its snapshot. Subsequent
	// updates use the same epoch-guarded live path.
	rows := make([]core.Row, 0, len(o.rows))
	for _, r := range o.rows {
		rows = append(rows, r)
	}
	o.staging = false
	o.rows = nil
	c.view.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &st})
	o.once.Do(func() { close(o.done) })
}

// Observe local counters only while loading. Preparation can itself wait for
// a fresh shared snapshot, before the replacement subscription is attached.
func (c *watchControl) trackProgress(epoch uint64) {
	reporter, ok := c.source.(provider.LoadingProgressReporter)
	if !ok {
		return
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		c.mu.Lock()
		if c.closed || c.epoch != epoch || c.view.Status().State != provider.StatusLoading {
			c.mu.Unlock()
			return
		}
		started := c.started
		c.mu.Unlock()
		bytes := reporter.LoadingProgress(c.query, started)
		c.mu.Lock()
		if c.closed || c.epoch != epoch {
			c.mu.Unlock()
			return
		}
		status := c.view.Status()
		if status.State != provider.StatusLoading {
			c.mu.Unlock()
			return
		}
		status.DownloadedBytes = bytes
		c.view.Apply(provider.Delta{Status: &status})
		c.mu.Unlock()
		select {
		case <-c.ctx.Done():
			return
		case <-tick.C:
		}
	}
}
