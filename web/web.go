// Package web holds the viewer: plain ES modules and CSS, type-checked with
// `tsc --checkJs` and served as they are (no build step).
package web

import "embed"

// Files is the viewer, embedded into the binary.
//
//go:embed *.html *.js *.css
var Files embed.FS
