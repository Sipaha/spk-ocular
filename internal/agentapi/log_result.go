package agentapi

import (
	"encoding/json"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/provider"
)

// boundLogJSON also counts JSON escaping and source metadata: a small text
// budget alone must not produce a large response for a many-container object.
func boundLogJSON(tail api.Tail, maxBytes int) api.Tail {
	short := func(value string, limit int) string {
		if len(value) <= limit {
			return value
		}
		tail.Truncated, tail.LimitReason = true, "byte_limit"
		return value[:limit] + "…"
	}
	state := func(value *provider.LogState) *provider.LogState {
		if value == nil {
			return nil
		}
		result := *value
		result.Message = short(result.Message, 512)
		return &result
	}
	tail.State = state(tail.State)
	sources := make([]api.TailSource, len(tail.Sources))
	for i, source := range tail.Sources {
		source.Label, source.Channel = short(source.Label, 512), short(source.Channel, 128)
		source.State = state(source.State)
		sources[i] = source
	}
	tail.TotalSources = len(sources)
	lines := tail.Lines
	build := func(n int, issues bool) api.Tail {
		candidate := tail
		candidate.Lines = lines[len(lines)-n:]
		if candidate.Lines == nil {
			candidate.Lines = []api.TailLine{}
		}
		needed := map[int]bool{}
		for _, line := range candidate.Lines {
			needed[line.Source] = true
		}
		candidate.Sources = []api.TailSource{}
		extra := 0
		for _, source := range sources {
			problem := source.State != nil && (source.State.State == provider.LogError || source.State.State == provider.LogGap || source.State.State == provider.LogLimited || source.State.State == provider.LogTruncated)
			if needed[source.ID] || issues && problem && extra < 8 {
				candidate.Sources = append(candidate.Sources, source)
				if !needed[source.ID] {
					extra++
				}
			}
		}
		if n < len(lines) {
			candidate.Truncated, candidate.LimitReason = true, "byte_limit"
		}
		return candidate
	}
	fits := func(value api.Tail) bool {
		data, _ := json.MarshalIndent(value, "", "  ")
		return len(data)+1 <= maxBytes
	}
	issues := fits(build(0, true))
	if !issues {
		tail.Truncated, tail.LimitReason = true, "byte_limit"
	}
	low, high := 0, len(lines)
	for low < high {
		mid := (low + high + 1) / 2
		if fits(build(mid, issues)) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return build(low, issues)
}
