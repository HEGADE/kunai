// Package webui embeds the built Svelte PWA so the whole client ships inside the
// single kunai binary. The SvelteKit app (in ../../web) is configured to build
// into this package's dist/ directory.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded PWA file tree rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time; this cannot fail
	}
	return sub
}
