//go:build wails

package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"testing"
)

func TestDetachedLogSize(t *testing.T) {
	for _, tc := range []struct {
		name          string
		screen        *application.Screen
		width, height int
	}{
		{"no screen", nil, 1100, 750},
		{"Windows 100 percent", &application.Screen{WorkArea: application.Rect{Width: 1920, Height: 1040}, ScaleFactor: 1}, 1920, 936},
		{"Windows 150 percent", &application.Screen{WorkArea: application.Rect{Width: 1707, Height: 920}, PhysicalWorkArea: application.Rect{Width: 2560, Height: 1380}, ScaleFactor: 1.5}, 1707, 828},
		{"macOS Retina", &application.Screen{WorkArea: application.Rect{Width: 1496, Height: 967}, PhysicalWorkArea: application.Rect{Width: 2992, Height: 1934}, ScaleFactor: 2}, 1496, 870},
		{"Linux 200 percent", &application.Screen{WorkArea: application.Rect{Width: 960, Height: 520}, ScaleFactor: 2}, 960, 468},
		{"bounds fallback", &application.Screen{Bounds: application.Rect{Width: 1280, Height: 720}, ScaleFactor: 2}, 1280, 648},
		{"size fallback", &application.Screen{Size: application.Size{Width: 800, Height: 600}, ScaleFactor: 2}, 800, 540},
		{"invalid screen", &application.Screen{}, 1100, 750},
		{"small work area", &application.Screen{WorkArea: application.Rect{Width: 320, Height: 200}}, 320, 180},
	} {
		t.Run(tc.name, func(t *testing.T) {
			width, height := detachedLogSize(tc.screen)
			if width != tc.width || height != tc.height {
				t.Fatalf("got %dx%d; want %dx%d", width, height, tc.width, tc.height)
			}
		})
	}
}

func TestDetachedLogScreenUsesNormalizedMonitor(t *testing.T) {
	primary := &application.Screen{ID: "primary", Bounds: application.Rect{Width: 1920, Height: 1080}, WorkArea: application.Rect{Width: 1920, Height: 1040}, IsPrimary: true}
	retina := &application.Screen{ID: "retina", Bounds: application.Rect{X: 1920, Width: 1496, Height: 1000}, WorkArea: application.Rect{X: 1920, Width: 1496, Height: 967}, ScaleFactor: 2}
	screens := []*application.Screen{primary, retina}
	// macOS's current-window descriptor can still carry device-pixel bounds.
	rawRetina := &application.Screen{ID: "retina", Bounds: application.Rect{X: 3840, Width: 2992, Height: 2000}, ScaleFactor: 2}
	if got := detachedLogScreen("darwin", rawRetina, screens, primary); got != retina {
		t.Fatal("Retina descriptor was not resolved by monitor ID")
	}
	// GTK uses the window ID and reports monitor geometry as WorkArea.
	rawGTK := &application.Screen{ID: "primary", Bounds: retina.Bounds, WorkArea: retina.Bounds, ScaleFactor: 2}
	if got := detachedLogScreen("linux", rawGTK, screens, primary); got != retina {
		t.Fatal("GTK descriptor used a window ID instead of matching monitor bounds")
	}
	if got := detachedLogScreen("windows", &application.Screen{ID: "retina"}, screens, primary); got != retina {
		t.Fatal("Windows secondary monitor was lost")
	}
	if got := detachedLogScreen("linux", &application.Screen{ID: "unknown"}, screens, primary); got != primary {
		t.Fatal("unknown monitor must fall back to primary")
	}
	if got := detachedLogScreen("darwin", nil, nil, nil); got != nil {
		t.Fatal("missing screens must retain size fallback")
	}
}

func TestDetachedToolSizeStaysNormalAndWithinWorkArea(t *testing.T) {
	for _, tc := range []struct {
		kind          string
		screen        *application.Screen
		width, height int
	}{
		{"shell", nil, 960, 600}, {"files", nil, 1200, 800},
		{"shell", &application.Screen{WorkArea: application.Rect{Width: 1920, Height: 1040}, ScaleFactor: 2}, 960, 600},
		{"shell", &application.Screen{WorkArea: application.Rect{Width: 800, Height: 500}}, 720, 450},
		{"files", &application.Screen{WorkArea: application.Rect{Width: 800, Height: 500}}, 720, 450},
	} {
		width, height := detachedToolSize(tc.screen, tc.kind)
		if width != tc.width || height != tc.height {
			t.Fatalf("%s: got%dx%d want%dx%d", tc.kind, width, height, tc.width, tc.height)
		}
	}
}
