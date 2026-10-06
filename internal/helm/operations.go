package helm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
	"sigs.k8s.io/yaml"
)

// Factory builds a fresh action configuration for one bounded operation.
type Factory func(context.Context, string) (*action.Configuration, error)
type storedPlan struct {
	plan                Plan
	chart               chart.Charter
	fingerprint, owner  string
	rollbackFingerprint string
	timer               *time.Timer
}
type Plans struct {
	mu      sync.Mutex
	entries map[string]storedPlan
	busy    map[string]bool
}

func NewPlans() *Plans { return &Plans{entries: map[string]storedPlan{}, busy: map[string]bool{}} }
func (p *Plans) Forget(id, owner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.entries[id]; ok && v.owner == owner {
		delete(p.entries, id)
		if v.timer != nil {
			v.timer.Stop()
		}
	}
}

func (p *Plans) Prepare(ctx context.Context, owner string, f Factory, o Operation, ch chart.Charter) (Plan, error) {
	if err := checkOperation(o); err != nil {
		return Plan{}, err
	}
	a, err := f(ctx, o.Namespace)
	if err != nil {
		return Plan{}, err
	}
	r, fingerprint, err := current(a, o.Name)
	if err != nil {
		return Plan{}, err
	}
	if err = checkCurrent(o, r); err != nil {
		return Plan{}, err
	}
	plan := Plan{PreviewMode: "server", Operation: o, Expires: time.Now().Add(10 * time.Minute), Warnings: []string{
		"Helm hooks may run arbitrary workloads. The preview contains sensitive values and manifests.",
		"The chart and values are pinned; templates using lookup, time or randomness may render differently at execution. CRDs and hook side effects are not fully represented by this preview.",
	}}
	if r != nil {
		plan.CurrentRevision = r.Version
		plan.PreviousManifest = r.Manifest
	}
	rollbackFingerprint := ""
	switch o.Action {
	case "install", "upgrade":
		if o.Action == "install" && hasCRDs(ch) {
			plan.PreviewMode = "client"
		}
		raw, err := applyChart(ctx, a, o, ch, true)
		if err != nil {
			return Plan{}, err
		}
		rel, err := asRelease(raw)
		if err != nil {
			return Plan{}, err
		}
		plan.Manifest = rel.Manifest
		for _, h := range rel.Hooks {
			plan.Manifest += "\n---\n" + h.Manifest
		}
		if rel.Info != nil {
			plan.Notes = rel.Info.Notes
		}
	case "rollback":
		raw, err := a.Releases.Get(o.Name, o.Revision)
		if err != nil {
			return Plan{}, err
		}
		rel, err := asRelease(raw)
		if err != nil {
			return Plan{}, err
		}
		rollbackFingerprint, err = releaseFingerprint(rel)
		if err != nil {
			return Plan{}, err
		}
		plan.Manifest = rel.Manifest
		for _, h := range rel.Hooks {
			plan.Manifest += "\n---\n" + h.Manifest
		}
	case "uninstall":
		plan.Manifest = r.Manifest
	}
	// A concurrent external Helm change during rendering invalidates this review.
	_, after, err := current(a, o.Name)
	if err != nil {
		return Plan{}, err
	}
	if after != fingerprint {
		return Plan{}, errors.New("release changed while preparing; prepare again")
	}
	id := make([]byte, 24)
	if _, err = rand.Read(id); err != nil {
		return Plan{}, err
	}
	plan.ID = hex.EncodeToString(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, v := range p.entries {
		if time.Now().After(v.plan.Expires) {
			delete(p.entries, id)
		}
	}
	if len(p.entries) >= 16 {
		return Plan{}, errors.New("too many open Helm reviews; close a review first")
	}
	idToForget := plan.ID
	timer := time.AfterFunc(time.Until(plan.Expires), func() { p.Forget(idToForget, owner) })
	p.entries[plan.ID] = storedPlan{plan: plan, chart: ch, fingerprint: fingerprint, owner: owner, rollbackFingerprint: rollbackFingerprint, timer: timer}
	return plan, nil
}

func (p *Plans) Run(ctx context.Context, owner, id string, f Factory) (Result, error) {
	p.mu.Lock()
	v, ok := p.entries[id]
	if !ok || v.owner != owner {
		p.mu.Unlock()
		return Result{}, errors.New("helm review is missing or belongs to another connection")
	}
	delete(p.entries, id) // one-shot, including failures; no ambiguous replay
	if v.timer != nil {
		v.timer.Stop()
	}
	key := owner + "\x00" + v.plan.Operation.Namespace + "\x00" + v.plan.Operation.Name
	if p.busy[key] {
		p.mu.Unlock()
		return Result{}, errors.New("a Helm operation is already running for this release")
	}
	p.busy[key] = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.busy, key); p.mu.Unlock() }()
	if time.Now().After(v.plan.Expires) {
		return Result{}, errors.New("helm review expired; prepare again")
	}
	o := v.plan.Operation
	ctx, cancel := context.WithTimeout(ctx, time.Duration(o.TimeoutSeconds)*time.Second)
	defer cancel()
	a, err := f(ctx, o.Namespace)
	if err != nil {
		return Result{}, err
	}
	_, fingerprint, err := current(a, o.Name)
	if err != nil {
		return Result{}, err
	}
	if fingerprint != v.fingerprint {
		return Result{}, errors.New("release changed after review; prepare again")
	}
	if o.Action == "rollback" {
		raw, err := a.Releases.Get(o.Name, o.Revision)
		if err != nil {
			return Result{}, err
		}
		rel, err := asRelease(raw)
		if err != nil {
			return Result{}, err
		}
		fingerprint, err := releaseFingerprint(rel)
		if err != nil {
			return Result{}, err
		}
		if fingerprint != v.rollbackFingerprint {
			return Result{}, errors.New("rollback revision changed after review; prepare again")
		}
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	switch o.Action {
	case "install", "upgrade":
		raw, runErr := applyChart(ctx, a, o, v.chart, false)
		err = runErr
		if err == nil {
			r, e := asRelease(raw)
			if e != nil {
				return Result{}, e
			}
			s := summary(r)
			return Result{Outcome: "done", Release: &s}, nil
		}
	case "rollback":
		r := action.NewRollback(a)
		r.Version = o.Revision
		r.Timeout = time.Duration(o.TimeoutSeconds) * time.Second
		r.DisableHooks = o.DisableHooks
		r.WaitStrategy = waitStrategy(o)
		r.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
		err = r.Run(o.Name)
	case "uninstall":
		u := action.NewUninstall(a)
		u.Timeout = time.Duration(o.TimeoutSeconds) * time.Second
		u.DisableHooks = o.DisableHooks
		u.KeepHistory = o.KeepHistory
		u.WaitStrategy = waitStrategy(o)
		u.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
		_, err = u.Run(o.Name)
	}
	if err != nil {
		return Result{Outcome: "unknown", Message: "Helm did not complete successfully. Inspect the release and cluster before preparing another operation. " + err.Error()}, nil
	}
	return Result{Outcome: "done"}, nil
}
func waitStrategy(o Operation) kube.WaitStrategy {
	if o.Wait {
		return kube.StatusWatcherStrategy
	}
	return kube.HookOnlyStrategy
}
func applyChart(ctx context.Context, a *action.Configuration, o Operation, ch chart.Charter, dry bool) (any, error) {
	if ch == nil {
		return nil, errors.New("chart is required")
	}
	ch, err := cloneChart(ch)
	if err != nil {
		return nil, err
	}
	vals := map[string]any{}
	if err := yaml.UnmarshalStrict([]byte(o.Values), &vals, func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }); err != nil {
		return nil, err
	}
	if o.Action == "install" {
		installConfig := a
		clientPreview := dry && hasCRDs(ch)
		if clientPreview {
			installConfig = action.NewConfiguration(action.ConfigurationSetLogger(a.Logger().Handler()))
		}
		i := action.NewInstall(installConfig)
		i.ReleaseName = o.Name
		i.Namespace = o.Namespace
		i.Timeout = time.Duration(o.TimeoutSeconds) * time.Second
		i.CreateNamespace = o.CreateNamespace
		i.DisableHooks = o.DisableHooks
		i.WaitStrategy = waitStrategy(o)
		i.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
		if dry {
			i.DryRunStrategy = action.DryRunServer
		}
		if clientPreview {
			if err := configureCRDPreview(i, a, ch); err != nil {
				return nil, err
			}
		}
		return i.RunWithContext(ctx, ch, vals)
	}
	u := action.NewUpgrade(a)
	u.Namespace = o.Namespace
	u.Timeout = time.Duration(o.TimeoutSeconds) * time.Second
	u.DisableHooks = o.DisableHooks
	u.WaitStrategy = waitStrategy(o)
	u.WaitOptions = []kube.WaitOption{kube.WithWaitContext(ctx)}
	u.ResetValues = true
	if dry {
		u.DryRunStrategy = action.DryRunServer
	}
	return u.RunWithContext(ctx, o.Name, ch, vals)
}

// Helm mutates chart values/dependencies while processing conditions. Each pass
// receives its own tree so dry-run cannot alter the pinned execution input.
func cloneChart(raw chart.Charter) (chart.Charter, error) {
	source, ok := raw.(*chartv2.Chart)
	if !ok {
		return nil, errors.New("unsupported chart format")
	}
	b, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var cloned chartv2.Chart
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err = decoder.Decode(&cloned); err != nil {
		return nil, err
	}
	cloned.Raw = source.Raw
	for _, dependency := range source.Dependencies() {
		ch, err := cloneChart(dependency)
		if err != nil {
			return nil, err
		}
		cloned.AddDependency(ch.(*chartv2.Chart))
	}
	return &cloned, nil
}

func (p *Plans) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range p.entries {
		if v.timer != nil {
			v.timer.Stop()
		}
	}
	clear(p.entries)
}
