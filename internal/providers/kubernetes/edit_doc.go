package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// maxEditDepth bounds the nesting of an edited document.
const maxEditDepth = 100

// parseEditDoc reads the text of an edited object: exactly one YAML
// document whose root is a mapping, without duplicate keys, anchors or
// aliases, into JSON values — numbers as exact literals (json.Number), never
// through float64. Scalars follow YAML 1.2 (yes/on are strings).
func parseEditDoc(text string) (map[string]any, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the document is empty")
		}
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("not valid YAML: %w", err)
		}
		return nil, fmt.Errorf("line %d: only one document can be edited here", more.Line)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, errors.New("the document is empty")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: the object must be a mapping (key: value)", root.Line)
	}
	v, err := editValue(root, 0)
	if err != nil {
		return nil, err
	}
	return v.(map[string]any), nil
}

func editValue(n *yaml.Node, depth int) (any, error) {
	if depth > maxEditDepth {
		return nil, fmt.Errorf("line %d: nested too deep (over %d levels)", n.Line, maxEditDepth)
	}
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("line %d: anchors and aliases are not supported here", n.Line)
	}
	switch n.Kind {
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, val := n.Content[i], n.Content[i+1]
			if k.Tag == "!!merge" {
				return nil, fmt.Errorf("line %d: anchors and aliases are not supported here (<<)", k.Line)
			}
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return nil, fmt.Errorf("line %d: a key must be a string", k.Line)
			}
			if _, dup := out[k.Value]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, k.Value)
			}
			v, err := editValue(val, depth+1)
			if err != nil {
				return nil, err
			}
			out[k.Value] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := editValue(c, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		return editScalar(n)
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

func editScalar(n *yaml.Node) (any, error) {
	switch n.Tag {
	case "!!str", "!!timestamp":
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		return b, nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			return nil, fmt.Errorf("line %d: the number %s is out of range (64-bit integers)", n.Line, n.Value)
		}
		return json.Number(strconv.FormatInt(i, 10)), nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("line %d: %s is not a finite number", n.Line, n.Value)
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	}
	return nil, fmt.Errorf("line %d: the tag %s is not supported here", n.Line, n.Tag)
}

// mergePatch is the JSON merge patch (RFC 7386) that turns orig into
// edited: changed or new keys with their edited value, removed keys as
// null, maps compared key by key; anything else (lists, scalars) is
// replaced whole when it differs. Numbers compare as their literals.
func mergePatch(orig, edited map[string]any) map[string]any {
	out := map[string]any{}
	for k := range orig {
		if _, ok := edited[k]; !ok {
			out[k] = nil
		}
	}
	for k, e := range edited {
		o, had := orig[k]
		om, oIsMap := o.(map[string]any)
		em, eIsMap := e.(map[string]any)
		switch {
		case had && oIsMap && eIsMap:
			if p := mergePatch(om, em); len(p) > 0 {
				out[k] = p
			}
		case !had || !reflect.DeepEqual(o, e):
			out[k] = e
		}
	}
	return out
}
