# Dependency and upstream notice

Release preparation review: 2026-09-10. This document describes the public dependency boundary; remote clean-clone results are recorded separately in the release verification report.

## KanaRPC

- CampusTrace is the independent module `github.com/KanaDoodle/CampusTrace`.
- Its only direct KanaRPC import is `github.com/KanaDoodle/KanaRPC-Go/rpc`. CampusTrace does not import KanaRPC `internal` packages or copy their implementation.
- `go.mod` requires the published `github.com/KanaDoodle/KanaRPC-Go v0.1.0` and contains no `replace` directive.
- An optional ignored development `go.work` can point to `../KanaRPC-Go`. Release builds use `GOWORK=off` and the published tag; a local workspace is not a release dependency.
- KanaRPC retains the source and Git history of [KamaRPC-Go](https://github.com/youngyangyang04/KamaRPC-Go). Its inherited upstream commit is `0e678f3e0704d1015e8db2c7e1a5b10491938038`. The public facade and reliability improvements are published in `v0.1.0`, commit `02581e2370799302b34182f32c05cd48ee482741`.
- CampusTrace's [LICENSE](../LICENSE) is byte-for-byte identical to the retained KanaRPC license: **GNU Affero General Public License, Version 3 (AGPL v3)**. Earlier local audit prose incorrectly called this GPL. Neither license text has been changed. Retain the upstream source attribution and license when distributing the projects.

The public facade provides registry construction, registration, discovery/readiness, context-aware invocation and server lifecycle. It filters breaker-open instances before per-service selection and does not replay a transmitted business call. Registration recovery uses a new lease after KeepAlive loss. The wire protocol has no early remote cancellation message: CampusTrace carries an explicit deadline and derives a service-lifecycle context.

## Other dependencies

Exact versions are recorded in `go.mod` and `go.sum`, including the official MCP Go SDK v1.7.0, go-sql-driver/mysql, go-redis/v9, golang-jwt/jwt/v5, x/crypto and x/net. Third-party modules retain their own licenses; this notice does not relicense them.

The local Compose stack uses MySQL 8.4, Redis 7.4 and etcd 3.6.7. Example credentials and seed fixtures are explicitly for synthetic local demonstrations. Do not commit real environment files, user databases, resumes or interview data.

## Publication gate

The maintainer repositories are [KanaRPC-Go](https://github.com/KanaDoodle/KanaRPC-Go) and [CampusTrace](https://github.com/KanaDoodle/CampusTrace). Acceptance of a CampusTrace commit requires a real remote clone with `GOWORK=off`, a fresh module cache, no sibling checkout, full checks and the documented demo. A successful push or local replacement build alone does not resolve F01.
