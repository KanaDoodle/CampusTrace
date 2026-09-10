package migrations

import _ "embed"

//go:embed 001_init.sql
var SQL string

//go:embed 002_visibility.sql
var VisibilitySQL string

//go:embed 003_release_repair.sql
var ReleaseSQL string
