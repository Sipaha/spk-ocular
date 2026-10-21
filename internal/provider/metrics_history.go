package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// MetricsHistorySource is an optional UI-only, finite external history reader.
type MetricsHistorySource interface {
	ResourceMetricsHistory(context.Context, core.HistoryRequest) (core.MetricsHistory, error)
}
