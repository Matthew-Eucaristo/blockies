// Package examples holds the example boards shipped with the binary.
package examples

import (
	"embed"
	"io/fs"
	"slices"
	"strings"
)

//go:embed *.blk
var files embed.FS

// Names lists the examples, sorted.
func Names() []string {
	entries, _ := fs.ReadDir(files, ".")
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".blk"))
	}
	slices.Sort(out)
	return out
}

// Get returns an example script by name.
func Get(name string) (string, bool) {
	b, err := files.ReadFile(name + ".blk")
	return string(b), err == nil
}
