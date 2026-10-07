package appfiles

import (
	"bytes"
	"image/png"
	"os"
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

func TestPanelIconFamilyFitsNativeSizesAndX11Payload(t *testing.T) {
	total := 0
	for _, icon := range WindowIcons {
		cfg, err := png.DecodeConfig(bytes.NewReader(icon.PNG))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != icon.Size || cfg.Height != icon.Size {
			t.Fatalf("invalid %dpx icon dimensions", icon.Size)
		}
		total += 2 + cfg.Width*cfg.Height
	}
	// X11 encodes two dimensions and 32-bit ARGB pixels per representation.
	// Leave headroom for the property request header.
	if total*4 >= 250000 {
		t.Fatalf("window icon family exceeds safe X11 request payload: %d bytes", total*4)
	}
}

func TestBrowserIconUsesCanonicalProductMark(t *testing.T) {
	source, err := os.ReadFile("icons/icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	browser, err := os.ReadFile("../../web/public/icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, browser) {
		t.Fatal("browser favicon differs from the canonical product SVG; run scripts/render-icons.mjs")
	}
}
