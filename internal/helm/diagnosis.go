package helm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	yaml "go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/action"
	release "helm.sh/helm/v4/pkg/release/v1"
)

// Diagnosis contains stored observations, never an assertion about current hook execution.
type Diagnosis struct {
	fingerprint string
	Selected    Release      `json:"selected"`
	History     []Release    `json:"history"`
	Hooks       []HookRecord `json:"hooks"`
	Truncated   bool         `json:"truncated"`
	CompareFrom int          `json:"compareFrom"`
	CompareTo   int          `json:"compareTo"`
	CapturedAt  time.Time    `json:"capturedAt"`
}
type HookRecord struct {
	Index          int       `json:"index"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	APIVersion     string    `json:"apiVersion"`
	Namespace      string    `json:"namespace"`
	Phase          string    `json:"phase"`
	StartedAt      time.Time `json:"startedAt"`
	CompletedAt    time.Time `json:"completedAt"`
	Events         []string  `json:"events"`
	DeletePolicies []string  `json:"deletePolicies"`
	Coverage       string    `json:"coverage"`
}
type RevisionComparison struct {
	From  int    `json:"from"`
	To    int    `json:"to"`
	Left  string `json:"left"`
	Right string `json:"right"`
}
type FluxAssociation struct {
	Ref        core.Ref                 `json:"ref"`
	State      string                   `json:"state"`
	Source     string                   `json:"source"`
	Conditions []core.DiagnosisEvidence `json:"conditions"`
}
type FluxContext struct {
	Candidates []FluxAssociation `json:"candidates"`
	Coverage   string            `json:"coverage"`
	CapturedAt int64             `json:"capturedAt"`
}

func stored(a *action.Configuration, name string, revision int) (*release.Release, error) {
	g := action.NewGet(a)
	g.Version = revision
	raw, err := g.Run(name)
	if err != nil {
		return nil, errors.New("helm revision could not be read")
	}
	r, err := asRelease(raw)
	if err != nil || r.Name != name || (revision > 0 && r.Version != revision) {
		return nil, errors.New("helm revision identity changed")
	}
	return r, nil
}
func Diagnose(ctx context.Context, a *action.Configuration, name string, revision int) (Diagnosis, error) {
	out := Diagnosis{History: []Release{}, Hooks: []HookRecord{}}
	r, err := stored(a, name, revision)
	if err != nil {
		return out, err
	}
	out.Selected = diagnosisSummary(r, &out.Truncated)
	out.fingerprint, err = releaseFingerprint(r)
	if err != nil {
		return Diagnosis{}, errors.New("invalid stored revision")
	}
	h := action.NewHistory(a)
	h.Max = 129
	history, err := h.Run(name)
	if err != nil {
		return out, errors.New("helm history could not be read")
	}
	for _, raw := range history {
		v, e := asRelease(raw)
		if e != nil || v.Name != name {
			return Diagnosis{}, errors.New("invalid Helm history")
		}
		out.History = append(out.History, diagnosisSummary(v, &out.Truncated))
	}
	sort.Slice(out.History, func(i, j int) bool { return out.History[i].Revision > out.History[j].Revision })
	if len(out.History) > 128 {
		out.History = out.History[:128]
		out.Truncated = true
	}
	out.CompareTo = r.Version
	for _, v := range out.History {
		if v.Revision < r.Version && (v.Status == "deployed" || v.Status == "superseded") {
			out.CompareFrom = v.Revision
			break
		}
	}
	total := 0
	for i, h := range r.Hooks {
		if h == nil {
			out.Truncated = true
			continue
		}
		if len(out.Hooks) >= 32 {
			out.Truncated = true
			break
		}
		rec := HookRecord{Index: i, Name: h.Name, Kind: h.Kind, Phase: string(h.LastRun.Phase), StartedAt: h.LastRun.StartedAt, CompletedAt: h.LastRun.CompletedAt, Events: []string{}, DeletePolicies: []string{}, Coverage: "unsupported"}
		for _, e := range h.Events {
			if len(rec.Events) >= 32 {
				out.Truncated = true
				break
			}
			rec.Events = append(rec.Events, string(e))
		}
		for _, p := range h.DeletePolicies {
			if len(rec.DeletePolicies) >= 32 {
				out.Truncated = true
				break
			}
			rec.DeletePolicies = append(rec.DeletePolicies, string(p))
		}
		total += len(h.Manifest)
		if len(h.Manifest) <= 256<<10 && total <= 1<<20 {
			docs, e := parseDocuments(h.Manifest, 256<<10, 1)
			if e == nil && len(docs) == 1 {
				obj := docs[0]
				rec.APIVersion = nodeString(obj, "apiVersion")
				rec.Kind = nodeString(obj, "kind")
				rec.Name = nodeString(nodeValue(obj, "metadata"), "name")
				rec.Namespace = nodeString(nodeValue(obj, "metadata"), "namespace")
				if rec.Namespace == "" {
					rec.Namespace = r.Namespace
				}
				if rec.Name != "" && rec.Namespace == r.Namespace && ((rec.Kind == "Job" && rec.APIVersion == "batch/v1") || (rec.Kind == "Pod" && rec.APIVersion == "v1")) {
					rec.Coverage = "supported"
				}
			}
		} else {
			rec.Coverage = "partial"
			out.Truncated = true
		}
		for _, p := range []*string{&rec.Name, &rec.Kind, &rec.APIVersion, &rec.Namespace, &rec.Phase} {
			if len(*p) > 2048 {
				*p = ""
				rec.Coverage = "partial"
				out.Truncated = true
			}
		}
		for i := range rec.Events {
			rec.Events[i] = boundedObservation(rec.Events[i], &out.Truncated)
		}
		for i := range rec.DeletePolicies {
			rec.DeletePolicies[i] = boundedObservation(rec.DeletePolicies[i], &out.Truncated)
		}
		out.Hooks = append(out.Hooks, rec)
	}
	if err = ctx.Err(); err != nil {
		return Diagnosis{}, err
	}
	if err = out.Verify(a); err != nil {
		return Diagnosis{}, err
	}
	out.CapturedAt = time.Now()
	return out, nil
}

// Compare uses stored renderings, rejecting changed records instead of silently rebasing.
func Compare(ctx context.Context, a *action.Configuration, name string, from, to int) (RevisionComparison, error) {
	out := RevisionComparison{From: from, To: to}
	if from < 1 || to < 1 || from == to {
		return out, errors.New("select two distinct stored revisions")
	}
	records := make([]*release.Release, 2)
	fingerprints := make([]string, 2)
	for i, v := range []int{from, to} {
		r, e := stored(a, name, v)
		if e != nil {
			return RevisionComparison{}, e
		}
		records[i] = r
		fingerprints[i], e = releaseFingerprint(r)
		if e != nil {
			return RevisionComparison{}, errors.New("invalid release record")
		}
	}
	var err error
	out.Left, err = NormalizeManifest(records[0].Manifest)
	if err != nil {
		return RevisionComparison{}, err
	}
	out.Right, err = NormalizeManifest(records[1].Manifest)
	if err != nil {
		return RevisionComparison{}, err
	}
	for i, v := range []int{from, to} {
		r, e := stored(a, name, v)
		if e != nil {
			return RevisionComparison{}, e
		}
		f, e := releaseFingerprint(r)
		if e != nil || f != fingerprints[i] {
			return RevisionComparison{}, errors.New("stored revision changed during comparison")
		}
	}
	if err = ctx.Err(); err != nil {
		return RevisionComparison{}, err
	}
	return out, nil
}
func nodeValue(n *yaml.Node, key string) *yaml.Node {
	if n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
	}
	return nil
}
func nodeString(n *yaml.Node, key string) string {
	v := nodeValue(n, key)
	if v != nil && v.Kind == yaml.ScalarNode && v.Tag == "!!str" {
		return v.Value
	}
	return ""
}
func parseDocuments(text string, maxBytes, maxDocs int) ([]*yaml.Node, error) {
	fail := errors.New("stored manifest exceeds limits or contains unsupported YAML")
	if len(text) > maxBytes {
		return nil, fail
	}
	dec := yaml.NewDecoder(strings.NewReader(text))
	docs := []*yaml.Node{}
	nodes := 0
	var walk func(*yaml.Node, int) bool
	walk = func(n *yaml.Node, depth int) bool {
		nodes++
		if nodes > 100000 || depth > 64 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return false
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
					return false
				}
				seen[k.Value] = true
			}
		}
		for _, c := range n.Content {
			if !walk(c, depth+1) {
				return false
			}
		}
		return true
	}
	for {
		var root yaml.Node
		err := dec.Decode(&root)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || !walk(&root, 0) {
			return nil, fail
		}
		if len(root.Content) == 0 {
			continue
		}
		n := root.Content[0]
		if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
			continue
		}
		if len(docs) >= maxDocs || n.Kind != yaml.MappingNode {
			return nil, fail
		}
		docs = append(docs, n)
	}
	return docs, nil
}

// NormalizeManifest redacts only core Secret objects. Unknown schemas can still contain sensitive plaintext.
func NormalizeManifest(text string) (string, error) {
	docs, err := parseDocuments(text, 4<<20, 256)
	if err != nil {
		return "", err
	}
	var project func(*yaml.Node)
	project = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			kind, api := nodeString(n, "kind"), nodeString(n, "apiVersion")
			if kind == "SecretList" && api == "v1" {
				items := nodeValue(n, "items")
				if items != nil && items.Kind == yaml.SequenceNode {
					for _, item := range items.Content {
						if item.Kind == yaml.MappingNode {
							secretIdentity(item)
						}
					}
				}
				secretIdentity(n)
				n.Content[3].Value = "SecretList"
				if items != nil {
					n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "items"}, items)
				}
			}
			if kind == "Secret" && api == "v1" {
				secretIdentity(n)
			}
		}
		n.HeadComment = ""
		n.LineComment = ""
		n.FootComment = ""
		for _, c := range n.Content {
			project(c)
		}
		if n.Kind == yaml.MappingNode {
			pairs := make([][2]*yaml.Node, 0, len(n.Content)/2)
			for i := 0; i+1 < len(n.Content); i += 2 {
				pairs = append(pairs, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
			}
			sort.Slice(pairs, func(i, j int) bool { return pairs[i][0].Value < pairs[j][0].Value })
			n.Content = nil
			for _, p := range pairs {
				n.Content = append(n.Content, p[0], p[1])
			}
		}
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	for _, n := range docs {
		project(n)
		if err = enc.Encode(n); err != nil {
			return "", errors.New("stored manifest could not be normalized")
		}
		if b.Len() > 1<<20 {
			return "", errors.New("normalized manifest exceeds 1 MiB")
		}
	}
	if err = enc.Close(); err != nil {
		return "", fmt.Errorf("manifest normalization failed")
	}
	if strings.Count(b.String(), "\n") > 20000 {
		return "", errors.New("normalized manifest exceeds 20000 lines")
	}
	return b.String(), nil
}

// Verify rejects an external replacement of the selected stored hook/revision.
func (d Diagnosis) Verify(a *action.Configuration) error {
	r, err := stored(a, d.Selected.Name, d.Selected.Revision)
	if err != nil {
		return err
	}
	f, err := releaseFingerprint(r)
	if err != nil || f != d.fingerprint {
		return errors.New("stored revision changed during observation")
	}
	return nil
}

func boundedObservation(text string, truncated *bool) string {
	if len(text) > 2048 {
		*truncated = true
		return ""
	}
	return text
}
func diagnosisSummary(r *release.Release, truncated *bool) Release {
	out := summary(r)
	for _, p := range []*string{&out.Name, &out.Namespace, &out.Chart, &out.ChartVersion, &out.AppVersion, &out.Status} {
		*p = boundedObservation(*p, truncated)
	}
	return out
}

func secretIdentity(n *yaml.Node) {
	identity := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	meta := nodeValue(n, "metadata")
	for _, k := range []string{"name", "namespace"} {
		if v := nodeValue(meta, k); v != nil && v.Kind == yaml.ScalarNode && v.Tag == "!!str" {
			identity.Content = append(identity.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, v)
		}
	}
	n.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "apiVersion"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "v1"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "kind"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "Secret"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "metadata"}, identity}
}
