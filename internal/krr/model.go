// Package krr admits external KRR evidence without establishing live identity.
package krr

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxBytes = 1 << 20
const MaxRows = 256

var ErrInvalid = errors.New("unsupported or malformed KRR report, or import limit exceeded")

type Quantity struct {
	State string  `json:"state"`
	Value float64 `json:"value,omitempty"`
}
type Setting struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}
type Row struct {
	Cluster    string     `json:"cluster,omitempty"`
	Kind       string     `json:"kind"`
	Namespace  string     `json:"namespace"`
	Name       string     `json:"name"`
	Container  string     `json:"container"`
	Current    []Quantity `json:"current"`
	Proposed   []Quantity `json:"proposed"`
	Severities []string   `json:"severities"`
	Severity   string     `json:"severity"`
	Reasons    []string   `json:"reasons"`
	Warnings   []string   `json:"warnings"`
	HPA        string     `json:"hpa"`
}
type Report struct {
	Minimums []Quantity `json:"minimums"`
	Rows     []Row      `json:"rows"`
	Settings []Setting  `json:"settings"`
	Errors   int        `json:"errors"`
}

// decode bounds tokens/depth and rejects duplicate decoded keys. Input is never
// retained by the report registry. Decoder errors never become user diagnostics.
func decode(d *json.Decoder, depth int, tokens *int) (any, error) {
	*tokens++
	if depth > 32 || *tokens > 100000 {
		return nil, ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	delim, is := t.(json.Delim)
	if !is {
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
			if _, dup := out[key]; dup {
				return nil, ErrInvalid
			}
			v, e := decode(d, depth+1, tokens)
			if e != nil {
				return nil, e
			}
			out[key] = v
		}
		*tokens++
		if *tokens > 100000 {
			return nil, ErrInvalid
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, ErrInvalid
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			v, e := decode(d, depth+1, tokens)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		*tokens++
		if *tokens > 100000 {
			return nil, ErrInvalid
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, ErrInvalid
		}
		return out, nil
	default:
		return nil, ErrInvalid
	}
}
func obj(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func text(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" || len(s) > 256 || !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl) {
		return "", false
	}
	return s, true
}
func numeric(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, e := strconv.ParseFloat(string(n), 64)
	return f, e == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= 9007199254740991
}
func quantity(m map[string]any, key string) (Quantity, bool) {
	v, exists := m[key]
	if !exists {
		return Quantity{State: "unknown"}, true
	}
	if v == nil {
		return Quantity{State: "unset"}, true
	}
	if v == "?" {
		return Quantity{State: "unavailable"}, true
	}
	if f, ok := numeric(v); ok {
		return Quantity{State: "number", Value: f}, true
	}
	return Quantity{}, false
}
func oneOf(v any, choices ...string) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, c := range choices {
		if s == c {
			return s, true
		}
	}
	return "", false
}
func severity(v any) (string, bool) { return oneOf(v, "UNKNOWN", "GOOD", "OK", "WARNING", "CRITICAL") }

var settingKeys = []string{"cpu_percentile", "memory_buffer_percentage", "points_required", "allow_hpa", "use_oomkill_data", "oom_memory_buffer_percentage", "history_duration", "timeframe_duration"}

func settings(raw any) ([]Setting, bool) {
	m, ok := obj(raw)
	if !ok || len(m) != len(settingKeys) {
		return nil, false
	}
	out := []Setting{}
	for _, k := range settingKeys {
		v, exists := m[k]
		if !exists {
			return nil, false
		}
		if k == "allow_hpa" || k == "use_oomkill_data" {
			b, ok := v.(bool)
			if !ok {
				return nil, false
			}
			out = append(out, Setting{k, b})
			continue
		}
		f, ok := numeric(v)
		if !ok {
			return nil, false
		}
		switch k {
		case "cpu_percentile":
			ok = f > 0 && f <= 100
		case "memory_buffer_percentage":
			ok = f > 0 && f <= 10000
		case "oom_memory_buffer_percentage":
			ok = f <= 10000
		case "points_required":
			ok = f >= 1 && f <= 1e9 && math.Trunc(f) == f
		case "history_duration":
			ok = f >= 1 && f <= 1e6
		case "timeframe_duration":
			ok = f > 0 && f <= 1e6
		}
		if !ok {
			return nil, false
		}
		out = append(out, Setting{k, f})
	}
	return out, true
}

