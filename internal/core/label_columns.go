package core

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

const MaxLabelColumns = 8
const MaxLabelValueBytes = 256

// LabelValue is finite metadata evidence, not a path into an object. Missing
// means the key was absent; unavailable means no trustworthy value was read.
type LabelValue struct {
	State string `json:"state"` // value, missing, unavailable, limit
	Value string `json:"value,omitempty"`
}

func ValidLabelKeys(keys []string) error {
	if len(keys) > MaxLabelColumns {
		return errors.New("too many label columns")
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if len(key) == 0 || len(key) > 253 || !utf8.ValidString(key) || seen[key] {
			return errors.New("invalid or duplicate label key")
		}
		for _, r := range key {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return errors.New("invalid label key")
			}
		}
		seen[key] = true
	}
	return nil
}
