package web

import "embed"

//go:embed index.html app.js display.js style.css
var Files embed.FS
