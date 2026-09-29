package desktop

// PageOrigin is the Origin the desktop page's requests carry (checked by
// the log stream spike, docs/spikes/2026-09-29-log-stream-desktop.md): the
// loopback stream server allows exactly this one.
const PageOrigin = "wails://localhost"
