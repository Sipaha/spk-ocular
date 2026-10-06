// Package appfiles embeds application resources (icons).
package appfiles

import _ "embed"

//go:embed icons/icon.png
var IconPNG []byte

// WindowIcons are rasterized at native panel sizes, avoiding a second scale
// of the 256px fallback. The combined X11 property stays below its request limit.
type WindowIcon struct {
	Size int
	PNG  []byte
}

//go:embed icons/icon-16.png
var icon16 []byte

//go:embed icons/icon-24.png
var icon24 []byte

//go:embed icons/icon-32.png
var icon32 []byte

//go:embed icons/icon-48.png
var icon48 []byte

//go:embed icons/icon-64.png
var icon64 []byte

//go:embed icons/icon-128.png
var icon128 []byte

var WindowIcons = []WindowIcon{
	{Size: 16, PNG: icon16},
	{Size: 24, PNG: icon24},
	{Size: 32, PNG: icon32},
	{Size: 48, PNG: icon48},
	{Size: 64, PNG: icon64},
	{Size: 128, PNG: icon128},
}
