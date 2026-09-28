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
| GET /api/profile/resume/capabilities | whether a server-default external model is configured; model name only |
| POST /api/profile/resume/draft | accepts only user-reviewed redacted `{"text":"...","model_config":{...}}` (max 16 KiB text); optional per-request model; returns cited, unsaved profile/project drafts; rejects common direct identifiers |
| POST /api/matching/preview | all accessible jobs, redacted candidate facts, and `local` version/score/tier/role/reasons/warnings/checks with source and candidate excerpts; optional model_url/model_name/mask_name; no model call |
| PUT /api/matching/settings | round_limit 1..100, daily_calls 1..200, auto_new |
| POST /api/matching/analyze | job_ids (1..3), candidate_hash, optional mask_name/model_config; evidence-checked cached comparison with daily quota |
| POST /api/matching/results/{id} | optional model_url/model_name/mask_name; BASIC/ANALYZED/STALE, current owner-scoped `local` screening/excluded_reason and optional saved model result; no model call |
| POST /api/matching/export | job_ids (1..1000 unique visible IDs), candidate_hash, optional model_url/model_name/mask_name; no model or quota use; ordered redacted facts/preferences/job text and metadata, max 5 MiB, Cache-Control: no-store |
| GET, POST /api/applications | owner-scoped records with visible job metadata, safe official URL and allowed next states; or directly create a plan (human API path) |
| PUT /api/applications/{id} | edit owned resume_version and note with expected version; preserve state and applied_at, append metadata event |
| POST /api/applications/transition | application_id, state, expected version, optional note |
| GET /api/applications/{id}/history | owner-scoped events |
| GET, POST /api/interviews | list or schedule interview |
| POST /api/interviews/{id}/finish | result PASS/FAIL/PENDING and notes |
| GET, POST /api/reviews | owner reviews; one validated review per interview |
| GET /api/weak_topics | accumulated review-backed topics |
| GET, POST /api/projects | owner projects |
| PUT /api/projects/{id} | rename one owned project |
| GET, POST /api/project_facts | IMPLEMENTED/LIMITATION/PLANNED facts; ownership checked against project |
| PUT /api/project_facts/{id} | edit one owned fact, retaining its project and creation time; explicit `verified` flag |
| GET, POST /api/documents | owner knowledge documents |
| GET /api/knowledge?q=Redis | hybrid Top-5 chunks |
| GET /api/jobs/{id}/preparation | job + current_requirements + current_observations + input_identity + eligibility/fit + project facts + weak topics + knowledge |
| POST /agent/decide | session_id, message, optional per-request model_config; synchronous final result |
| POST /agent/stream | same body; SSE lifecycle |
| POST /agent/actions/{id}/confirm | explicit `{"confirm":true}`; revalidates owner, TTL and transaction state |
| GET /agent/traces/{runID} | sanitized owner-scoped 24h trace |

Agent write tools are **not** mapped to direct CRUD routes. They call `Tools.Propose`, which only creates an expiring Redis preview. Direct authenticated human CRUD requests are already explicit actions. Confirmation uses a MySQL receipt in the same transaction as the workflow write.

Resume files are read in the browser; the resume draft API never accepts a file or raw resume upload. Manual profile and project/fact writes still persist only the selected structured fields. The resume draft endpoint has a per-user model call limit of five per minute, validates exact source excerpts, and does not persist input or draft output. An optional `model_config` contains `url`, `model`, and `api_key` for this request only. It must be a public HTTPS Chat Completions endpoint; private DNS results, proxies, and redirects are rejected. The key is not persisted. If no per-request model or server default exists, the draft endpoint returns `RESUME_MODEL_UNAVAILABLE`; invalid model settings return `MODEL_CONFIG_INVALID`. Other resume-specific codes are `RESUME_TEXT_INVALID`, `RESUME_PII_DETECTED`, and `RESUME_DRAFT_FAILED`.

Operational DLQ has no public REST endpoint; use `go run ./cmd/dlq` and explicit `-redrive ID`. Worker metrics bind `127.0.0.1:18081`. Source registration is a local CLI operation, not a way for untrusted HTTP clients to create official evidence.

Manual chat export is a read-only snapshot: it never includes account IDs, raw resume uploads, project references or credentials; project names appear only as redacted context. Missing latest successful JD text returns `MATCH_EXPORT_TEXT_REQUIRED` (409); changed candidate review returns `MATCH_INPUT_CHANGED` (409); count/serialized size overflow returns `MATCH_EXPORT_CAPACITY` (400). Duplicates and invalid IDs are rejected, and any inaccessible selected job rejects the whole export. The browser packs and downloads reviewed files locally; no export is persisted or automatically sent to ChatGPT, and there is no chat-result import endpoint.

The canonical evidence normalization vocabulary and import fixtures are in `internal/domain`, `internal/analysis` and `testdata`. Errors returned to HTTP are deliberately generic to avoid exposing SQL/schema or private payload contents; internal tests assert concrete error types.

