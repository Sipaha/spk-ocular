//go:build !wails

package main

import (
	"context"
	"errors"
)

func runDesktop(context.Context, browserOpts) error {
	return errors.New("desktop mode requires building with: go build -tags \"wails gtk3\" ./cmd/spk-ocular (or run with --browser)")
}
