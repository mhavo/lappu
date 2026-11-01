// Package lappu embeds the viewer served by the Go server.
package lappu

import (
	"embed"
	"io/fs"
)

//go:embed public
var public embed.FS

// Public returns the viewer files (index.html etc.) rooted at public/.
func Public() fs.FS {
	sub, err := fs.Sub(public, "public")
	if err != nil {
		panic(err)
	}
	return sub
}
