// Package webui embeds the management console assets.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static returns the embedded static filesystem.
func Static() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
