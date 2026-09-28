[中文](README.md) | **English**

# CampusTrace v0.2 — Job Radar

**A campus recruiting workflow and job intelligence backend that monitors watched recruiting sources and turns verified facts into a daily Job Radar.**

A job page returning HTTP 200 does not mean the role is still open. A model saying “eligible” is not evidence. Campus recruiting has graduation, degree and job-type constraints that need explicit rules. CampusTrace records **what was observed, when, and why an assessment exists**.

This is a new domain implementation, with synthetic demonstrations. It does not apply to jobs automatically, invent personal experience, or claim production adoption.

## Job Matching

The Job Matching page locally screens all accessible jobs (explicit 10,000-job cap, independent of Search and Radar limits), then analyzes up to 30 pending jobs per round by default. Company/title and analysis-state filters, 50-row pagination, pause/resume, and individual failed-item retries are available. Each round allows 1–100 jobs; the default matching-call limit is 40 attempts per Beijing calendar day, configurable to 1–200. Calls are sequential per account, with batches of up to three jobs and 24,000 input bytes. Dense requirements split into smaller comparison groups. Failed attempts count toward the server-enforced daily limit; this is neither an account-wide API spending cap nor a currency budget.

Local screening recognizes role aliases and explicit duties in generic titles, separates mandatory requirements/duties/bonuses, distinguishes alternative versus conjunctive language choices, and uses only positive confirmed IMPLEMENTED facts as project evidence. Aliases and duplicate skills do not inflate scores; components do not imply unrecorded capabilities. Priority combines role (30), city/type preferences (20, including acceptable cities), technical clues (30) and project evidence (20), with requirement weights 3/2/1. Four suggestions—HIGH, POSSIBLE, UNCERTAIN, LOW—remain separate from deep model scores. Missing evidence is unknown, not inability. Only unambiguous minimum degree/cohort failures exclude automatic analysis; conflicting, preferred, month-level or incomplete conditions remain for review. Updated time breaks score ties only. Click a job to inspect source excerpts and candidate evidence; filter by inferred direction or tier.

This deterministic parser uses a bounded vocabulary and does not fully interpret arbitrary job semantics. Limits are 600 bytes per clause, 48 requirement groups, 64 duty clues and 16 qualification clauses; overflow is explicitly flagged. Redacted JD parsing is cached in process by content and a separate local version, bounded to 10,000 entries/a 32 MiB estimated memory budget. Candidate evidence is rebuilt per account/snapshot. No model calls or quota consumption occur, and the local version does not invalidate paid model results.

Select individual jobs, the current page, all filtered results, or the first N pending jobs. Selections persist across pagination, filters, and refresh in account-scoped browser sessionStorage; inaccessible jobs are pruned. Selected API analysis retains round limits, cache reuse, and daily quotas. Alternatively, prepare a manual ChatGPT package without a model key or external model calls. The owner-scoped export returns only redacted candidate facts, preferences, and current selected job descriptions/metadata, with limits of 1,000 jobs and 5 MB. Missing latest text or changed candidate inputs cause an explicit error. Review/edit the complete text and confirm before downloading or copying. Packages repeat the same facts and scoring instructions, split at eight jobs or approximately 48 KB UTF-8 including instructions; oversized individual jobs remain intact in their own package. One package downloads as Markdown; multiple packages download as a ZIP with usage and merge instructions. Users upload the files to ChatGPT themselves. Chat results are not imported automatically or saved over API results in this version.

The page reuses the selected browser model. Review the outbound structured facts before starting. Only criteria, skills, languages, role/city/type preferences, and confirmed IMPLEMENTED/LIMITATION facts with redacted project context are sent; raw resumes, account/contact information, reference links, unconfirmed facts, and plans are excluded. Common identifiers are masked. An additional name can be masked for this run by the CampusTrace service; that temporary input is neither stored nor sent to the provider. A remote deployment still processes it remotely. API keys are transient request configuration, never queue/database/cache contents.

