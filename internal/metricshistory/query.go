// Package metricshistory implements bounded, explicit Prometheus range reads.
// It has no credential discovery, persistence, proxy inheritance or Kubernetes auth.
package metricshistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
)

const MaxContainers = 16

var uidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var labelPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func ValidUID(uid string) bool { return uidPattern.MatchString(uid) }

func Validate(source core.HistorySource, window string) error {
	u, e := url.Parse(source.URL)
	if e != nil || len(source.URL) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.Contains(u.Path, "..") || (u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1"))) {
		return errors.New("history requires HTTPS (or literal loopback HTTP), without URL credentials, query or fragment")
	}
	if len(source.Token) > 4096 {
		return errors.New("history credential exceeds limit")
	}
	for _, c := range source.Token {
		if c < 33 || c > 126 {
			return errors.New("invalid history credential")
		}
	}
	if window != "1h" && window != "6h" && window != "24h" {
		return errors.New("unsupported history window")
	}
	if source.Dedicated {
		if source.ClusterLabel != "" || source.ClusterValue != "" {
			return errors.New("choose dedicated source or exact cluster label")
		}
	} else {
		if !labelPattern.MatchString(source.ClusterLabel) || len(source.ClusterLabel) > 64 || strings.HasPrefix(source.ClusterLabel, "__") || len(source.ClusterValue) == 0 || len(source.ClusterValue) > 256 || strings.ContainsAny(source.ClusterValue, "\x00\r\n") {
			return errors.New("shared history requires an exact cluster label and value")
		}
		switch source.ClusterLabel {
		case "namespace", "pod", "container", "id":
			return errors.New("cluster label conflicts with workload identity")
		}
	}
	return nil
}

// The UID occupies a complete Pod cgroup segment. Systemd replaces UID hyphens
// with underscores; substring/name matching and current KSM joins are not used.
func identityPattern(uid string) string {
	return `^(/[^/]+)*/pod` + regexp.QuoteMeta(uid) + `/[^/]+$|^(/[^/]+)*/([^/]+-)?pod` + regexp.QuoteMeta(strings.ReplaceAll(uid, "-", "_")) + `\.slice/[^/]+$`
}

// Read admits at most 4 MiB total bodies, 32 series and 50000 points across three
// fixed queries. These are client admission bounds, not server evaluation limits.
func Read(ctx context.Context, req core.HistoryRequest, start, end int64, containers []string) ([]core.HistoryMetric, error) {
	if e := Validate(req.Source, req.Window); e != nil {
		return nil, e
	}
	if !ValidUID(req.Ref.UID) || len(containers) == 0 || len(containers) > MaxContainers || end < start || end-start > 86400 || start%60 != 0 || end%60 != 0 {
		return nil, errors.New("invalid history identity or interval")
	}
	idPattern := identityPattern(req.Ref.UID)
	idRE := regexp.MustCompile(idPattern)
	allowed := map[string]bool{}
	names := make([]string, len(containers))
	for i, n := range containers {
		if len(n) == 0 || len(n) > 63 || allowed[n] {
			return nil, errors.New("invalid history containers")
		}
		allowed[n] = true
		names[i] = regexp.QuoteMeta(n)
	}
	selector := `{namespace=` + strconv.Quote(req.Ref.Scope) + `,pod=` + strconv.Quote(req.Ref.Name) + `,container=~` + strconv.Quote(strings.Join(names, "|")) + `,id=~` + strconv.Quote(idPattern)
	if !req.Source.Dedicated {
		selector += `,` + req.Source.ClusterLabel + `=` + strconv.Quote(req.Source.ClusterValue)
	}
	selector += `}`
	queries := []struct{ name, query string }{
		{"cpu", `rate(container_cpu_usage_seconds_total` + selector + `[5m])`},
		{"memory", `container_memory_working_set_bytes` + selector},
		{"throttling", `rate(container_cpu_cfs_throttled_periods_total` + selector + `[5m]) / rate(container_cpu_cfs_periods_total` + selector + `[5m])`},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 8 * time.Second
	transport.MaxConnsPerHost = 1
	transport.DisableCompression = true
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	remaining := 4 << 20
	seriesLeft, pointsLeft := 32, 50000
	out := make([]core.HistoryMetric, 0, 3)
	for _, q := range queries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		m := core.HistoryMetric{Name: q.name, State: "unavailable", Series: []core.HistorySeries{}}
		u, _ := url.Parse(req.Source.URL)
		u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query_range"
		v := url.Values{"query": {q.query}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}, "step": {"60"}, "timeout": {"8s"}, "limit": {"33"}}
		u.RawQuery = v.Encode()
		r, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return nil, errors.New("invalid history source")
		}
		if req.Source.Token != "" {
			r.Header.Set("Authorization", "Bearer "+req.Source.Token)
		}
		r.Header.Set("Accept", "application/json")
		response, e := client.Do(r)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			out = append(out, m)
			continue
		}
		if response.StatusCode != http.StatusOK {
			if response.StatusCode == 401 || response.StatusCode == 403 {
				m.State = "forbidden"
			}
			_ = response.Body.Close()
			out = append(out, m)
			continue
		}
		if remaining <= 0 || response.ContentLength > int64(remaining) {
			m.State = "limited"
			_ = response.Body.Close()
			out = append(out, m)
			continue
		}
		b, e := io.ReadAll(io.LimitReader(response.Body, int64(remaining)+1))
		_ = response.Body.Close()
		if e != nil || len(b) > remaining {
			remaining = 0
			m.State = "limited"
			out = append(out, m)
			continue
		}
		remaining -= len(b)
		projected, state, usedSeries, usedPoints := project(b, req, idRE, allowed, start, end, seriesLeft, pointsLeft, q.name)
		m.Series, m.State = projected, state
		present := map[string]bool{}
		for _, series := range projected {
			for _, value := range series.Values {
				if value != nil {
					present[series.Container] = true
					break
				}
			}
		}
		for _, container := range containers {
			if !present[container] {
				m.MissingContainers = append(m.MissingContainers, container)
			}
		}
		if len(m.MissingContainers) > 0 && m.State == "available" {
			m.State = "partial"
		}
		seriesLeft -= usedSeries
		pointsLeft -= usedPoints
		out = append(out, m)
	}
	return out, nil
}

