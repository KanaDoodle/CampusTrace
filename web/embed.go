package web

import "embed"

//go:embed journey.js journey.css preferences.js notifications.js radar_browse.js loader.js deadlines.js auth.js drafts.js knowledge_bulk.js index.html knowledge.js knowledge.css company_decision.css company_decision.js company_chat.js agent.css agent_actions.js agent_harness.js agent_workspace.js practice.js ui.js navigation.js inventory.js todos.js source_catalog.js source_bulk.js source_watches.js radar.js app.js display.js models.js profile.js profile_education.js profile_local.js matching.js matching_tasks.js campaigns.js matching_decision.js matching_chat.js applications.js interviews.js evidence.js style.css workbench.css vendor/*
var Files embed.FS
