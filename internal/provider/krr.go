package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/krr"
)

// KRRRunner is a UI-only reviewed one-shot external-engine capability.
// Preparation performs reads only. Execution must durably journal before CREATE.
type KRRRunner interface {
	ReviewKRR(context.Context, krr.RunOptions) (krr.Review, error)
	PrepareKRR(context.Context, krr.Review, HelperJournal) (krr.Execution, error)
}
