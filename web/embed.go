package web

import "embed"

//go:embed index.html radar.js app.js display.js profile.js profile_local.js style.css vendor/*
var Files embed.FS
