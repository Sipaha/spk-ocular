package provider

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
)

// HelperTracked handles receive the app's durable journal before registration.
type HelperTracked interface{ SetHelperJournal(HelperJournal) }
type HelperJournal interface {
	Begin(context.Context, core.OwnedHelper, context.CancelFunc) (HelperLease, error)
}
type HelperLease interface {
	PinUID(context.Context, string) error
	Finish(context.Context, bool) error
}

// HelperCleaner operates only through an existing explicit connection. pin
// persists an observed UID before a recovery delete when CREATE was ambiguous.
type HelperCleaner interface {
	CleanupOwnedHelper(context.Context, core.OwnedHelper, func(string) error) (bool, error)
}
