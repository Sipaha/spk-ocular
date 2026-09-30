package kubernetes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Run now (P12): a Job made from a CronJob's template, as kubectl create
// job --from=cronjob does. The name is chosen at the review and carried in
// a signed grant (the plan's Expect); a grant is spent before its one
// POST, so one review sends at most one Job — also after that Job is gone.
var actRunNow = core.ActionDescriptor{ID: "run", Title: "Run now"}

var jobsV1GVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}

const (
	// instantiateKey marks a Job made by hand from a CronJob (kubectl's).
	instantiateKey = "cronjob.kubernetes.io/instantiate"
	// runGrantTag separates run grants from the key's other uses.
	runGrantTag = "spk-ocular/p12/run-grant/v1\x00"
	// maxGrantLen bounds what a run decodes.
	maxGrantLen = 4096
	// maxSpentGrants bounds the grants remembered as spent (live ones).
	maxSpentGrants = 1000
)

// runGrantTTL: how long a run's review may be confirmed. A variable for tests.
var runGrantTTL = 10 * time.Minute

var suffixRe = regexp.MustCompile(`^[a-z0-9]{5}$`)

// runJobName: <CronJob, cut to 50, no trailing '.' or '-'>-manual-<suffix>;
// ok false when the result is not a valid Job name (DNS subdomain and label
// value: the name is its pods' job-name label).
func runJobName(cron, suffix string) (string, bool) {
	base := cron
	if len(base) > 50 {
		base = base[:50]
	}
	base = strings.TrimRight(base, ".-")
	name := base + "-manual-" + suffix
	ok := base != "" && suffixRe.MatchString(suffix) && len(validation.IsDNS1123Subdomain(name)) == 0 && len(validation.IsValidLabelValue(name)) == 0
	return name, ok
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomSuffix() string {
	const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// runState: what a run's texts read — the CronJob's incarnation, its
// template, suspension, concurrency policy, history limits. Its status
// (the active runs) is only seen at the review, never checked.
func runState(u *unstructured.Unstructured) string {
	o := u.Object
	b, _ := json.Marshal(map[string]any{
		"uid": string(u.GetUID()), "deleting": u.GetDeletionTimestamp() != nil,
		"template": fieldAt(o, "spec", "jobTemplate"), "suspend": boolAt(o, "spec", "suspend"),
		"concurrency": fieldAt(o, "spec", "concurrencyPolicy"),
		"succeeded":   fieldAt(o, "spec", "successfulJobsHistoryLimit"), "failed": fieldAt(o, "spec", "failedJobsHistoryLimit"),
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// runGrant is what a run's review promised, signed.
type runGrant struct {
	Action string `json:"act"`
	Route  string `json:"route"`
	NS     string `json:"ns"`
	Name   string `json:"name"`
	UID    string `json:"uid"`
	State  string `json:"state"`
	Job    string `json:"job"`
	Exp    int64  `json:"exp"` // unix ms
	Inc    string `json:"inc"` // the session incarnation
	Nonce  string `json:"nonce"`
}

func runGrantSig(payload []byte) string {
	m := hmac.New(sha256.New, printKey)
	m.Write([]byte(runGrantTag))
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

// expect encodes g as a plan's Expect: <route>-<payload>.<signature>.
func (g runGrant) expect() string {
	b, _ := json.Marshal(g)
	return g.Route + "-" + base64.RawURLEncoding.EncodeToString(b) + "." + runGrantSig(b)
}

// readRunGrant checks a run's Expect: its signature, and that it names
// this action, route, object and a valid Job name for it. Expiry and the
// session incarnation are the caller's (a conflict, not an invalid request).
func readRunGrant(def *kindDef, ref core.Ref, expect string) (runGrant, error) {
	var g runGrant
	bad := provider.Said(provider.ClassInvalid, msg("run.invalid"))
	if len(expect) > maxGrantLen {
		return g, bad
	}
	route, rest, ok := strings.Cut(expect, "-")
	if !ok {
		return g, bad
	}
	enc, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return g, bad
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || !hmac.Equal([]byte(sig), []byte(runGrantSig(payload))) {
		return g, bad
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&g) != nil {
		return g, bad
	}
	want, ok := runJobName(g.Name, g.Job[max(0, len(g.Job)-5):])
	if route != g.Route || g.Action != actRunNow.ID || g.Route != routeOf(def) || g.NS != ref.Scope || g.Name != ref.Name || g.UID != ref.UID ||
		!ok || want != g.Job || g.Nonce == "" {
		return g, bad
	}
	return g, nil
}

// spentGrants remembers the run grants already spent until they expire.
type spentGrants struct {
	mu      sync.Mutex
	byNonce map[string]time.Time // → expiry
}

// has: g's nonce was spent (a replay of a review already run).
func (sg *spentGrants) has(nonce string) bool {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	_, ok := sg.byNonce[nonce]
	return ok
}

// spend decides, as one step, whether g may send its Job now: not
// expired, not spent, room to remember it; then it is spent — whatever the
// write's outcome. The clock is read under the lock: a time sampled before
// it could be older than a prune another spend already made.
func (sg *spentGrants) spend(g runGrant, clock func() time.Time) error {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	now := clock()
	exp := time.UnixMilli(g.Exp)
	if !now.Before(exp) {
		return provider.Said(provider.ClassConflict, msg("run.expired"))
	}
	for n, e := range sg.byNonce {
		if !now.Before(e) {
			delete(sg.byNonce, n)
		}
	}
	if _, ok := sg.byNonce[g.Nonce]; ok {
		return provider.Said(provider.ClassConflict, msg("run.spent"))
	}
	if len(sg.byNonce) >= maxSpentGrants {
		return provider.Said(provider.ClassUnavailable, msg("run.tooMany"))
	}
	sg.byNonce[g.Nonce] = exp
	return nil
}

// prepareRunNow names the Job, signs the grant and says what the run does.
func (s *session) prepareRunNow(def *kindDef, plan core.ActionPlan, u *unstructured.Unstructured) core.ActionPlan {
	o := u.Object
	job, ok := runJobName(u.GetName(), randomSuffix())
	if !ok {
		m := msg("cronjob.runNoName", "name", u.GetName())
		plan.Unavailable = &m
		return plan
	}
	plan.Expect = runGrant{
		Action: actRunNow.ID, Route: routeOf(def), NS: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID()),
		State: runState(u), Job: job, Exp: s.now().Add(runGrantTTL).UnixMilli(), Inc: s.incarnation, Nonce: randomHex(16),
	}.expect()
	limit := func(key string, def int64) int64 {
		if v, found, _ := unstructured.NestedFieldNoCopy(o, "spec", key); found && v != nil {
			return i64(o, "spec", key)
		}
		return def
	}
	policy := str(o, "spec", "concurrencyPolicy")
	if policy == "" {
		policy = "Allow"
	}
	plan.Effects = []core.Message{
		msg("cronjob.run", "job", job, "name", u.GetName()),
		msg("cronjob.runOwned", "succeeded", limit("successfulJobsHistoryLimit", 3), "failed", limit("failedJobsHistoryLimit", 1)),
		msg("cronjob.runConcurrency", "policy", policy),
		msg("cronjob.runUnexpected"),
	}
	if boolAt(o, "spec", "suspend") {
		plan.Warnings = append(plan.Warnings, msg("cronjob.runSuspended"))
	}
	if boolAt(o, "spec", "jobTemplate", "spec", "suspend") {
		plan.Warnings = append(plan.Warnings, msg("cronjob.runTemplateSuspended"))
	}
	if v, found, _ := unstructured.NestedFieldNoCopy(o, "spec", "jobTemplate", "spec", "parallelism"); found && v != nil && i64(o, "spec", "jobTemplate", "spec", "parallelism") == 0 {
		plan.Warnings = append(plan.Warnings, msg("cronjob.runNoParallelism"))
	}
	if n := len(slice(o, "status", "active")); n > 0 {
		plan.Warnings = append(plan.Warnings, countMsg("cronjob.runAlongsideOne", "cronjob.runAlongside", n))
	}
	return plan
}

// runJob: the Job kubectl create job --from=cronjob makes — the template's
// labels; instantiate=manual, then the template's annotations over it; the
// CronJob as its controller; the template's spec as it is.
func runJob(u *unstructured.Unstructured, name string) *unstructured.Unstructured {
	o := u.Object
	md := map[string]any{"name": name, "namespace": u.GetNamespace()}
	if l, ok := fieldAt(o, "spec", "jobTemplate", "metadata", "labels").(map[string]any); ok && len(l) > 0 {
		md["labels"] = runtimeDeepCopy(l)
	}
	ann := map[string]any{instantiateKey: "manual"}
	if a, ok := fieldAt(o, "spec", "jobTemplate", "metadata", "annotations").(map[string]any); ok {
		for k, v := range a {
			ann[k] = v
		}
	}
	md["annotations"] = ann
	md["ownerReferences"] = []any{map[string]any{"apiVersion": "batch/v1", "kind": "CronJob", "name": u.GetName(), "uid": string(u.GetUID()), "controller": true}}
	job := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": md}
	if spec, ok := fieldAt(o, "spec", "jobTemplate", "spec").(map[string]any); ok {
		job["spec"] = runtimeDeepCopy(spec)
	}
	return &unstructured.Unstructured{Object: job}
}

func runtimeDeepCopy(m map[string]any) map[string]any {
	return (&unstructured.Unstructured{Object: m}).DeepCopy().Object
}

// runNow: the grant checked, the CronJob read by UID and its state
// compared, the grant spent, then one POST of the Job — never again.
func (s *session) runNow(ctx context.Context, def *kindDef, run provider.ActionRun) (core.ActionResult, error) {
	g, err := readRunGrant(def, run.Ref, run.Expect)
	if err != nil {
		return core.ActionResult{}, err
	}
	if g.Inc != s.incarnation {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("run.earlier"))
	}
	if !s.now().Before(time.UnixMilli(g.Exp)) {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("run.expired"))
	}
	u, err := s.getConfirmed(ctx, def, run.Ref)
	if err != nil {
		return core.ActionResult{}, err
	}
	if why := actionUnavailable(def, run.Action, u); why != nil {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, *why)
	}
	if runState(u) != g.State {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("run.changed", "name", u.GetName()))
	}
	if s.beforeWrite != nil {
		s.beforeWrite(run.Action, u)
	}
	if err := ctx.Err(); err != nil {
		return core.ActionResult{}, provider.Said(provider.ClassUnavailable, msg("error.nothingWritten", "detail", err.Error()))
	}
	if err := s.spent.spend(g, s.now); err != nil {
		return core.ActionResult{}, err
	}
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	wctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	err = wr.create(wctx, jobsV1GVR, u.GetNamespace(), runJob(u, g.Job))
	if err == nil {
		return core.ActionResult{Message: msg("done.run", "job", g.Job, "name", u.GetName())}, nil
	}
	return core.ActionResult{}, runFailed(err, g.Job)
}

// runFailed classifies a failed create: nothing is ever sent again.
func runFailed(err error, job string) error {
	var se apierrors.APIStatus
	unknown := func(detail string) error {
		return provider.Said(provider.ClassUnknown, msg("run.unknown", "detail", detail, "job", job))
	}
	switch {
	case !errors.As(err, &se) && strings.Contains(err.Error(), "getting credentials"): // before sending
		class, m := classify(err)
		return &provider.Error{Class: class, Message: m}
	case !errors.As(err, &se):
		return unknown(err.Error())
	case ambiguous(err):
		return unknown(statusMessage(err))
	case apierrors.IsAlreadyExists(err):
		return provider.Said(provider.ClassConflict, msg("run.exists", "job", job))
	case apierrors.IsForbidden(err):
		return &provider.Error{Class: provider.ClassForbidden, Message: statusMessage(err)}
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		return &provider.Error{Class: provider.ClassInvalid, Message: statusMessage(err)}
	}
	class, m := classify(err)
	return &provider.Error{Class: class, Message: m}
}
