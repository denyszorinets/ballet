//go:build bindata

package webui

import (
	"embed"
	"io/fs"
)

// dist is the built SPA, copied here by `make bundle` before building.
//
//go:embed all:dist
var dist embed.FS

// Bundled returns the SPA embedded in the binary.
func Bundled() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed directive guarantees the directory
	}
	return sub
}
