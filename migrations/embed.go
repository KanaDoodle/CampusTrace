package migrations

import _ "embed"

//go:embed 001_init.sql
var SQL string

//go:embed 002_visibility.sql
var VisibilitySQL string

//go:embed 003_release_repair.sql
var ReleaseSQL string

//go:embed 004_job_radar.sql
var RadarSQL string

//go:embed 005_matching.sql
var MatchingSQL string

//go:embed 006_backend_upgrade.sql
var BackendSQL string

//go:embed 007_local_reliability.sql
var LocalReliabilitySQL string

//go:embed 008_source_import.sql
var SourceImportSQL string

//go:embed 009_holistic_matching.sql
var HolisticSQL string

//go:embed 010_agent_workspace.sql
var AgentWorkspaceSQL string

//go:embed 011_agent_harness.sql
var AgentHarnessSQL string