Job requirements and personal comparisons are cached separately by account, exact redacted content, model and matching version; comparisons bind profile revision, confirmed facts, preferences and redacted project names. Names supply context, not proof of ability. The v2 main score uses technical REQUIRED items only: direct/partial/transferable/mismatch = 1/0.5/0.25/0. Unknown or low-confidence items reduce core coverage; below 60% the score remains null. Duties, bonuses and soft traits have independent coverage; soft traits are not scored. Explicit ANY groups count once using the best supported alternative; a group is a mismatch only when all alternatives have explicit contrary evidence. Preferred majors are bonuses, cities use normalized aliases, and saved preferences cannot prove technical ability. Extraction supports at most 36 items per job and rejects reported truncation. Verified SQL or message-recovery mechanisms may support partial/transferable experience without implying Kafka or AI-framework proficiency. The UI shows section summaries, project context and stale prior evidence. `matching-v2-evidence` invalidates old extraction/comparison caches without triggering paid calls or rewriting saved results. Qualifications and recruiting status remain separate; semantic judgments still require human review.

Optional automatic analysis checks newly added or changed jobs every minute only while this page remains open, outbound facts are reviewed, and allowance remains. Historical inventory is not automatically sent on first opening. Leaving/closing the page stops subsequent batches; completed groups and extraction caches persist for later continuation. There is no closed-browser key custody. OpenAI API-key calls use independent API billing, not Codex subscription allowance.

## Job Radar increment

User-owned watches, atomic MySQL scheduling, posting discovery/fetch fan-out, recent changes, deadline radar, preferences, daily digest and a notification inbox now share the existing business chain. The Chinese homepage centers on daily actions. Four Agent read tools and two confirmation-only watch proposal tools reuse the existing runtime.

Lever (`weride`), Greenhouse (`pingcap`) and SmartRecruiters (`Ubisoft2`) are IMPLEMENTED, FIXTURE TESTED and LIVE VERIFIED on 2026-09-14 through Discover → Fetch → Ingest → persisted Observation. The WeRide sample was a China new-graduate role; the Greenhouse sample was in Tokyo, and the Ubisoft Shanghai sample was not claimed to be a graduate role. The Xiaohongshu campus listing at `https://job.xiaohongshu.com/campus/position` now has a dedicated adapter: paste the URL in Watches, preview its current campus project, then choose a broad direction, optional title/location keyword and scan interval. Imports are private to the user and deliberately retain `MANUAL` trust. The public API's paginated list and job details were live-read on 2026-09-28; no other arbitrary career site is implied to work. Unknown adapters are UNSUPPORTED; 401/403 are terminal BLOCKED for that attempt, while 429/5xx/timeouts retry. Local source pacing defers tasks without spending their network retry budget. Failure never establishes closure.

Migration 004 adds watch targets, run/result receipts, references, user preferences and notification uniqueness. Stop old workers before upgrading: old binaries cannot consume the new task types. The Source fetch-only API remains compatible; discovery is an additive capability. KanaRPC-Go and the Analysis RPC boundary are unchanged.

Public source queries reuse connections and try alternative pinned public addresses after validating the entire DNS answer. Read-only queries retry transport errors and 5xx once; access denial and rate limits are not immediately retried. Preview errors distinguish network connectivity, upstream load, access restrictions, and schema changes.

Use `./scripts/verify-radar.sh` for fresh isolated integration/migration databases and `RADAR_RACE=1 ./scripts/verify-radar.sh` for real-dependency race coverage. Explicit external verification requires `CAMPUS_LIVE_SOURCES=1 CAMPUS_INTEGRATION=1` with `go test -count=1 -run TestRadarLiveSources -v ./internal/integration`. Frontend tests: `node --test web/*.test.cjs`.

