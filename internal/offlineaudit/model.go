// Package offlineaudit admits external audit evidence without live UID binding.
package offlineaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxBytes = 1 << 20
const MaxObjects = 128
const MaxChecks = 2048

var ErrInvalid = errors.New("unsupported or malformed Polaris report, or import limit exceeded")

type Check struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	Container string `json:"container,omitempty"`
	Success   bool   `json:"success"`
	Severity  string `json:"severity"`
	Category  string `json:"category"`
}
type Subject struct {
	Kind      string  `json:"kind"`
	Namespace string  `json:"namespace"`
	Name      string  `json:"name"`
	Checks    []Check `json:"checks"`
}
type Report struct {
	AuditTime  string    `json:"auditTime,omitempty"`
	SourceType string    `json:"sourceType"`
	Subjects   []Subject `json:"subjects"`
	Checks     int       `json:"checks"`
}

// jsonValue bounds the temporary tree and rejects duplicate decoded keys,
// including keys in discarded data. Parse errors never echo source contents.
func jsonValue(d *json.Decoder, depth int, tokens *int) (any, error) {
	*tokens++
	if depth > 32 || *tokens > 100000 {
		return nil, ErrInvalid
	}
	t, e := d.Token()
	if e != nil {
		return nil, ErrInvalid
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	if depth >= 32 {
		return nil, ErrInvalid
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for d.More() {
			*tokens++
			if *tokens > 100000 {
				return nil, ErrInvalid
			}
			k, e := d.Token()
			if e != nil {
				return nil, ErrInvalid
			}
			key, ok := k.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, exists := out[key]; exists {
				return nil, ErrInvalid
			}
			v, e := jsonValue(d, depth+1, tokens)
			if e != nil {
				return nil, e
			}
			out[key] = v
		}
		*tokens++
		end, e := d.Token()
		if e != nil || end != json.Delim('}') || *tokens > 100000 {
			return nil, ErrInvalid
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			v, e := jsonValue(d, depth+1, tokens)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		*tokens++
		end, e := d.Token()
		if e != nil || end != json.Delim(']') || *tokens > 100000 {
			return nil, ErrInvalid
		}
		return out, nil
	default:
		return nil, ErrInvalid
	}
}

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$`)
var namespaceName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var kindName = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,63}$`)
var ruleID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

func object(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func identifier(v any, pattern *regexp.Regexp, maxLen int, empty bool) (string, bool) {
	s, ok := v.(string)
	return s, ok && len(s) <= maxLen && ((empty && s == "") || pattern.MatchString(s))
}

// Parse accepts the operator-declared v10.2.5 format-1.0 JSON shape. The
// format marker authenticates neither the binary release nor the audit target.
// Compatibility was inspected at upstream c84bb2ea3674ee7ec044584db8c9cc9e8d17bdd0.
func Parse(data []byte, version string) (Report, error) {
	bad := func() (Report, error) { return Report{}, ErrInvalid }
	if version != "10.2.5" || len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tokens := 0
	v, e := jsonValue(d, 0, &tokens)
	if e != nil {
		return bad()
	}
	if _, e = d.Token(); !errors.Is(e, io.EOF) {
		return bad()
	}
	root, ok := object(v)
	if !ok || root["PolarisOutputVersion"] != "1.0" {
		return bad()
	}
	// Reject conflicting release/schema envelopes rather than guessing precedence.
	for _, key := range []string{"version", "schemaVersion", "polarisVersion", "Version", "SchemaVersion", "PolarisVersion"} {
		if _, exists := root[key]; exists {
			return bad()
		}
	}
	source, ok := root["SourceType"].(string)
	if !ok || (source != "Cluster" && source != "Path") {
		return bad()
	}
	subjects, ok := root["Results"].([]any)
	if !ok || len(subjects) > MaxObjects {
		return bad()
	}
	out := Report{SourceType: source, Subjects: []Subject{}}
	if at, exists := root["AuditTime"]; exists && at != nil {
		raw, ok := at.(string)
		if !ok || len(raw) > 64 {
			return bad()
		}
		if raw != "" {
			parsed, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil {
				return bad()
			}
			if parsed.Year() > 1 {
				out.AuditTime = parsed.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	for _, raw := range subjects {
		m, ok := object(raw)
		if !ok {
			return bad()
		}
		subject := Subject{Checks: []Check{}}
		if subject.Kind, ok = identifier(m["Kind"], kindName, 64, false); !ok {
			return bad()
		}
		if subject.Namespace, ok = identifier(m["Namespace"], namespaceName, 63, true); !ok {
			return bad()
		}
		if subject.Name, ok = identifier(m["Name"], dnsName, 253, false); !ok {
			return bad()
		}
		if !appendChecks(&subject, m["Results"], "resource", "", &out.Checks) {
			return bad()
		}
		if pod, exists := m["PodResult"]; exists && pod != nil {
			p, ok := object(pod)
			if !ok {
				return bad()
			}
			if !appendChecks(&subject, p["Results"], "pod", "", &out.Checks) {
				return bad()
			}
			if containers, exists := p["ContainerResults"]; exists && containers != nil {
				cs, ok := containers.([]any)
				if !ok || len(cs) > 16 {
					return bad()
				}
				// Same-name container occurrences are not merged: this format has no role.
				for _, c := range cs {
					cm, ok := object(c)
					if !ok {
						return bad()
					}
					name, ok := identifier(cm["Name"], namespaceName, 63, false)
					if !ok {
						return bad()
					}
					if !appendChecks(&subject, cm["Results"], "container", name, &out.Checks) {
						return bad()
					}
				}
			}
		}
		out.Subjects = append(out.Subjects, subject)
	}
	return out, nil
}
func appendChecks(subject *Subject, raw any, scope, container string, count *int) bool {
	m, ok := object(raw)
	if !ok || len(m) > MaxChecks-*count {
		return false
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		c, ok := object(m[key])
		if !ok || !ruleID.MatchString(key) || c["ID"] != key {
			return false
		}
		success, ok := c["Success"].(bool)
		if !ok {
			return false
		}
		severity, ok := c["Severity"].(string)
		if !ok || (severity != "warning" && severity != "danger" && severity != "ignore") {
			return false
		}
		category := "unknown"
		if raw, exists := c["Category"]; exists {
			s, ok := raw.(string)
			if !ok || len(s) > 256 {
				return false
			}
			for _, known := range []string{"Security", "Networking", "Resources", "Health Checks", "Images", "Efficiency", "Reliability", "General"} {
				if s == known {
					category = s
					break
				}
			}
		}
		subject.Checks = append(subject.Checks, Check{ID: key, Scope: scope, Container: container, Success: success, Severity: severity, Category: category})
		*count++
	}
	return true
}

// ValidContext bounds operator assertions; no source name is an identity proof.
func ValidContext(target, revision string) bool {
	return target != "" && len(target) <= 1024 && revision != "" && len(revision) <= 256 && utf8.ValidString(target+revision) && !strings.ContainsFunc(target+revision, unicode.IsControl)
}
