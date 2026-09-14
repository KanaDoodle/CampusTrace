package web

import "embed"

//go:embed index.html radar.js app.js display.js style.css
var Files embed.FS
