package web

import "embed"

//go:embed index.html ui.js navigation.js todos.js source_catalog.js radar.js app.js display.js models.js profile.js profile_education.js profile_local.js matching.js matching_tasks.js campaigns.js matching_decision.js matching_chat.js applications.js interviews.js evidence.js style.css vendor/*
var Files embed.FS