type matrixSeries struct {
	Metric     map[string]string   `json:"metric"`
	Values     [][]json.RawMessage `json:"values"`
	Histograms json.RawMessage     `json:"histograms"`
}

func project(b []byte, req core.HistoryRequest, idRE *regexp.Regexp, allowed map[string]bool, start, end int64, seriesLeft, pointsLeft int, name string) ([]core.HistorySeries, string, int, int) {
	empty := []core.HistorySeries{}
	var envelope struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Infos    []string `json:"infos"`
		Data     struct {
			ResultType string         `json:"resultType"`
			Result     []matrixSeries `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.Status != "success" || envelope.Data.ResultType != "matrix" || len(envelope.Warnings) > 0 || len(envelope.Infos) > 0 {
		return empty, "unavailable", 0, 0
	}
	rows := envelope.Data.Result
	if len(rows) > seriesLeft {
		return empty, "limited", 0, 0
	}
	count := int((end-start)/60) + 1
	total := 0
	partial := false
	out := make([]core.HistorySeries, 0, len(rows))
	seen := map[string]bool{}
	owners := map[string]map[int]int{}
	for _, row := range rows {
		labels := row.Metric
		labelBytes := 0
		for k, v := range labels {
			labelBytes += len(k) + len(v)
		}
		if len(labels) > 32 || labelBytes > 8192 || len(row.Histograms) > 0 || labels["namespace"] != req.Ref.Scope || labels["pod"] != req.Ref.Name || !allowed[labels["container"]] || !idRE.MatchString(labels["id"]) || (!req.Source.Dedicated && labels[req.Source.ClusterLabel] != req.Source.ClusterValue) {
			return empty, "unavailable", 0, 0
		}
		if len(row.Values) > count || total+len(row.Values) > pointsLeft {
			return empty, "limited", 0, 0
		}
		total += len(row.Values)
		// Keep incarnations separate. Never add duplicate scrape populations together.
		sum := sha256.Sum256([]byte(labels["id"]))
		key := hex.EncodeToString(sum[:8])
		seriesKey := labels["container"] + "/" + key
		if seen[seriesKey] {
			return empty, "unavailable", 0, 0
		}
		seen[seriesKey] = true
		s := core.HistorySeries{Container: labels["container"], Incarnation: key, Values: make([]*float64, count)}
		previous := start - 60
		for _, pair := range row.Values {
			if len(pair) != 2 {
				return empty, "unavailable", 0, 0
			}
			var timestamp float64
			var text string
			if json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &text) != nil || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) || timestamp != math.Trunc(timestamp) || timestamp < float64(start) || timestamp > float64(end) {
				return empty, "unavailable", 0, 0
			}
			at := int64(timestamp)
			if at <= previous || (at-start)%60 != 0 {
				return empty, "unavailable", 0, 0
			}
			previous = at
			value, e := strconv.ParseFloat(text, 64)
			index := int((at - start) / 60)
			if e != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || (name == "throttling" && value > 1) {
				partial = true
				continue
			}
			s.Values[index] = &value
			if owners[s.Container] == nil {
				owners[s.Container] = map[int]int{}
			}
			owners[s.Container][index]++
		}
		out = append(out, s)
	}
	have := false
	for i := range out {
		for j, p := range out[i].Values {
			if owners[out[i].Container][j] > 1 {
				out[i].Values[j] = nil
				partial = true
			} else if p != nil {
				have = true
			}
		}
	}
	if !have {
		return empty, "unavailable", len(rows), total
	}
	if partial {
		return out, "partial", len(rows), total
	}
	return out, "available", len(rows), total
}

// SourceDescription deliberately excludes authentication and arbitrary labels.
func SourceDescription(source core.HistorySource) string {
	u, _ := url.Parse(source.URL)
	return fmt.Sprint(u.Scheme, "://", u.Host, u.Path)
}
