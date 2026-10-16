// Package web holds the browser frontend and embeds its production build.
//
// The source lives in src/ and is built by Vite into dist/, which this
// package embeds. Only dist/.gitkeep is committed, so a binary built without
// running the web build serves a page saying so instead of failing.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built frontend rooted at the dist directory.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// The directory is embedded at compile time, so this cannot fail
		// unless the embed directive and this name disagree.
		panic("web: dist is not embedded: " + err.Error())
	}
	return sub
}
