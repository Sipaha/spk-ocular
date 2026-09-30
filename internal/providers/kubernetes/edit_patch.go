package kubernetes

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// lastAppliedKey is kubectl's annotation: hidden in the editor and never
// changed by it.
const lastAppliedKey = "kubectl.kubernetes.io/last-applied-configuration"

// editIdentity is the object an edit is of (captured from the signed base,
// never from the texts).
type editIdentity struct {
	APIVersion, Kind, Name, Namespace, UID string
	// Secret: only metadata is edited; values are masked and never written.
	Secret bool
}

// check: doc is this object (apiVersion, kind, name, namespace, uid).
func (id editIdentity) check(doc map[string]any) error {
	meta, _ := doc["metadata"].(map[string]any)
	got := []any{doc["apiVersion"], doc["kind"], meta["name"], meta["namespace"], meta["uid"]}
	want := []string{id.APIVersion, id.Kind, id.Name, id.Namespace, id.UID}
	for i, w := range want {
		g, _ := got[i].(string)
		if got[i] == nil && w == "" {
			continue // cluster-scoped: no namespace
		}
		if g != w || meta == nil {
			return fmt.Errorf("this is another object: apiVersion, kind, name, namespace and uid cannot change (edit %s %s)", id.Kind, id.Name)
		}
	}
	return nil
}

// secretMask is maskSecret's placeholder.
var secretMask = regexp.MustCompile(`^<\d+ bytes>$`)

// buildEditPatch is the merge patch from the shown document (orig) to the
// edited one, for the object id: identity kept, the version in the text
// ignored, hidden paths (status, managedFields, last-applied) neither
// present nor removed through an ancestor, a Secret's values untouched.
func buildEditPatch(id editIdentity, orig, edited map[string]any) (map[string]any, error) {
	if err := id.check(orig); err != nil {
		return nil, err
	}
	if err := id.check(edited); err != nil {
		return nil, err
	}
	if _, ok := edited["status"]; ok {
		return nil, errors.New("status is not edited here: remove it from the text")
	}
	meta := edited["metadata"].(map[string]any)
	if _, ok := meta["managedFields"]; ok {
		return nil, errors.New("metadata.managedFields is not edited here: remove it from the text")
	}
	if ann, ok := meta["annotations"].(map[string]any); ok {
		if _, ok := ann[lastAppliedKey]; ok {
			return nil, fmt.Errorf("the annotation %s is not edited here: remove it from the text", lastAppliedKey)
		}
	}
	patch := mergePatch(withoutVersion(orig), withoutVersion(edited))
	if pm, ok := patch["metadata"].(map[string]any); ok {
		if a, has := pm["annotations"]; has {
			switch a.(type) {
			case nil:
				// All shown annotations removed: remove those, not the
				// hidden ones (a null would take last-applied too).
				del := map[string]any{}
				om, _ := orig["metadata"].(map[string]any)
				oa, _ := om["annotations"].(map[string]any)
				for k := range oa {
					del[k] = nil
				}
				pm["annotations"] = del
			case map[string]any:
			default:
				return nil, errors.New("metadata.annotations must be a mapping")
			}
		}
	}
	if id.Secret {
		for k := range patch {
			if k != "metadata" {
				return nil, fmt.Errorf("the values of a Secret are not edited here (%s): only its metadata is", k)
			}
		}
		if p := findMask(patch, ""); p != "" {
			return nil, fmt.Errorf("a masked Secret value cannot be written (%s)", p)
		}
	}
	return patch, nil
}

// withoutVersion: doc without metadata.resourceVersion (the provider sets
// the version; the one in the text is ignored).
func withoutVersion(doc map[string]any) map[string]any {
	meta, ok := doc["metadata"].(map[string]any)
	if !ok {
		return doc
	}
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	m := make(map[string]any, len(meta))
	for k, v := range meta {
		if k != "resourceVersion" {
			m[k] = v
		}
	}
	out["metadata"] = m
	return out
}

