// Package web embeds the built frontend (web/dist) into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:generate npm run build

//go:embed all:dist
var dist embed.FS

// Assets returns the built frontend rooted at dist/.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
