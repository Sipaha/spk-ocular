package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	yamlv3 "go.yaml.in/yaml/v3"
	"sigs.k8s.io/yaml"
)

// Comparison pins two explicitly opened objects and their admitted connections.
// It reads provider-safe resource YAML, never editor drafts or revealed values.
type CompareSelection struct {
	Ref          core.Ref `json:"ref"`
	ConfigRev    string   `json:"configRev"`
	ConnectionID uint64   `json:"connectionId"`
}
type CompareRequest struct {
	Left  CompareSelection `json:"left"`
	Right CompareSelection `json:"right"`
}
type ComparisonSide struct {
	Ref            core.Ref `json:"ref"`
	YAML           string   `json:"yaml"`
	FullYAML       string   `json:"fullYAML"`
	CapturedAt     int64    `json:"capturedAt"`
	Omitted        []string `json:"omitted"`
	ValuesExcluded bool     `json:"valuesExcluded"`
}
type ResourceComparison struct {
	Left  ComparisonSide `json:"left"`
	Right ComparisonSide `json:"right"`
}

const comparisonBytes = 1 << 20
const comparisonLines = 20000

func compareError(code string) error {
	why := apiMessage("compareUnavailable")
	return &CodedError{Code: code, Detail: why.Text, Why: &why}
}

func (s *Service) CompareResources(ctx context.Context, req CompareRequest) (ResourceComparison, error) {
	if !fromUI(ctx) {
		return ResourceComparison{}, compareError("forbidden")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	selections := []CompareSelection{req.Left, req.Right}
	entries := make([]*sessionEntry, 2)
	for i, sel := range selections {
		r := sel.Ref
		if r.Provider == "" || r.Target == "" || r.Kind == "" || r.Name == "" || r.UID == "" || sel.ConfigRev == "" || sel.ConnectionID == 0 {
			return ResourceComparison{}, compareError(CodeBadRequest)
		}
		for _, field := range []string{r.Provider, r.Target, r.Kind, r.Name, r.Scope, r.UID, sel.ConfigRev} {
			if len(field) > 4096 || strings.ContainsRune(field, 0) {
				return ResourceComparison{}, compareError(CodeBadRequest)
			}
		}
		e, err := s.sessionFor(ctx, r.Provider, r.Target)
		if err != nil {
			return ResourceComparison{}, err
		}
		s.sessMu.Lock()
		a := s.connections[ownerKey(r.Provider, r.Target)]
		valid := a != nil && a.status.ID == sel.ConnectionID && a.owner == e.owner && a.status.State == "connected" && s.configRev(e.hash) == sel.ConfigRev
		s.sessMu.Unlock()
		if !valid {
			return ResourceComparison{}, compareError(CodeConflict)
		}
		entries[i] = e
	}
	var out ResourceComparison
	sides := []*ComparisonSide{&out.Left, &out.Right}
	for i, sel := range selections {
		resource, err := entries[i].sess.Get(ctx, sel.Ref)
		if err != nil {
			return ResourceComparison{}, fromProvider(err)
		}
		if resource == nil || resource.Ref.Kind != sel.Ref.Kind || resource.Ref.Name != sel.Ref.Name || resource.Ref.Scope != sel.Ref.Scope || resource.Ref.UID != sel.Ref.UID {
			return ResourceComparison{}, compareError(CodeGone)
		}
		side, err := normalizeComparison(resource.YAML, sel.Ref.Provider == "kubernetes", sel.Ref.Kind == "secrets")
		if err != nil {
			return ResourceComparison{}, err
		}
		side.Ref = resource.Ref
		side.Ref.Provider = sel.Ref.Provider
		side.Ref.Target = sel.Ref.Target
		side.CapturedAt = s.now().UnixMilli()
		*sides[i] = side
	}
	// Revalidate both configuration and session incarnations after all reads.
	for i, sel := range selections {
		e, err := s.sessionFor(ctx, sel.Ref.Provider, sel.Ref.Target)
		if err != nil {
			return ResourceComparison{}, err
		}
		s.sessMu.Lock()
		a := s.connections[ownerKey(sel.Ref.Provider, sel.Ref.Target)]
		valid := a != nil && a.status.ID == sel.ConnectionID && a.owner == e.owner && a.status.State == "connected"
		s.sessMu.Unlock()
		if e != entries[i] || !valid {
			return ResourceComparison{}, compareError(CodeGone)
		}
	}
	if ctx.Err() != nil {
		return ResourceComparison{}, ctx.Err()
	}
	return out, nil
}

func normalizeComparison(text string, kubernetes, secret bool) (ComparisonSide, error) {
	fail := func() (ComparisonSide, error) { return ComparisonSide{}, compareError(CodeUnsupported) }
	if len(text) > comparisonBytes || strings.Count(text, "\n") > comparisonLines {
		return fail()
	}
	// Provider-generated YAML has no aliases. Reject them and deep/wide documents
	// before conversion so custom provider output cannot expand without a bound.
	var root yamlv3.Node
	dec := yamlv3.NewDecoder(strings.NewReader(text))
	if dec.Decode(&root) != nil || len(root.Content) != 1 || root.Content[0].Kind != yamlv3.MappingNode {
		return fail()
	}
	var extra yamlv3.Node
	if !errors.Is(dec.Decode(&extra), io.EOF) {
		return fail()
	}
	count := 0
	var bounded func(*yamlv3.Node, int) bool
	bounded = func(n *yamlv3.Node, depth int) bool {
		count++
		if depth > 64 || count > 100000 || n.Kind == yamlv3.AliasNode {
			return false
		}
		for _, child := range n.Content {
			if !bounded(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !bounded(&root, 0) {
		return fail()
	}
	raw, err := yaml.YAMLToJSONStrict([]byte(text))
	if err != nil {
		return fail()
	}
	var object map[string]any
	jd := json.NewDecoder(bytes.NewReader(raw))
	jd.UseNumber()
	if jd.Decode(&object) != nil || object == nil {
		return fail()
	}
	side := ComparisonSide{Omitted: []string{}}
	// Secret payloads are never compared, including size masks from Resource.Get.
	// Shape-based detection also protects providers exposing core/v1 Secret YAML.
	if secret || (object["apiVersion"] == "v1" && object["kind"] == "Secret") {
		delete(object, "data")
		delete(object, "stringData")
		delete(object, "binaryData")
		side.ValuesExcluded = true
	}
	encode := func() (string, error) {
		data, e := json.Marshal(object)
		if e != nil {
			return "", e
		}
		data, e = yaml.JSONToYAML(data)
		if e != nil || len(data) > comparisonBytes || bytes.Count(data, []byte("\n")) > comparisonLines {
			return "", errors.New("comparison limit")
		}
		return string(data), nil
	}
	if side.FullYAML, err = encode(); err != nil {
		return fail()
	}
	if kubernetes {
		if _, ok := object["status"]; ok {
			delete(object, "status")
			side.Omitted = append(side.Omitted, "status")
		}
		if meta, ok := object["metadata"].(map[string]any); ok {
			for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "managedFields", "selfLink"} {
				if _, ok := meta[key]; ok {
					delete(meta, key)
					side.Omitted = append(side.Omitted, "metadata."+key)
				}
			}
		}
	}
	if side.YAML, err = encode(); err != nil {
		return fail()
	}
	return side, nil
}
