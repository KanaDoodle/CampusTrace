# HTTP and operator interfaces

API binds localhost by default. JSON requests are strict and limited to 64KiB except imports (1MiB / 100 rows). Every `/api/*` and `/agent/*` route requires a Bearer JWT. User identity is derived from the token, never from a client-supplied ownership argument. `/metrics` and health routes are local operational endpoints without authentication.

| Method / path | Behavior |
|---|---|
| POST /auth/register | email, password (10..72 bytes), bcrypt |
| POST /auth/login | JWT, 12h validity |
| GET /healthz, /readyz | process health; MySQL+Redis readiness |
| GET /metrics | process-local counters and latency sums/counts |
| GET /api/jobs?q=Go | up to 100 shared canonical jobs |
| GET /api/jobs/{id} | job, observation/evidence/change/assessment history, current user eligibility/ranking |
| POST /api/ingest | manual text or public URL, forced manual trust |
| POST /api/import | JSON array or text/csv; per-row results, not an all-or-nothing batch |
| GET, PUT /api/profile | current user's candidate profile |
| GET, POST /api/applications | list or directly create a plan (human API path) |
| POST /api/applications/transition | application_id, state, expected version, optional note |
| GET /api/applications/{id}/history | owner-scoped events |
| GET, POST /api/interviews | list or schedule interview |
| POST /api/interviews/{id}/finish | result PASS/FAIL/PENDING and notes |
| GET, POST /api/reviews | owner reviews; one validated review per interview |
| GET /api/weak_topics | accumulated review-backed topics |
| GET, POST /api/projects | owner projects |
| GET, POST /api/project_facts | IMPLEMENTED/LIMITATION/PLANNED facts; ownership checked against project |
| GET, POST /api/documents | owner knowledge documents |
| GET /api/knowledge?q=Redis | hybrid Top-5 chunks |
| GET /api/jobs/{id}/preparation | job + current_requirements + current_observations + input_identity + eligibility/fit + project facts + weak topics + knowledge |
| POST /agent/decide | session_id, message; synchronous final result |
| POST /agent/stream | same body; SSE lifecycle |
| POST /agent/actions/{id}/confirm | explicit `{"confirm":true}`; revalidates owner, TTL and transaction state |
| GET /agent/traces/{runID} | sanitized owner-scoped 24h trace |

Agent write tools are **not** mapped to direct CRUD routes. They call `Tools.Propose`, which only creates an expiring Redis preview. Direct authenticated human CRUD requests are already explicit actions. Confirmation uses a MySQL receipt in the same transaction as the workflow write.

Operational DLQ has no public REST endpoint; use `go run ./cmd/dlq` and explicit `-redrive ID`. Worker metrics bind `127.0.0.1:18081`. Source registration is a local CLI operation, not a way for untrusted HTTP clients to create official evidence.

The canonical evidence normalization vocabulary and import fixtures are in `internal/domain`, `internal/analysis` and `testdata`. Errors returned to HTTP are deliberately generic to avoid exposing SQL/schema or private payload contents; internal tests assert concrete error types.

Release repair: preparation fields are a single current input snapshot; historical evidence is available separately through the evidence endpoint. Knowledge ingestion/search returns HTTP 409 with `code: CORPUS_CAPACITY` when the searchable 10,000-chunk bound would be exceeded/is already exceeded. Agent `OUTPUT_LIMIT` is a terminal partial/limited outcome. Confirm replays a completed owner-scoped SQL receipt before consulting Redis pending data. Query evaluation is separated from persistence; explicit assessment uses input CAS (stale inputs return a conflict).
