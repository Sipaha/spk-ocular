// Package appfiles embeds application resources (icons).
package appfiles

import _ "embed"

//go:embed icons/icon.png
var IconPNG []byte