Release repair: preparation fields are a single current input snapshot; historical evidence is available separately through the evidence endpoint. Knowledge ingestion/search returns HTTP 409 with `code: CORPUS_CAPACITY` when the searchable 10,000-chunk bound would be exceeded/is already exceeded. Agent `OUTPUT_LIMIT` is a terminal partial/limited outcome. Confirm replays a completed owner-scoped SQL receipt before consulting Redis pending data. Query evaluation is separated from persistence; explicit assessment uses input CAS (stale inputs return a conflict).

## Job Radar v0.2 additions

All `/api/*` routes use the existing JWT owner scope; there is no body/query owner override.

| Endpoint | Contract |
| --- | --- |
| GET /api/sources | Visible operator-registered source catalog, up to 100 |
| GET, POST /api/watches | Owner list / create; `source_id`, `check_interval` in seconds (300..604800), `keyword` (0..100 bytes), `enabled` |
| GET, PUT, DELETE /api/watches/{id} | Owner read / replace settings / delete; PUT retains source_id; enabling/disabling uses the enabled field |
| GET /api/radar/digest | Current SQL snapshot; `counts`, `as_of`, new/recommended/closing jobs, status/recent changes, upcoming interviews, explicit `truncated` |
| GET /api/radar/changes?days=1 | days = 1 or 7; latest 100 visible, non-ignored change events |
| GET /api/radar/closing?days=7 | days = 3, 7 or 14; current status/evidence timezone/eligibility/ranking/application/preference |
| GET /api/preferences | Owner preference records, up to 500 |
| PUT /api/jobs/{id}/preference | `{"disposition":"NONE|SAVED|IGNORED"}`; verifies job access |
| GET /api/notifications | Latest 100 owner notifications, including read_at |
| POST /api/notifications/refresh | Explicitly materialize current notification facts; SQL dedup; counts created/deduplicated |
| POST /api/notifications/{id}/read | Idempotently mark one owned notification as read |

Digest is read-only and does not send/materialize notifications. It returns complete counts within the aggregation capacity; each job section shows at most 5 records, changes/interviews at most 10. Capacity overflows return HTTP 409 `code: RADAR_CAPACITY`, rather than claiming a complete result. Closing results exclude ignored/closed/past-deadline jobs and applications beyond PLANNED. Existing application creation and versioned transition routes implement quick actions.

Source configuration is not a user-write API: a local operator registers `adapter` (`lever`, `greenhouse`, `smartrecruiters`), `tenant`, and optional `rate_limit` in the Source record. The Watch keyword filters posting title/location; URLs are derived from the registered platform and tenant. Editing a Watch increments its scheduling version and makes previous work stale. Already committed observations remain immutable history.

Agent read tools: `get_daily_digest {}`, `get_watched_sources {}`, `get_recent_changes {"days":1|7}`, `get_closing_jobs {"days":3|7|14}`. Writes: `watch_source` takes the same create fields; `unwatch_source {"watch_id":"..."}` proposes deletion. Both reuse PendingAction, owner/TTL checks, explicit `{"confirm":true}` and the existing transaction receipt. They cannot invoke confirmation themselves. Existing five-tool stdio MCP surface is unchanged.

Matching failures include a safe `diagnostic` object (`stage`: PREPARE/EXTRACT/COMPARE/SAVE, optional `provider_status` and fixed `response_reason`) and `request_id`. Transport failures use MODEL_CONNECTION_FAILED, MODEL_TIMEOUT, MODEL_ENDPOINT_BLOCKED or MODEL_RESPONSE_INVALID; evidence validation remains MATCH_OUTPUT_INVALID. Logs do not include raw error messages, provider bodies, keys or candidate content. No failed paid call is automatically retried.

Deep matching v2 results include `breakdown` for REQUIRED / RESPONSIBILITY / BONUS / SOFT, with total/known/coverage and direct/partial/transferable/missing/mismatch counts. Main score/coverage refer only to technical REQUIRED units; explicit ANY `group_id` items count once, supported by exact `group_excerpt`. Requirements also carry TECHNICAL/SOFT `aspect`. Candidate facts may contain redacted `project_name` and CITY_PREFERRED/CITY_ACCEPTABLE/JOB_TYPE_PREFERENCE facts; context and preferences cannot prove technical ability. Old inputs are STALE under `matching-v2-evidence`; saved prior results remain readable. Export version is `campustrace-chat-v2` with the same comparison rules.


API comparison input separates `candidate.facts` (qualification and ability facts) from `candidate.limitations`; preferences are resolved locally and omitted from the model input. A structurally valid positive judgment with an exact but inappropriate preference/limitation citation is withdrawn as NO_EVIDENCE with empty evidence and local `review_note=INVALID_ABILITY_EVIDENCE`. Other verified items remain; unknown IDs, forged excerpts, missing/duplicate items and schema failures still reject the output. Models cannot submit `review_note`. The analyze response includes `evidence_reviews` for newly saved withdrawn items, and preparation/company rows carry the same count when nonzero. Withdrawn items lower coverage, never count as an ability match or mismatch, and do not trigger another model call. Existing candidate hashes and requirement caches are preserved.
