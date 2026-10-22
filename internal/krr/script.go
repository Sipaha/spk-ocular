package krr

import _ "embed"

// RunnerScript is executed only inside the pinned external KRR image. Ocular
// neither bundles a Python runtime nor invokes the host's Python interpreter.
//
//go:embed runner.py
var RunnerScript string

const Image = "robustadev/krr@sha256:ce666bf65215ec3cbe4366df5e935ffb0c10a9aaaccaeeb879e289a0f4d67da1"
