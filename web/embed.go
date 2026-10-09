package web

import "embed"

//go:embed loader.js drafts.js knowledge_bulk.js index.html knowledge.js knowledge.css company_decision.css company_decision.js company_chat.js agent.css agent_actions.js agent_harness.js agent_workspace.js practice.js ui.js navigation.js inventory.js todos.js source_catalog.js source_bulk.js radar.js app.js display.js models.js profile.js profile_education.js profile_local.js matching.js matching_tasks.js campaigns.js matching_decision.js matching_chat.js applications.js interviews.js evidence.js style.css workbench.css vendor/*
var Files embed.FS