// Parse supports only the operator-declared v1.31.0/simple projection. The
// upstream document has no authenticated version discriminator or UID binding.
func Parse(data []byte, version string) (Report, error) {
	bad := func() (Report, error) { return Report{}, ErrInvalid }
	if version != "1.31.0" || len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	count := 0
	v, e := decode(d, 0, &count)
	if e != nil {
		return bad()
	}
	if _, e = d.Token(); !errors.Is(e, io.EOF) {
		return bad()
	}
	root, ok := obj(v)
	if !ok {
		return bad()
	}
	if _, exists := root["version"]; exists {
		return bad()
	}
	if _, exists := root["schemaVersion"]; exists {
		return bad()
	}
	strategy, ok := obj(root["strategy"])
	if !ok || strategy["name"] != "simple" {
		return bad()
	}
	ss, ok := settings(strategy["settings"])
	if !ok {
		return bad()
	}
	resources, ok := root["resources"].([]any)
	if !ok || len(resources) != 2 {
		return bad()
	}
	cpuMemory := resources[0] == "cpu" && resources[1] == "memory"
	memoryCPU := resources[0] == "memory" && resources[1] == "cpu"
	if !cpuMemory && !memoryCPU {
		return bad()
	}
	scans, ok := root["scans"].([]any)
	if !ok || len(scans) > MaxRows {
		return bad()
	}
	errs, ok := root["errors"].([]any)
	if !ok {
		return bad()
	}
	minimums := []Quantity{{State: "unknown"}, {State: "unknown"}}
	if raw, exists := root["config"]; exists {
		config, valid := obj(raw)
		if !valid {
			return bad()
		}
		for i, key := range []string{"cpu_min_value", "memory_min_value"} {
			if value, exists := config[key]; exists {
				number, valid := numeric(value)
				if !valid || number > 1e9 || math.Trunc(number) != number {
					return bad()
				}
				minimums[i] = Quantity{State: "number", Value: number}
			}
		}
	}
	out := Report{Rows: []Row{}, Settings: ss, Minimums: minimums, Errors: len(errs)}
	for _, raw := range scans {
		scan, ok := obj(raw)
		if !ok {
			return bad()
		}
		subject, ok := obj(scan["object"])
		if !ok {
			return bad()
		}
		current, ok := obj(subject["allocations"])
		if !ok {
			return bad()
		}
		proposed, ok := obj(scan["recommended"])
		if !ok {
			return bad()
		}
		row := Row{Current: []Quantity{}, Proposed: []Quantity{}, Severities: []string{}, Reasons: []string{}, Warnings: []string{}, HPA: "unknown"}
		if row.Kind, ok = oneOf(subject["kind"], "Deployment", "DaemonSet", "StatefulSet", "Job", "CronJob", "Rollout", "DeploymentConfig", "StrimziPodSet", "GroupedJob"); !ok {
			return bad()
		}
		if row.Namespace, ok = text(subject["namespace"]); !ok {
			return bad()
		}
		if row.Name, ok = text(subject["name"]); !ok {
			return bad()
		}
		if row.Container, ok = text(subject["container"]); !ok {
			return bad()
		}
		cluster, exists := subject["cluster"]
		if !exists {
			return bad()
		}
		if cluster != nil {
			if row.Cluster, ok = text(cluster); !ok {
				return bad()
			}
		}
		if row.Severity, ok = severity(scan["severity"]); !ok {
			return bad()
		}
		if hpa, exists := subject["hpa"]; exists {
			if hpa == nil {
				row.HPA = "absent"
			} else if _, ok = obj(hpa); ok {
				row.HPA = "present"
			} else {
				return bad()
			}
		}
		for _, selector := range []string{"requests", "limits"} {
			old, ok := obj(current[selector])
			if !ok {
				return bad()
			}
			next, ok := obj(proposed[selector])
			if !ok {
				return bad()
			}
			for _, resource := range []string{"cpu", "memory"} {
				q, ok := quantity(old, resource)
				if !ok {
					return bad()
				}
				row.Current = append(row.Current, q)
				sev := "UNKNOWN"
				if rec, is := obj(next[resource]); is {
					if _, exists := rec["value"]; !exists {
						return bad()
					}
					q, ok = quantity(rec, "value")
					if !ok {
						return bad()
					}
					sev, ok = severity(rec["severity"])
					if !ok {
						return bad()
					}
				} else {
					q, ok = quantity(next, resource)
					if !ok {
						return bad()
					}
				}
				row.Proposed = append(row.Proposed, q)
				row.Severities = append(row.Severities, sev)
			}
		}
		info, ok := obj(proposed["info"])
		if !ok {
			return bad()
		}
		for _, resource := range []string{"cpu", "memory"} {
			if reason := info[resource]; reason != nil {
				if _, ok = reason.(string); !ok {
					return bad()
				}
				literal, known := oneOf(reason, "No data", "Not enough data", "HPA detected", "OOMKill detected")
				if !known {
					literal = "unknown"
				}
				row.Reasons = append(row.Reasons, literal)
			}
		}
		warnings, ok := subject["warnings"].([]any)
		if !ok || len(warnings) > 16 {
			return bad()
		}
		for _, warning := range warnings {
			literal, known := oneOf(warning, "NoPrometheusPods", "NoPrometheusCPUMetrics", "NoPrometheusMemoryMetrics")
			if !known {
				literal = "unknown"
			}
			row.Warnings = append(row.Warnings, literal)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}
