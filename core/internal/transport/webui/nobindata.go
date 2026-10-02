//go:build !bindata

package webui

import "io/fs"

// Bundled returns nil: this binary was built without the bindata tag, so it
// embeds no SPA (serve one with web.dir, or use the Vite dev server).
func Bundled() fs.FS { return nil }