Defaults are intentionally bounded: 100 watches per owner; 500 posting refs per watch; 1 MiB decompressed response and 60,000-byte posting body; 500 visible jobs per Radar aggregation. Digest totals are complete within capacity, with 5 jobs per section and 10 changes/interviews; feeds/inbox expose the latest 100 records. The Worker scans 10 notification owners per minute. Historical pagination, automatic retention, broad source coverage, source-login automation and external notifications are not implemented. See the [engineering report](PRODUCTIZATION_REPORT.md) and [Chinese setup and adapter details](README.md#job-radar-v02).

## Core ideas

- A canonical Job can have several source postings. Each actual observation is immutable input; its extraction state can advance independently.
- Evidence has a source observation, verbatim/normalized excerpt, method and confidence. Candidate model claims are strictly decoded and validated before persistence.
- Job status, eligibility, technology fit, preference ranking and application state are distinct concepts.
- MySQL transactions and unique constraints provide correctness. Redis provides queueing, retries, rate limits, caches and short-lived Agent state.
- Analysis is the **only** remote business service. It returns candidate claims over the existing KanaRPC/MyRPC framework. The main backend owns decisions and CRUD.

## Architecture

```mermaid
flowchart TD
  UI[Thin UI / HTTP clients] --> API[Go API + JWT ownership]
  Host[External MCP Host] --> MCP[MCP stdio - 5 read tools]
  MCP --> Local[Local business tools]
  API --> Local
  API --> Watch[User-owned WatchTarget]
  Watch --> Scheduler[Scheduler inside Worker]
  Scheduler --> DB
  Worker --> Discover[WATCH_CHECK / public discoverer]
  Discover --> Fanout[Transactional Outbox / WATCH_FETCH]
  Fanout --> Stream
  Worker --> Fetch[Public posting fetcher]
  Fetch --> O[SourcePosting / Observation]
  API --> O
  Assessment --> Radar[DailyDigest / changes / deadlines]
  Radar --> Inbox[MySQL notification inbox]
  O --> DB[(MySQL authoritative data + outbox)]
  DB --> Publisher[Transactional outbox publisher]
  Publisher --> Stream[(Redis Stream + Consumer Group)]
  Stream --> Worker[Bounded Go workers]
  Worker --> RPC[MyRPC client + independent semaphore]
  RPC --> A1[Analysis instance 1]
  RPC --> A2[Analysis instance 2]
  Etcd[(etcd discovery)] -.-> RPC
  A1 --> Claims[Strict candidate claims]
  A2 --> Claims
  Claims --> DB
  DB --> Assessment[Assessment task + deterministic rules]
  Assessment --> DB
  Worker --> Retry[(Retry ZSET / DLQ)]
  Retry --> Stream
  Local --> Agent[Bounded Agent runtime]
  Agent --> Pending[Pending action preview]
  Pending --> Confirm[Explicit authenticated confirmation]
  Confirm --> DB
  Local --> RAG[MySQL chunks + lexical vectors + hybrid search]
```

## Evidence and eligibility

`Source → Observation → Evidence → Deterministic Rules → Assessment`.

Sources are operator-attested `OFFICIAL`, `THIRD_PARTY` or `MANUAL`. Public API imports use a user-owned `PRIVATE` manual source and cannot grant themselves official trust. Official/system sources remain `GLOBAL`. Private manual observations never merge into the shared catalog. A local operator can register sources using `cmd/ingest`.

Canonicalization first matches source + external ID, then a source-scoped URL as **posting** identity. A versioned structured digest only finds cross-source candidates; reuse also checks company ID, normalized title, job type, canonical location set, and source identity. External IDs and URL path/query preserve case; content uses exact bytes. No fuzzy/LLM merge. Posting JSON stores the rationale. Uncertain matches remain separate. Without a stable external ID or URL, changed manual text may form a separate job; supply a stable external ID when observing a manual posting again.

Status: recent official application evidence without closure permits `OPEN`; explicit official closure/expired deadline permits `CLOSED`; conflicting/insufficient or third-party-only evidence needs verification; failed/stale official observations yield `UNKNOWN`. HTTP 200 alone never opens a job. Latest observations are selected per posting; previous successes cannot hide a newer 403. Worker enqueues hourly freshness reassessments using stored observations, with up to one hour of status-cache lag. Enabled WatchTargets now discover and re-fetch supported sources through the existing ingestion chain.

Assessment history is append-only. Successive successful content hashes drive content/structured changes. SQL receipts and Redis cache share a complete `ProcessingVersion`: semantic/model/prompt configuration, implementation parser version, source adapter/parser version and the observation parser version. Change `ANALYSIS_VERSION` when changing a model or extraction prompt; parser-only changes invalidate receipts automatically. At scheduling (`BindAnalysis`), a previously unseen processing identity receives a numeric generation and becomes the observation's explicit desired/active generation. Queued tasks are durably bound to that identity. Replaying a known old identity never reactivates it; version strings are never sorted. Only the active generation may become current. Late old results remain historical; active receipt replay reconciles the pointer and replaces derived apply/deadline fields atomically. While the desired generation is pending, old-generation evidence is excluded from current rules. Deployment must schedule the desired implementation before relying on it; this is explicit request order, not automatic inference of release chronology from version names. To intentionally redo or roll back an implementation, use a new processing configuration identity. A full history selection interface remains outside 1.0.

Eligibility produces eight rule results: graduation, degree, job type, location, experience, major, language and technical requirements. Missing graduation/degree/job-type evidence is critical `UNKNOWN`. Explicit hard failure wins; unresolved evidence beats preference conditions. Location and preferred job type are preferences (`CONDITIONAL`), not fabricated legal eligibility constraints. Majors/languages/experience become hard requirements when identified. Optional technology signals do not override explicit `REQUIRED:` constraints.

Every comparison row includes the saved candidate value even when job evidence is missing or conflicting. Candidate cities include preferred and acceptable locations; graduation ranges are displayed as ranges. Missing job evidence and unfilled profile fields use distinct messages. A complete candidate profile does not turn missing job evidence into a passing requirement.

GoFit is independent: `EXPLICIT_GO`, `LANGUAGE_FLEXIBLE`, `NO_GO_SIGNAL`, `CONFLICTING`, `UNKNOWN`. Ranking is a weighted sum with a visible breakdown; configure all weights through `RANKING_WEIGHTS`.

The conservative offline parser supports explicit labels in [testdata/import.json](testdata/import.json), common `2027届`, `本科及以上`, Go/Golang and apply/closed text. It does not claim broad natural-language extraction quality.

## Candidate profile and resume drafts

The **Candidate Profile** page now holds both job criteria and projects/facts. Users can add or rename projects, and add or edit facts with explicit IMPLEMENTED/LIMITATION/PLANNED and verification flags. Only verified IMPLEMENTED facts support completed-work claims in the Agent.

Text-based PDF, DOCX, and TXT resumes (up to 5 MB) are extracted in the browser. The raw file and raw extracted text are never uploaded. Labeled names and common mainland Chinese mobile formats (including spaces, hyphens, and `+86`) are masked locally. For an unlabeled name, the user can enter it for local masking; that entry is neither saved nor sent to the server. The user must inspect and may edit the complete outbound preview before explicitly sending it to the configured external model. The model returns cited, unsaved drafts. Go implementation details such as `goroutine` and `sync.Mutex` are filtered from standalone skill suggestions while remaining available as project facts. Suggestions and project facts are unchecked by default, and a user selects what to persist in the existing MySQL profile/project records. Automatic masking is not exhaustive, and scanned PDFs have no OCR support. Without a server default or a model selected on the Models page, manual editing remains available. Confirmed criteria and project facts can also be used by the Job Matching workflow below.

The Models page provides GPT-6 Luna, GPT-6 Sol, DeepSeek Flash, and DeepSeek V4 Pro presets. Users enter only an OpenAI or DeepSeek API key, then select a model; provider endpoints and model IDs are fixed by the project. One provider key works across its presets. The server default remains available. Keys are saved per signed-in account in this browser's local storage and survive refresh and logout; users can delete them on the Models page. They are not stored in the CampusTrace database. Browser local storage is not an encrypted vault: someone with access to this browser profile may read the keys. Enter them only on a trusted device using a local page or HTTPS. On each call, the browser sends the key to CampusTrace, which forwards it to the selected provider. Agent questions and relevant tool context are sent to that provider, with separate conversations for different models.

PDF.js and JSZip are bundled locally under `web/vendor/` with version and license information.

## Application and interview workflow

Applications follow `PLANNED → APPLIED → OA / INTERVIEW → HR / OFFER`, with permitted rejection/withdrawal transitions. Every transition locks/validates current state and expected version, inserts an event and updates the application in **one MySQL transaction**. Closed jobs do not terminate applications. Terminal application states cannot be reopened in 1.0.

Interviews and reviews belong to their application owner. Reviews are append-only, one per interview. Validated weak topics accumulate count, severity, first/last seen and review references. Preparation exposes `current_requirements` and `current_observations` from the same input snapshot as eligibility/technology fit, plus verified project facts (including limitations), weak topics and retrieved knowledge. Historical requirements are not mixed into this field. Priorities currently use weak-topic severity × occurrence count.

## Reliable async pipeline

One Stream carries `WATCH_CHECK`, `WATCH_FETCH`, `ANALYZE` and `ASSESS` envelopes. Publishing outbox rows may duplicate after a crash; consumers tolerate this. SQL analysis results and completed assessment-task keys are the correctness boundaries.

Workers use a fixed pool. MyRPC calls have a separate semaphore; model and embedding work have separately configurable limits. PEL recovery uses `XAUTOCLAIM` with idle time greater than the task deadline. Crash-after-commit-before-ACK is explicitly tested.

Transient failures use exponential backoff (2s, 4s, 8s...) with jitter. Redis Lua atomically stores retry/DLQ state and ACKs. Another Lua script transfers due retry members into the Stream and removes them atomically. Permanent schema/auth/validation failures go directly to DLQ. Terminal analysis failure is recorded in MySQL and reassessed as unknown. Malformed envelopes are retained in a sanitized DLQ task entry; raw malformed payload preservation is not implemented.

Inspect/redrive deliberately through the local operator CLI:

```sh
go run ./cmd/dlq
go run ./cmd/dlq -redrive TASK_ID
```

Sliding-window rate limits use Redis server time and atomic ZSET/Lua remove/count/accept/record, with source-specific keys plus global LLM/embedding keys. Redis is standalone in 1.0 (the multi-key Lua scripts are not Redis Cluster slot-aware). Stream/completed-task/outbox history retention is manual; do not run indefinitely without adding archival policies.

## MyRPC integration

CampusTrace is the independent module `github.com/KanaDoodle/CampusTrace`. It imports only `github.com/KanaDoodle/KanaRPC-Go/rpc`. The narrow public facade supplies registry construction/discovery/readiness, client invocation with context, handler registration and server lifecycle. Transport, codecs, pools and breaker implementation types stay internal to KanaRPC.

CampusTrace pins `github.com/KanaDoodle/KanaRPC-Go v0.1.0` from the maintainer's [public repository](https://github.com/KanaDoodle/KanaRPC-Go). `go.mod` has no replacement. Normal builds use `GOWORK=off` and require no sibling checkout. The RPC API is a v0.x public facade and does not yet promise long-term API stability.

For optional local co-development only, `make dev-workspace` creates an ignored `go.work` pointing to a sibling checkout. `make verify-boundary` checks the current source against the pinned published module with the workspace disabled; it does not replace the separate remote clean-clone acceptance procedure.

Two instances register in etcd. Known breaker-open instances are excluded before load balancing; admission races permit at most one selection attempt per discovered address. Remote business methods are never automatically replayed. Local cancellation is neutral to the breaker; backend deadlines still count. Lost KeepAlive/lease triggers degraded readiness, capped exponential backoff with jitter, a new lease and registration recovery. Analysis exposes `/healthz` and `/readyz` at `ANALYSIS_HEALTH_ADDR` (local default `127.0.0.1:19191`; the second launch uses `19192`). Tests revoke a real lease and inject a closed KeepAlive channel.

The RPC wire protocol still has no early remote cancellation signal. CampusTrace carries an explicit deadline and IDs; server work stops on that deadline or service shutdown.

## Agent / RAG / MCP

Default `DemoModel` is an explicitly deterministic offline natural-language router. Users can select an external model on the Models page, while operators can set `LLM_URL`, `LLM_API_KEY`, and `LLM_MODEL` as the server default; ordinary tests use scripted models and never require external credentials.

The model selects tools through at most 4 calls / 8 executions; deadline 35s and per-tool timeout 8s are configurable. All tool arguments are validated at runtime, including unknown fields, trailing JSON, nulls, required fields, enums, ranges and size. Last-step proposals are traced but never executed.

Final business-fact narration is rendered deterministically from successful tool observations. **Model final prose is not presented as factual truth.** This deliberately limits free-form conversational synthesis, while the model can still decide which structured tools or knowledge to retrieve. Project facts are explicitly labelled IMPLEMENTED/LIMITATION/PLANNED and only verified facts establish a claim. Retrieved instructions cannot invoke arbitrary tools or skip confirmation.

Write tools only store a validated PendingAction with user ownership and a 10-minute TTL. `POST /agent/actions/{id}/confirm` requires `{"confirm":true}`, then revalidates current state/version. Confirmation first reads a completed MySQL receipt scoped by action ID and authenticated user, so replay survives missing/unavailable Redis pending data. Without a receipt, owner/TTL/current workflow state/version are validated as usual; Redis absence and backend errors remain distinct. MySQL action receipts make concurrent/repeated confirmations idempotent. The model has no confirmation tool.

Session memory is scoped by user and session, at most 6 complete turns/32KB, 30-minute TTL with optimistic version checks. Concurrent session saves may skip the stale writer rather than overwrite newer memory. Failure traces are sanitized and retained 24h; a Redis outage can prevent trace persistence. Session content is not business evidence.

RAG persists documents/chunks/vectors in MySQL. Its default embedding is **deterministic lexical hashing into 128 dimensions**, not a semantic embedding model. Retrieval uses brute-force cosine + keyword overlap over at most 10,000 owner-scoped chunks, Top-K ≤20. The restartable release migration adds `chunks(user_id,id)` for the owner-filtered scan. Ingestion that would exceed 10,000 chunks is rejected atomically with `CORPUS_CAPACITY`; an existing oversized corpus also returns that explicit error instead of silently advertising full-corpus Top-K. HTTP/UI surface this capacity error. Duplicate document import, update and delete lifecycle remain Known Limitations. No ANN, vector database or hidden external embedding call. Chinese bigrams provide basic lexical support.

`POST /agent/decide` returns the final result. `/agent/stream` emits run_start, model_start/end, tool_start/result, final and error over bounded direct SSE writes. Disconnect cancels Context; token streaming/replay/reconnect are out of scope.

Official Go MCP SDK `v1.7.0`: a standalone stdio server exposes five read tools: search_jobs, get_job, get_job_evidence, get_job_eligibility, search_knowledge. Stdout is protocol-only; logs go to stderr. Local Agent tools do not round-trip through MCP. These are business-workflow read-only tools. MCP/Agent queries reuse an unexpired assessment for identical inputs, or compute a current result without inserting eligibility/ranking/history rows. Redis knowledge rate-limit counters may still change. Explicit/background assessment persists through input-identity CAS and unchanged-input deduplication. Profile revisions increment on saves; current observation/generation, job metadata and rule/weight versions are included. Time-dependent cached results expire within one minute or at the next deadline/freshness boundary.

## Quick start

The source entry point needs Go 1.25.9+, Git, a running Docker engine with Compose, and network access for initial dependencies. No sibling KanaRPC checkout is required.

```sh
git clone https://github.com/KanaDoodle/CampusTrace.git
cd CampusTrace
./campustrace start --open
./campustrace status
./campustrace logs api --tail 100
./campustrace doctor
./campustrace stop
./campustrace start
./campustrace restart --build
./campustrace backup
./campustrace stop --all
```

The source wrapper builds `bin/campustrace` when needed. The CLI combines `docker-compose.yml` and `compose.app.yml` to run MySQL, Redis, etcd, API, worker and two analysis containers, waiting for all seven health checks. Services are detached from the terminal. Normal starts reuse the existing application image; source updates require `restart --build`. The Dockerfile uses a multi-stage Go build and an unprivileged minimal runtime with CA certificates and timezone data. No remote prebuilt application image has been published; initial local builds need network access. Private configuration, databases and backups are excluded from the build context.

Startup applies existing migrations but never seeds accounts, jobs or profiles. Register in the web UI on a fresh database. The fixed `campustrace` Compose project preserves existing named volumes. `stop` stops only the four application services; `stop --all` also stops dependencies and never removes volumes. Backups are local SQL files created with mode 0600, refusing to overwrite files. Rebuilds during `restart --build` back up before stopping the running application. Lifecycle commands use an OS file lock; failed starts stop only application services that were not already running.

On macOS, `start` backs up and takes over the legacy `com.campustrace.local` supervisor only when it belongs to this checkout and no deep matching lease exists. An unrelated occupied web port causes an error without terminating the occupying process. Every command supports `--dir`; otherwise the CLI searches its working directory and executable location. `make run` and `make stop` use the same CLI. `logs --follow` stops following on interruption while services remain running.

Use ignored `.env` values for `CAMPUS_HTTP_PORT`, `JWT_SECRET`, optional server model configuration, or an alternative trusted Go build image via `CAMPUS_GO_IMAGE`. The default JWT secret remains compatible with the previous local demo setup; set a different 32+ byte secret outside local demonstrations. Browser model keys and selection remain in the browser and are not read by the CLI.

Host development remains available: stop container applications, then use `make deps`, `make dev-run` and `make dev-stop`. Supervised foreground development uses `make build && FOREGROUND=1 ./scripts/start.sh`. Each binary supports environment configuration documented in [configs/local.env.example](configs/local.env.example); quote exports correctly rather than sourcing its unquoted DSN. Run `make seed` only to explicitly request synthetic demo data (demo account: `demo@campustrace.local` / `Synthetic-demo-2027`).

Manual imports:

```sh
go run ./cmd/ingest -file testdata/import.json
go run ./cmd/ingest -format csv -file testdata/import.csv
# Operator-attested source registration (no automatic trust inference):
go run ./cmd/ingest -register-source -source company-careers -name 'Example careers' -type OFFICIAL -trust OFFICIAL
```

Public URL import is available in UI/API; captchas, login requirements and 403s are recorded without bypass. HTTP fetches reject private/loopback/link-local addresses, including redirects/DNS resolution. Only explicit text/HTML content types are parsed. Reading more than 1 MiB of decompressed response content, unsupported encodings or binary types returns `PARSE_ERROR`, with no partial text treated as a complete observation.

For MCP, log in through `/auth/login`, then provide its JWT as `MCP_TOKEN` with the same `JWT_SECRET` and database config before running `go run ./cmd/mcp-server`. Token is verified at process startup; restart the stdio server to change identity or revoke access. Mid-session JWT expiry/revocation is not enforced in 1.0.

## Demo

1. Jobs: search `Cedar` and open `Go backend engineer`.
2. Inspect two observations, changed hashes, deadline/technology changes and assessment history.
3. Compare graduation/degree pass, hard-fail and unknown profiles/jobs; view independent GoFit/ranking.
4. Applications: inspect synthetic PLANNED/APPLIED/OA/INTERVIEW history; change state using its displayed version.
5. Interviews / Weak Topics: review the stored PEL weakness; open job preparation context to see it included.
6. Agent: ask `为什么岗位 JOB_ID OPEN？` or `我的项目做了什么？`. It queries this run's structured facts.
7. Ask `创建申请 JOB_ID` for a job without an application. Review the pending preview before the separate confirmation button.

Seed content and relationships are deterministic examples; IDs and reference-relative timestamps are generated per fresh database. All companies, jobs and project claims are explicitly synthetic.

## Testing and actual measurements

```sh
go test ./...
go test -race ./...
go vet ./...
make integration           # creates/grants campustrace_test, then real dependency tests
CAMPUS_INTEGRATION=1 go test -race -count=1 -v ./internal/integration
make eval                  # 24 scripted runtime contract cases, NOT live-model accuracy
make loadgen               # 100 synthetic observations through the running worker/MyRPC
```

Regular tests explicitly skip integration without `CAMPUS_INTEGRATION=1`; final verification also runs them enabled. `MYSQL_TEST_DSN` can override the separate integration database. Tests retain synthetic rows within that test database. Local operator database privileges are needed for test-database creation in the Make target; tests themselves use the non-root `campus` account.

Remote reproducibility is checked by cloning this repository outside a development workspace, using `GOWORK=off` and a fresh `GOPATH` / `GOMODCACHE`, running download/test/race/vet/build and the Quick Start + Demo flows. Frontend regressions run with `node --test web/display.test.cjs`. Local measurements alone do not establish remote reproducibility.

## Known limitations

- Limited rule parser/public HTTP adapter; no JS rendering, login automation, anti-bot bypass or automatic job application.
- Source trust is operator-attested; the system does not prove domain ownership or verify a third-party board's authenticity.
- Exact canonicalization intentionally under-merges and includes the initial content fingerprint. Changed postings stay attached by external ID/URL; cross-source aliases with changed text may remain separate.
- JSON-backed SQL aggregates plus relational ownership/FK/unique keys optimize implementation clarity for personal scale. No schema downgrade tool, document revisions, rich search pagination or multi-tenant administrative roles.
- Historical evidence versions remain stored. Explicit scheduling selects an active processing generation; older completions cannot replace its current evidence. Untouched legacy observations require explicit reanalysis. No all-history migration or version-switching UI is provided.
- Old official contradictions conservatively produce verification/unknown outcomes. Freshness status cache may lag one hour; enabled watches add a MySQL-authoritative recrawl schedule.
- The v0.x KanaRPC API and remote cancellation limitations described above; educational framework, not a production RPC-platform claim.
- Free-form model answers are intentionally withheld in favor of grounded rendering. Optional live provider and semantic embedding quality have not been evaluated. Weak-topic extraction currently uses validated explicit input, not an LLM extractor.
- SSE no replay/reconnect; MCP auth is startup-scoped; no account email verification/password reset/revocation service.
- No automatic queue/history archival or Redis Cluster support. DLQ has operator-only CLI access. Redis metrics are process-local counters, reset on restart: API `/metrics`, worker `127.0.0.1:18081/metrics`.
- The Chinese-first UI uses structured profile and review forms and has a 100-result job search cap. No frontend framework is used.

## Roadmap

Evolve the existing public RPC facade under v0.x; extend the three implemented public platform adapters; semantic embedding provider with evals; structured review extraction; retained-history archival; richer UI/profile forms. None of these planned features is described as implemented.

License: GNU Affero General Public License, Version 3 (AGPL v3); see the existing [LICENSE](LICENSE) and the upstream notice in [dependency audit](docs/dependency-audit.md). The license text is unchanged.

## Repair migration and semantics

Startup migrations are serialized and restartable. Existing IDs, raw text and history are retained. Legacy manual data has no reliable owner: jobs touched by those sources (including mixed jobs) are quarantined as private with no assigned user. An operator must review the original provenance before assigning ownership or reconstructing a public job; the repair does not guess or expose these records. Existing wrongly merged historical jobs are not automatically split. Posting lookup keys are upgraded while retaining IDs. Stop old writers before migrating; mixed old/new binaries are unsupported.

New manual sources default to `Asia/Shanghai`. An operator can specify `-timezone` and `-owner` in `cmd/ingest`. Date-only deadlines expire at the start of the next day in the source timezone; timestamps with explicit offsets retain their instant. Cache namespace `cache-v2`, semantic implementation `claims-v3-semantics`, complete `processing-v3-*` identity and exact text digest invalidate older cache/receipt identities. The effective semantic version is enforced even when an old environment still sets `claims-v1`; custom model/prompt versions are namespaced and hashed within the SQL length limit. Historical assessment rows remain historical. Reanalyzed observations select only evidence from their current analysis version; untouched historical observations require explicit reanalysis or a new observation.

Application/closure claims require both excerpt binding and conservative signal support. Conflicting positive/negative observations cannot become OPEN merely because a positive phrase exists. Ranking `breakdown_sources` separates evidence assessments, user preferences and job/observation metadata. Latest metadata from a stable posting refreshes title/type/locations; older observations cannot overwrite it.

Malformed Stream messages use a separate `poison` hash with bounded diagnostic text, message digest, timestamp, consumer and recoverable correlation ID. Failed quarantine keeps the message pending. Failure transfer preflights Redis key types/group, writes retry/DLQ before a v2 completion marker, and acknowledges last. Lua runtime errors do not roll back earlier writes; stable transfer bytes allow safe reruns. Legacy premature markers are not trusted.

### Release repair runtime bounds

`AGENT_MAX_TOOL_RESULT_BYTES` defaults to 32,768; `AGENT_MAX_FACTS_BYTES` to 98,304; `AGENT_MAX_ANSWER_BYTES` to 32,768; `AGENT_MAX_FINAL_BYTES` to 163,840 (minimum effective final budget 512 bytes for the terminal envelope). Nonpositive values use defaults. The final budget includes serialized JSON and SSE framing. Oversized tool results are rejected before entering Facts; output reduction ends in `OUTPUT_LIMIT`, never a silently complete answer. Same-step duplicate call IDs execute once; validated canonical read/action arguments reuse the first result and the same pending action. Later model steps may reread.

PEL recovery retains the stream/group cursor across bounded passes (at most 32 XAUTOCLAIM commands per pass) and claims one message for each serial worker's one available slot. Work remains at-least-once: an executing task that exceeds its claim idle timeout can still be reclaimed; no fencing or lease renewal is claimed.
