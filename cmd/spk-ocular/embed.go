package main

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// frontendFS returns the built SPA rooted at dist/. In a plain `go build`
// (no frontend built) dist holds only .gitkeep and the UI is empty — the
// Makefile copies frontend/dist here before building.
func frontendFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
