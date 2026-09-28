package web

import "embed"

//go:embed index.html ui.js radar.js app.js display.js models.js profile.js profile_local.js matching.js matching_decision.js matching_chat.js applications.js evidence.js style.css vendor/*
var Files embed.FS
