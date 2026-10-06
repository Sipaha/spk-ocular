//go:build wails && (!linux || !gtk3)

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func configureWindowIcons(*application.WebviewWindow) {}
