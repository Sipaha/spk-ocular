//go:build !windows

package desktop

// PageOrigin is the Origin the desktop page's requests carry (checked by
// docs/architecture.md): the
// loopback stream server allows exactly this one.
const PageOrigin = "wails://localhost"
