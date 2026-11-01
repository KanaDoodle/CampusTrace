# Dependency and upstream notice

Reviewed for the KanaRPC migration on 2026-11-01. Exact versions and checksums are recorded in `go.mod` and `go.sum`.

## KanaRPC

- CampusTrace is the independent module `github.com/KanaDoodle/CampusTrace`.
- Analysis uses the public root, `discovery` and `routing` packages of `github.com/KanaDoodle/KanaRPC`. No SDK `internal` package is imported or copied.
- The dependency is pinned to `v0.0.0-20261031172914-7b63b80f0544`, corresponding to public commit [`7b63b80f0544`](https://github.com/KanaDoodle/KanaRPC/commit/7b63b80f05449c2033b6b3a18648a510b43a6d2c). This is a Go pseudo-version, not a release tag. `go.mod` contains no `replace`; release checks run with `GOWORK=off`.
- Optional `make dev-workspace` links the sibling `../KanaRPC` checkout through an ignored `go.work`. Release and container builds use the pinned module from GitHub, without that checkout.
- The new repository maintains an independent KNR2 implementation and Git history. The retired KanaRPC-Go fork preserves its earlier KamaRPC-Go history. The maintainer records that lineage and third-party sources in [KanaRPC ORIGINS.md](https://github.com/KanaDoodle/KanaRPC/blob/7b63b80f05449c2033b6b3a18648a510b43a6d2c/ORIGINS.md).
- CampusTrace and KanaRPC retain the standard **GNU Affero General Public License, Version 3 (AGPL v3)**. Third-party modules retain their own licenses and notices.

The adapter uses Context-first unary JSON calls over internal TCP, native cancellation, deadline propagation, bounded method scheduling, etcd freshness/ownership checks, connection reuse and graceful drain. Protobuf, server streaming, TLS and QUIC are SDK capabilities; the Analysis deployment uses unary JSON/TCP. Ordinary deployments keep the RPC and etcd listeners on loopback or the private Compose network.

KNR2 is incompatible with the retired protocol. Its discovery namespace is separate; upgrade Worker and both Analysis processes together. [Migration and acceptance](kanarpc-migration.md) documents the operational sequence and result-unknown rules. Search remains independent of the RPC and Core database clients.

## Other dependencies

Dependencies include the official MCP Go SDK v1.7.0, go-sql-driver/mysql, go-redis/v9, franz-go, golang-jwt/jwt/v5 and OpenTelemetry. The new SDK raises the selected etcd client to 3.7.3, Protobuf runtime to 1.36.12 and gRPC to 1.83.2 through Go module version selection. The isolated integration suite tests that client against the deployed etcd 3.6.7 server.

The local stack uses MySQL 8.4, Redis 7.4, Kafka 4.4.0 and etcd 3.6.7. Example credentials and seed fixtures belong to synthetic local demonstrations. Real environment files, user databases, resumes and interview data stay outside version control.

## Reproduction

```sh
GOWORK=off go mod download
GOWORK=off go mod verify
sh scripts/verify-release-boundary.sh
python3 scripts/verify-integration-isolation.py --race --count 1 --adversarial
```

The boundary check rejects the retired module and development replacements, builds the application, and checks Search's independent dependency graph. Container acceptance resolves the pinned module without sibling sources; local test results and remote CI results are recorded separately.
