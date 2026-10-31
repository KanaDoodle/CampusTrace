# CampusTrace

**A personal graduate job-search tool built in Go: track recruiting changes, compare roles, preserve applications, and turn interview reviews into preparation.**

[中文](README.md) | **English** · [Architecture](docs/architecture.md) · [Engineering evidence](docs/engineering-evidence.md) · [Release acceptance](docs/verification/release-20261031/README.md)

```text
Sources / manual imports → Search and role selection → Application plans and frozen materials
                                                               ↓
Next interview preparation ← Local study notes ← Interview records and reviews
```

Original text, extracted evidence and assessments remain separate. Whether a job is open, whether a candidate meets identified requirements, and overall relevance are different judgments. Model analysis reads complete reviewed materials; users inspect outbound text before starting it.

## Three backend problems

| Problem | Implementation | Evidence |
|---|---|---|
| A saved job must reach Search and notifications independently, including duplicate delivery | Transactional MySQL outbox, Kafka consumer groups, projection versions and durable notification receipts; Redis keeps task scheduling | [Job events](docs/job-events.md): lost acknowledgements, independent outages, replay and tombstones |
| Search failure must preserve authorization and bound database load | Foreground and remote admission limits, one bounded background recovery probe, budgeted SQL fallback; Core revalidates ACL, versions, hashes and generations | [Failure isolation](docs/search-resilience.md), [Trace / EXPLAIN / pprof diagnosis](docs/verification/search-diagnosis-20261031/README.md) |
| Multiple APIs share paid-model capacity; a crashed process cannot prove whether a request executed | Fair permits, atomic call budgets, invocation ledger, UNKNOWN reconciliation and late-result fencing | [Model execution control](docs/model-control.md): contention, process kills and recovery |

Applications freeze the materials used for that submission. Reviewed interview notes can become source-linked study documents and be retrieved for the same application's next preparation. Technical requirement parsing preserves literal C++, distinguishes required/preferred/alternative conditions, and fences old Analysis responses with semantic versions.

## Architecture

- **Core API / Worker:** Go, MySQL and Redis. Owns authoritative records, authorization, task scheduling and the application workflow.
- **Analysis:** separate Go processes, discovered through etcd and called through the public [KanaRPC-Go](https://github.com/KanaDoodle/KanaRPC-Go) facade. The module pins a published release; no sibling checkout is required. See [dependency provenance](docs/dependency-audit.md).
- **Optional Search:** independent HTTP service owning only the derived OpenSearch index. Core retains permission and content checks.
- **Optional Kafka / Notify:** independent Search and notification consumer groups. Notify remains in the Core business domain and shares its SQL database.
- **Observability and regression:** OpenTelemetry, Prometheus/Grafana/Tempo, pprof, k6, race/fuzz checks and GitHub Actions.
- **AI application backend:** persistent tasks, controlled tools, resumable reviewed workflows and MCP; local BM25 with optional Embedding/RRF/Reranker. Real model quality requires separate evaluation.

The [Chinese README](README.md#架构与技术栈) includes the service diagram. Default deployment starts Core, MySQL, Redis, etcd and two Analysis instances. Search, Kafka and the full observation stack are opt-in.

## Start

Requirements: **Go 1.25.9+, Git and Docker with Compose**. The first build downloads modules and images.

```sh
git clone https://github.com/KanaDoodle/CampusTrace.git
cd CampusTrace
./campustrace start --open
```

Open [127.0.0.1:8080](http://127.0.0.1:8080/) and register an account. The UI is in Chinese. Start with profile, source/manual import, job radar, applications, and interviews/reviews. Startup does not seed sample jobs.

```sh
./campustrace status
./campustrace doctor
./campustrace logs api --tail 100
./campustrace backup
./campustrace restart --build
./campustrace stop                 # Stop apps; preserve dependencies and data
./campustrace stop --all           # Stop dependencies too; preserve volumes
```

The CLI uses the fixed Compose project `campustrace`. Local settings live in the ignored `.env`. Use a separate JWT secret of at least 32 bytes outside local use. See [backup and recovery](docs/recovery.md).

## Synthetic demonstration

```sh
sh scripts/journey-local.sh demo
# In another terminal, against the fresh unmodified demo:
python3 scripts/smoke-journey.py
```

Demo: `http://127.0.0.1:23380`, account `demo@campustrace.invalid`, password `Synthetic2026!`. It owns disposable SQL/Redis dependencies, uses fictional data and a pre-seeded comparison, and makes no external model/source calls. Exiting removes only its temporary dependencies.

Alternatively, `sh scripts/journey-local.sh smoke` runs acceptance and exits. Set `JOURNEY_HTTP_PORT` for a different port. The HTTP scenario covers import/search, atomic application planning, frozen submissions, notice confirmation/rescheduling, review-to-note import, and retrieval for later preparation.

![Job radar using fictional data](docs/images/job-radar.jpg)

## Verification

```sh
export GOWORK=off
go build ./...
go vet ./...
go test -race -count=1 ./...
node --test web/*.test.cjs
make verify-boundary
make performance-test
```

- [Release acceptance](docs/verification/release-20261031/README.md): end-to-end scenario and verification entry points.
- [Search diagnosis](docs/verification/search-diagnosis-20261031/README.md): 1,000/5,000/10,000 synthetic jobs, trace/SQL/profile evidence and frozen sources.
- [Recovery probes](docs/verification/search-recovery-20261031/README.md): paired short runs, complete quantiles, failures and excluded host-interfered attempts.
- [Technical requirements](docs/verification/technology-requirements-20261031/README.md): historical excerpts, synthetic controls, RPC semantics and cache/generation fencing.
- [Workflow linkage](docs/verification/linkage-20261031/README.md) and [simplification](docs/verification/redundancy-fixes-20261031/README.md).

Performance belongs to each report's archived source and local synthetic conditions, not a production SLA or maximum throughput. Some p95/p99 measurements remain worse or variable; all results are retained. Rule controls and offline Agent tests are not model-accuracy measurements.

## Further reading

[Application journey](docs/application-journey.md) · [Matching](docs/matching.md) · [Knowledge retrieval](docs/knowledge-retrieval.md) · [Agent](docs/agent-harness.md) · [Sources](docs/campus-source-inventory.md) · [Operations](docs/operations.md) · [API](docs/api.md) · [Archived experiments](experiments/README.md).

Optional stacks: `make search-stack`, `make kafka-stack`, `make observability`. Disposable acceptance: `make resilience-test`, `make kafka-test`, `make performance`.

CampusTrace does not submit applications automatically. Notifications are mainly in-app. Scanned resumes have no OCR support; complex requirements need review. Production traffic, independent human-gold model quality and multi-node high availability have not been demonstrated. Keep personal resumes, API keys and database backups out of the public repository.

License: [AGPL-3.0](LICENSE).
