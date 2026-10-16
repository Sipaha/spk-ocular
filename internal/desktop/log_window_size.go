//go:build wails

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// Wails Screen.WorkArea, Bounds and Size are already device-independent units.
// Multiplying or dividing by ScaleFactor again breaks Retina and Windows HiDPI.
func detachedLogSize(screen *application.Screen) (width, height int) {
	width, height = 1100, 750
	if screen == nil {
		return
	}
	area := screen.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		area = screen.Bounds
	}
	if area.Width <= 0 || area.Height <= 0 {
		area.Width, area.Height = screen.Size.Width, screen.Size.Height
	}
	if area.Width > 0 && area.Height > 0 {
		width, height = area.Width, max(1, area.Height*90/100)
	}
	return
}

// Resolve GetScreen's platform-specific descriptor through the normalized
// ScreenManager cache. Raw descriptors can have physical bounds (macOS) or a
// geometry-only work area and window ID instead of monitor ID (GTK).
func detachedLogScreen(platform string, current *application.Screen, screens []*application.Screen, primary *application.Screen) *application.Screen {
	if current != nil {
		if platform != "linux" && current.ID != "" {
			for _, screen := range screens {
				if screen.ID == current.ID {
					return screen
				}
			}
		}
		for _, screen := range screens {
			if screen.Bounds.Width > 0 && screen.Bounds.Height > 0 && screen.Bounds == current.Bounds {
				return screen
			}
		}
	}
	return primary
}