// findMask: the path of a masked value anywhere in v ("" if none).
func findMask(v any, path string) string {
	switch t := v.(type) {
	case string:
		if secretMask.MatchString(t) {
			return strings.TrimPrefix(path, ".")
		}
	case map[string]any:
		for k, c := range t {
			if p := findMask(c, path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		for i, c := range t {
			if p := findMask(c, fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
	}
	return ""
}

// collisions: the paths the patch writes (replaces or deletes) whose value
// changed between the shown document (orig) and the current object — the
// edit would overwrite those changes. Sorted.
func collisions(patch, orig, current map[string]any) []string {
	var out []string
	var walk func(p, o, c map[string]any, path string)
	walk = func(p, o, c map[string]any, path string) {
		for k, pv := range p {
			ov, oHas := o[k]
			cv, cHas := c[k]
			at := strings.TrimPrefix(path+"."+k, ".")
			if pm, ok := pv.(map[string]any); ok {
				om, oMap := ov.(map[string]any)
				cm, cMap := cv.(map[string]any)
				if oMap && cMap {
					walk(pm, om, cm, at)
					continue
				}
				if !oHas && !cHas {
					continue // new on both sides of the base: nothing overwritten
				}
			}
			if oHas != cHas || !reflect.DeepEqual(ov, cv) {
				out = append(out, at)
			}
		}
	}
	walk(patch, orig, current, "")
	sort.Strings(out)
	return out
}

// editHeader opens the editor's text: what it does not show.
const editHeader = "# status, metadata.managedFields and the last-applied annotation are not shown;\n# the editor does not change them. null removes a key; lists are replaced whole.\n"

// editView is the document the editor shows for u: without status,
// managedFields and the last-applied annotation, a Secret's values masked.
// u itself is not changed.
func editView(u *unstructured.Unstructured, secret bool) (string, error) {
	o := u.DeepCopy().Object
	delete(o, "status")
	unstructured.RemoveNestedField(o, "metadata", "managedFields")
	unstructured.RemoveNestedField(o, "metadata", "annotations", lastAppliedKey)
	if ann, ok, _ := unstructured.NestedMap(o, "metadata", "annotations"); ok && len(ann) == 0 {
		unstructured.RemoveNestedField(o, "metadata", "annotations")
	}
	if secret {
		maskSecret(o)
	}
	o, back := exactDecimals(o)
	y, err := yaml.Marshal(o)
	if err != nil {
		return "", err
	}
	return editHeader + back(string(y)), nil
}

// exactDecimals: sigs.k8s.io/yaml reads numbers back through float64 on
// the way to YAML, so a decimal literal of an edit (json.Number, as sent)
// would show rounded — 9007199254740993.0 as 9.007199254740992e+15. Each
// one is swapped (in a copy) for a marker the emitter keeps as a plain
// word; back puts the literals in its place. A local overlay then shows
// the value it sends (an unknown backend may keep it exactly); integers
// are exact without this.
func exactDecimals(o map[string]any) (map[string]any, func(string) string) {
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	prefix := "ocularnum" + hex.EncodeToString(nonce[:]) + "x"
	var lits []string
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, x := range t {
				out[k] = walk(x)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, x := range t {
				out[i] = walk(x)
			}
			return out
		case json.Number:
			if !strings.ContainsAny(string(t), ".eE") {
				return t
			}
			lits = append(lits, string(t))
			return prefix + strconv.Itoa(len(lits)-1)
		}
		return v
	}
	out := walk(o).(map[string]any)
	return out, func(text string) string {
		if len(lits) == 0 {
			return text
		}
		// One pass: a marker is a whole scalar, so the digits after the
		// prefix are all its index (marker 1 never cuts marker 10 short).
		var b strings.Builder
		b.Grow(len(text))
		for {
			i := strings.Index(text, prefix)
			if i < 0 {
				break
			}
			j := i + len(prefix)
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			n, err := strconv.Atoi(text[i+len(prefix) : j])
			b.WriteString(text[:i])
			if err == nil && n < len(lits) {
				b.WriteString(lits[n])
			} else {
				b.WriteString(text[i:j])
			}
			text = text[j:]
		}
		b.WriteString(text)
		return b.String()
	}
}
