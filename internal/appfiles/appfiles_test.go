package appfiles

import (
	"bytes"
	"image/png"
	"testing"
)

func TestWindowIconFitsGTKX11(t *testing.T) {
	cfg, err := png.DecodeConfig(bytes.NewReader(IconPNG))
	if err != nil {
		t.Fatal(err)
	}
	// GTK/X11 silently omits _NET_WM_ICON for oversized images. 512x512
	// decoded successfully and had WM_HINTS pixmaps, but no taskbar icon.
	if cfg.Width != cfg.Height || cfg.Width < 32 || cfg.Width > 256 {
		t.Fatalf("window icon must be square, 32..256 px for GTK/X11; got %dx%d", cfg.Width, cfg.Height)
	}
}
