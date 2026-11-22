// Package web embeds the handoff UI into the binary.
package web

import "embed"

// FS holds the UI; it is served from memory with no-store so a rebuilt binary
// never has to fight a stale browser cache.
//
//go:embed index.html favicon.svg fonts.css js styles fonts
var FS embed.FS
