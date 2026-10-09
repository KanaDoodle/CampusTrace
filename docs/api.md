# HTTP and operator interfaces

API binds localhost by default. JSON requests are strict and limited to 64KiB except imports (1MiB / 100 rows). Every `/api/*` and `/agent/*` route requires a Bearer JWT. User identity is derived from the token, never from a client-supplied ownership argument. `/metrics` and health routes are local operational endpoints without authentication.

| Method / path | Behavior |
|---|---|
| POST /auth/register | email, password (10..72 bytes), bcrypt |
| POST /auth/login | JWT, 12h validity |
| GET /healthz, /readyz | process health; MySQL+Redis readiness |
| GET /metrics | process-local counters and latency sums/counts |
| GET /api/jobs?q=Go | up to 100 shared canonical jobs |
| GET /api/jobs/{id} | job, observation/evidence/change/assessment history, current user eligibility/ranking, `official_url` from a visible official posting (empty if absent or unsafe) |
| POST /api/ingest | manual text or public URL, forced manual trust |
| POST /api/import | JSON array or text/csv; per-row results, not an all-or-nothing batch |
| GET, PUT /api/profile | current user's candidate profile |
| GET /api/profile/resume/capabilities | whether a server-default external model is configured; model name only |
| POST /api/profile/resume/draft | accepts only user-reviewed redacted `{"text":"...","model_config":{...}}` (max 16000 UTF-8 bytes); optional per-request model; returns cited, unsaved profile/project drafts and safe item-level `warnings`; rejects common direct identifiers |
| POST /api/matching/preview | all accessible jobs, redacted candidate facts, and `local` version/score/tier/role/reasons/warnings/checks with source and candidate excerpts; optional model_url/model_name/mask_name; no model call |
| PUT /api/matching/settings | round_limit 1..100, daily_calls 1..200, auto_new |
| POST /api/matching/analyze | job_ids (1..3), candidate_hash, optional mask_name/model_config; evidence-checked cached comparison with daily quota |
| POST /api/matching/results/{id} | optional model_url/model_name/mask_name; BASIC/ANALYZED/STALE, current owner-scoped `local` screening/excluded_reason and optional saved model result; no model call |
| POST /api/matching/export | job_ids (1..1000 unique visible IDs), candidate_hash, optional model_url/model_name/mask_name; no model or quota use; ordered redacted facts/preferences/job text and metadata, max 5 MiB, Cache-Control: no-store |
| GET, POST /api/applications | latest 500 owner-scoped records with visible job metadata, safe official URL and allowed next states; or directly create a plan (human API path) |
| PUT /api/applications/{id} | edit owned resume_version and note with expected version; preserve state and applied_at, append metadata event |
| POST /api/applications/transition | application_id, state, expected version, optional note |
| GET /api/applications/{id} | One owned record with visible job metadata, safe official URL and allowed next states; supports direct navigation beyond the latest list, Cache-Control: no-store |
| GET /api/applications/{id}/history | owner-scoped events |
| GET, POST /api/interviews | latest 500 owned interviews with currently visible job metadata and the owner's review, Cache-Control: no-store; or schedule an owned application's interview with result defaulting to PENDING |
| GET /api/interviews/{id} | One owned interview with currently visible job and same-owner review, Cache-Control: no-store |
| POST /api/interviews/{id}/finish | result PASS/FAIL/PENDING and notes (max 8000 bytes); PENDING also records completion while awaiting feedback; later updates preserve the first finished_at; does not automatically change application state |
| GET, POST /api/reviews | owner reviews; one validated review per interview |
| GET /api/weak_topics | accumulated review-backed topics |
| GET, POST /api/projects | owner projects |
| PUT /api/projects/{id} | rename one owned project |
| GET, POST /api/project_facts | IMPLEMENTED/LIMITATION/PLANNED facts; ownership checked against project |
| PUT /api/project_facts/{id} | edit one owned fact, retaining its project and creation time; explicit `verified` flag |
| GET, POST /api/documents | owner knowledge documents |
| GET /api/knowledge?q=Redis | local keyword Top-5 chunks |
| GET /api/jobs/{id}/preparation | job + current_requirements + current_observations + input_identity + eligibility/fit + project facts + weak topics + knowledge |
| POST /agent/decide | session_id, message, optional per-request model_config; synchronous final result |
| POST /agent/stream | same body; SSE lifecycle |
| POST /agent/actions/{id}/confirm | explicit `{"confirm":true}`; revalidates owner, TTL and transaction state |
| GET /agent/traces/{runID} | sanitized owner-scoped 24h trace |

Agent write tools are **not** mapped to direct CRUD routes. They call `Tools.Propose`, which only creates an expiring Redis preview. Direct authenticated human CRUD requests are already explicit actions. Confirmation uses a MySQL receipt in the same transaction as the workflow write.

Agent read tools also include `get_match_result {"job_id":"..."}`, `compare_company_jobs {"company":"..."}` or `{ "job_ids": ["...", "..."] }` (same-company selection of 2–8), and `get_match_tasks {}`. Their matching identity comes from the authenticated request's selected model; `mask_name` is an optional extra local redaction input, not a model-chosen tool argument. Comparison can cover up to 200 company jobs but returns at most 8 bounded summary rows and marks truncation. These tools reuse saved matches and current snapshots, never issue paid matching calls or expose stale scores. The independent stdio MCP tool allowlist is unchanged.

Resume files are read in the browser; the resume draft API never accepts a file or raw resume upload. Manual profile and project/fact writes still persist only the selected structured fields. The resume draft endpoint has a per-user model call limit of five per minute, validates exact source excerpts, and does not persist input or draft output. An optional `model_config` contains `url`, `model`, and `api_key` for this request only. It must be a public HTTPS Chat Completions endpoint; private DNS results, proxies, and redirects are rejected. The key is not persisted. If no per-request model or server default exists, the draft endpoint returns `RESUME_MODEL_UNAVAILABLE`; invalid model settings return `MODEL_CONFIG_INVALID`. Other resume-specific codes are `RESUME_TEXT_INVALID`, `RESUME_PII_DETECTED`, and `RESUME_DRAFT_UNVERIFIABLE`.

Profiles additionally support up to eight `educations` records (`id`, `degree`, `majors`, `start_year`, `graduation_year`, optional `graduation_month`, `status`) and `primary_education_id`. Status is ENROLLED/GRADUATED/UNKNOWN; an unstated year is 0. Bachelor and master histories retain their own majors and dates. The selected record supplies the scalar degree and graduation year used for campus qualification; selection defaults deterministically to the highest degree, then latest graduation year. Generic major checks can use all recorded majors; an explicit degree-bound major check uses only the corresponding history. Model candidate major facts carry their owning degree. Old scalar profiles remain readable. An older client omitting `educations` cannot erase existing histories; explicit `[]` clears them. Existing revision CAS and owner boundaries remain. History lives in the existing profile JSON, requiring no table migration or additional model request. Graduation month is 0 when unknown, otherwise 1–12 with a known graduation year; it follows the selected education. Resume extraction accepts a month only when the source explicitly states the graduation endpoint.

Résumé drafts may include `educations` with the same academic fields plus a source `excerpt`, without model-generated IDs or primary selection. All retained education records, suggestions and project facts are selected by default in the browser. Users can edit or uncheck them before saving. Each project's single confirmation-and-save action marks only selected facts as user-confirmed (`verified: true`), preserving IMPLEMENTED/LIMITATION/PLANNED kinds. The browser does not save drafts automatically; confirmation means extraction/content review, not a code audit. Multiple conflicting legacy scalar suggestions are excluded rather than overwriting one another.

Résumé draft review preserves valid suggestions and facts when individual items fail validation. The response may include `warnings` with fixed `validation_reason`, `scope` (SUGGESTION/PROJECT/FACT) and one-based positions in the original model output. Quotes differing only in Unicode whitespace may be restored to a unique, contiguous original source slice; `normalized_excerpts` counts these restorations. Changed wording, punctuation/casing, concatenated passages, ambiguous restorations and oversized excerpts are never accepted. A project without a valid name and source quote cannot retain its facts. Plans and limitations keep their original kinds. If exclusions leave no usable suggestions or facts, the endpoint still fails with RESUME_DRAFT_UNVERIFIABLE.

Résumé failures include a safe `diagnostic` and `request_id`, distinguishing invalid JSON/schema, limits, values and excerpts without returning or logging rejected text. Both success and failure responses use Cache-Control: no-store. The model cannot supply review metadata. Original files remain in the browser; only the explicitly reviewed redacted preview is sent, once per user request. Draft review neither persists the submitted text nor saves suggestions/facts automatically and does not retry paid calls.

Operational DLQ has no public REST endpoint; use `go run ./cmd/dlq` and explicit `-redrive ID`. Worker metrics bind `127.0.0.1:18081`. Source registration is a local CLI operation, not a way for untrusted HTTP clients to create official evidence.

Manual chat export is a read-only snapshot: it never includes account IDs, raw resume uploads, project references or credentials; project names appear only as redacted context. Missing latest successful JD text returns `MATCH_EXPORT_TEXT_REQUIRED` (409); changed candidate review returns `MATCH_INPUT_CHANGED` (409); count/serialized size overflow returns `MATCH_EXPORT_CAPACITY` (400). Duplicates and invalid IDs are rejected, and any inaccessible selected job rejects the whole export. The browser packs and downloads reviewed files locally; no export is persisted or automatically sent to ChatGPT, and there is no chat-result import endpoint.

The canonical evidence normalization vocabulary and import fixtures are in `internal/domain`, `internal/analysis` and `testdata`. Errors returned to HTTP are deliberately generic to avoid exposing SQL/schema or private payload contents; internal tests assert concrete error types.

Release repair: preparation fields are a single current input snapshot; historical evidence is available separately through the evidence endpoint. Knowledge ingestion/search returns HTTP 409 with `code: CORPUS_CAPACITY` when the searchable 10,000-chunk bound would be exceeded/is already exceeded. Agent `OUTPUT_LIMIT` is a terminal partial/limited outcome. Confirm replays a completed owner-scoped SQL receipt before consulting Redis pending data. Query evaluation is separated from persistence; explicit assessment uses input CAS (stale inputs return a conflict).

Precise graduation ranges retain a literal `graduation_window` with date bounds and source context. A year or month passes only when its whole interval falls within the range; overlapping boundaries stay UNKNOWN. Saved comparisons receive read-only local qualification repairs, including a clearly stated minimum degree; paid ability judgments and analysis dates are preserved. Broad computing-related majors use a small explicit family and respect degree scope. Unsupported gates such as admission mode remain UNKNOWN. Explicitly requested new comparisons can split conjunctive foundation requirements from cached extraction; completed ability units are not copied into new parts. Inventory responses include compact section `breakdown` counts, without candidate evidence bodies. Pure attitude requirements are excluded from technical scoring even if mislabelled by the model.

## Job Radar v0.2 additions

All `/api/*` routes use the existing JWT owner scope; there is no body/query owner override.

| Endpoint | Contract |
| --- | --- |
| GET /api/sources/catalog | Implemented campus presets (company, canonical URL, scope, minimum_interval, supports_direction); no external call or source creation |
| GET /api/sources | Visible operator-registered source catalog, up to 100 |
| GET, POST /api/watches | Owner list / create; `source_id`, `check_interval` in seconds (300..604800), `keyword` (0..100 bytes), `enabled` |
| GET, PUT, DELETE /api/watches/{id} | Owner read / replace settings / delete; PUT retains source_id; enabling/disabling uses the enabled field |
| GET /api/radar/todos | Owner-only read snapshot; `as_of`, complete bounded `counts`, at most 5 `items` per kind, per-kind `truncated`; PLANNED_CLOSING / INTERVIEW_UPCOMING / REVIEW_PENDING / ANALYSIS_FAILED; no model or quota use, Cache-Control: no-store |
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

Local direction screening uses `local-screen-v4` independently of paid matching identities. It recognizes Agent/AI application roles and platform aliases, uses generic software titles only as a browsing fallback, and preserves concrete title/duty citations. All target roles are considered together: a direct hit does not become RELATED just because another target is adjacent. Skills/qualification parsing limits no longer imply incomplete direction; truncated duties, conflicting roles and genuinely unsupported targets remain UNCERTAIN. Source metadata, introductions, collaboration references and bonuses do not establish own duties. Local priority and company comparison consume the same direction classification. No schema migration, model call, automatic cleanup or invalidation of verified paid comparisons is required.


API comparison input separates `candidate.facts` (qualification and ability facts) from `candidate.limitations`; preferences are resolved locally and omitted from the model input. A structurally valid positive judgment with an exact but inappropriate preference/limitation citation is withdrawn as NO_EVIDENCE with empty evidence and local `review_note=INVALID_ABILITY_EVIDENCE`. Other verified items remain; unknown IDs, forged excerpts, missing/duplicate items and schema failures still reject the output. Models cannot submit `review_note`. The analyze response includes `evidence_reviews` for newly saved withdrawn items, and preparation/company rows carry the same count when nonzero. Withdrawn items lower coverage, never count as an ability match or mismatch, and do not trigger another model call. Existing candidate hashes and requirement caches are preserved.

The API model wire format supplies each fact as `id`, `kind`, optional `project_name`, and ordered `excerpts:[{"id":"e1","text":"original source chunk"}]`. Chunks partition the complete fact without rewriting or duplicating text, at most 600 UTF-8 bytes each. Model evidence selects `{"id":"fact id","excerpt_id":"e1"}`; IDs are scoped to the owning fact. The server resolves each selection to a literal source substring before applying the unchanged semantic and persistence validators. Saved/public results still use `{"id":"fact id","excerpt":"original text"}`. Capacity checks measure the actual indexed input; canonical candidate hashes and verified comparison caches stay unchanged because source facts and judgment rules are unchanged. The manual ChatGPT export format remains compatible.

Legacy model evidence containing only `id` and `excerpt` remains accepted with exact grounding. Whitespace differences may be restored only to one uniquely identified, length-bounded source span; translations, paraphrases and punctuation changes are rejected. New safe diagnostic reasons are `EXCERPT_ID_UNKNOWN`, `EXCERPT_REFERENCE_CONFLICT` and `EXCERPT_AMBIGUOUS`. Invalid references still fail the job without persisting a positive result or automatically making another paid call; parsed requirements remain reusable on explicit retry.


## Durable matching tasks and application campaign limits

`GET /api/profile/resume/capabilities` includes `durable_matching` and `application_campaigns`. Old synchronous `/api/matching/analyze` stays compatible and shares the external-call lease. New UI prefers durable tasks.

- `GET /api/matching/tasks`: latest 20 owned tasks, including state, version, per-item status and safe diagnostics. Expired executions become WAITING_AUTH.
- `POST /api/matching/tasks`: `{job_ids,input_keys,candidate_hash,request_key,mask_name?,model_config?,retry?}`, max configured round limit (at most 100). `request_key` is 16–64 ASCII alphanumeric/underscore/hyphen. Returns 202 with durable task; replay uses user/key plus stable input fingerprint. One active round per account; no key or outbound text is persisted.
- `GET /api/matching/tasks/{id}` and `/events`: owned task and latest 200 safe stage events.
- `POST /api/matching/tasks/{id}/control`: `{version,action:"PAUSE"|"CANCEL"}`; version conflict returns 409. Pause finishes the current batch, cancel revokes ownership immediately.
- `POST /api/matching/tasks/{id}/resume`: `{version,job_ids,input_keys,candidate_hash,mask_name?,model_config?,retry?}`. Explicit reviewed unfinished/failed subset only. Candidate/model and each input key must match; other pending items stay paused. Never automatically resends paid work after restart.
- `GET /api/application-campaigns` returns owned rules with planned, submitted, remaining and conflict counts. `/catalog` returns currently visible job metadata.
- `POST /api/application-campaigns`, `PUT /api/application-campaigns/{id}`: `{company_id,name,limit,job_ids,rule_url?,confirmed:true,version?}`. Owner-scoped job assignments, one rule per user/job; 50 rules/user, 200 jobs/rule, limits 1–10. Edits require current version.
- `DELETE /api/application-campaigns/{id}`: `{version}`. Removes only rule/assignments; application records remain.
- `POST /api/applications` accepts optional `submitted:true` for human truthful APPLIED recording. Plain creation reserves planned quota transactionally and returns `409 CAMPAIGN_LIMIT_REACHED` if full. Actual submitted records are allowed even over quota and surfaced as conflicts. Submitted withdrawn/rejected records retain usage. Agent tools cannot call the human-only submitted-record action.
- `GET /metrics/prometheus`: Prometheus histograms, safe counters and runtime/DB gauges. Existing JSON `/metrics` remains unchanged. Worker exposes the same additional metrics format.

See [backend-upgrade.md](backend-upgrade.md), [performance.md](performance.md) and [operations.md](operations.md) for measured results and execution/privacy boundaries.

## Adaptive source monitoring

`WatchInput`（POST/PUT `/api/watches` 和 POST `/api/sources/from-url`）增加可选布尔字段 `adaptive`、`priority`，缺省均为 false。`check_interval` 是基础秒数。返回 `WatchTarget` 增加 `effective_interval`、`schedule_reason`、`stable_rounds`、`failure_rounds`、`round_changed`、`round_started_at`、`discovery_hash`。调度原因：FIXED/BASE/PRIORITY/RECENT_CHANGE/UNCHANGED_BACKOFF/FAILURE_BACKOFF。配置更新清空历史节奏、增加 generation，并立即重新排队；抓取权限和可见性约束保持。QUEUED 表示等待后台检查，RESTORED_PAUSED 表示恢复副本尚未重新启用。

备份恢复与保留仅为本机 CLI 运维功能，没有暴露可导入 SQL 或清理全库的 HTTP/Agent 工具。见 `docs/recovery.md`。

## Today actions and inventory workflow projection

`POST /api/matching/preview` adds optional `jobs[].application` with `id`, `job_id`, `current_state`, `version` and optional `applied_at`. This is joined from the authenticated owner's applications in the same read transaction and restricted to currently visible jobs. No notes, resume names or owner ID are included. Workflow and preference changes do not alter candidate hashes, comparison identities or the paid analysis cache.

`GET /api/radar/todos` reads a consistent SQL snapshot independently of the 500-job global Radar bound. Closing plans require current confident deadline evidence within the next 7 days and exclude ignored, closed, already-submitted or terminal applications. Upcoming interviews cover the next 7 days, exclude completed interviews and terminal applications; completed PENDING interviews can instead enter REVIEW_PENDING, including reviews of ended applications. Only the owner's latest attempt per job can produce a failed item; active/cancelled/superseded tasks, ignored/closed/inaccessible jobs and failures resolved by later synchronous successful analysis are excluded. No task recovery or external call is performed by this projection. It returns exact counts within capacities of 500 visible non-ignored plans, 1000 owned interviews and 500 latest failed candidates; overflow returns HTTP 409 `TODO_CAPACITY`, never a partial count presented as complete. Each category returns its first 5 items with `truncated` set when more exist. Items contain only safe job context, navigation IDs, round and the relevant timestamp; no candidate content, application notes or raw model diagnostics.

## 校招来源扩展

`POST /api/sources/preview` 和 `POST /api/sources/from-url` 支持小红书、百度 GRADUATE、美团应届生、京东 present、网易互联网 2027 届（103）、阿里巴巴 2027 届应届生（100000760001）、哔哩哔哩应届生（freshmen）、快手 2027 届应届生（20271779425607）、OPPO 2027 应届生（30）、西门子中国官网校招分类（CAMPUSRECRUITMENT）、海尔集团2027校招（68）十一个固定范围。预览增加 `adapter`、`company`、`minimum_interval`（1800 秒）、`supports_direction`，原 `url/name/project_code/total/samples` 保留。创建来源只使用服务端预览解析出的 adapter 和项目，调用方不能自行注入 adapter、tenant 或官方可信度。用户来源仍为 PRIVATE/MANUAL，按账号+adapter+项目去重。

非支持范围、带其他筛选或内推参数的 URL 返回 `SOURCE_URL_UNSUPPORTED`；源超容量返回 `SOURCE_PREVIEW_CAPACITY`，不创建关注。百度、美团、京东、网易互联网、阿里巴巴、哔哩哔哩、快手、OPPO、西门子和海尔集团不支持 `direction`，非空值在创建/修改时拒绝；标题/地点 `keyword` 使用完整列表的本地筛选。每个公司站点的预览与后台抓取共用来源限速，预览自身仍每账号每分钟 5 次。网络不可达返回 `SOURCE_PREVIEW_NETWORK`，不会以部分扫描代替成功。美团当前本机直连验证未通过，详见 campus-sources.md。

京东首页或 `#/jobs` 会归一到应届生 `#/jobs?type=present`；实习/TGT 与其他筛选片段拒绝。网易只接收 `/app/job/position?id=103`，主页不自动推断招聘项目。导入岗位地点上限扩展为 300，超限仍拒绝；完整公司来源上限仍为 500 岗。

阿里巴巴仅接收 `/campus/position?batchId=100000760001` 的固定校招范围；主页、实习 batchId、内推/其他筛选参数均拒绝。预览前核对公开 graduate 批次的编号、名称与类型，每个岗位核对批次与 freshman 类型；完整发现后才按关键词过滤。公开查询需要由该官网页面签发的匿名会话及 CSRF 令牌，服务端按操作建立临时内存会话，查询不接收客户端 Cookie 或身份令牌，亦不保存这些会话。

哔哩哔哩仅接收 `https://jobs.bilibili.com/campus/positions`，可带唯一 `type=3` 参数，归一到应届生预设；实习 type=0、其他专项/筛选/内推参数、凭据与异常域名拒绝。预览先按官网公开流程获取匿名令牌，再查询应届生列表；元数据核验 recruitType=1 与全职类型，详情还核验 positionType=3 和原编号。令牌端点禁止响应缓存；令牌、匿名会话按操作隔离并不持久化。响应 412 或匿名会话失效归类为访问限制，不立即重试访问拒绝。最低检查周期、共享站点限速、私有 MANUAL 来源与去重规则沿用。

快手仅接收 `https://campus.kuaishou.cn/recruit/campus/e/` 的首页（无片段或 `#/campus/index`）、`#/campus/jobs` 或带唯一 `recruitSubProjectCodes=20271779425607` 的岗位列表片段，并归一到 2027 应届生预设。其他项目、实习、快Star 专项页面、额外筛选、页码或内推参数拒绝。预览核对公开项目名称、年份、fulltime 类型和启用状态；列表及详情核验 schoolr、固定项目、fulltime、Release 和官网展示标识。每页 50 条并核验页号、页大小、页数、总数和去重，使用完整扫描后的本地关键词筛选。详情按岗位编号读取职责、要求，再核验该岗位公开的毕业范围；查询无需凭据，使用禁用 CookieJar 的公开客户端且只允许同源 HTTPS 跳转。


OPPO 仅接收 `https://careers.oppo.com/university/oppo/campus/post`，可无查询参数或带唯一 `recruitType=Graduate`。从公开项目枚举核对项目 30 的名称、Graduate 类型与毕业要求，列表每页 50 条校验页号、页大小、页数、总数、每页行数及去重。详情使用 `idRecruitPosition`，不可替换成 ATS/projectPositionId；详情的 recruitmentType 可为空，但必须保留正确的项目编号、名称与“应届生”类型名。公开 Tenant-Id=1000 不包含个人凭据。原文保留职责、要求、知识技能、AI 能力、加分项与地区有别的毕业要求。

西门子仅接收 `https://jobs.siemens.com.cn/siemens/position/index?recruitmentType=CAMPUSRECRUITMENT`。按官网发布的 POST `/siemens/position/nextPageList` 表单读取，每页 15 条；从 HTML 的“共 N 个职位”、活动页号、总页数和行数核验完整分页，并逐行核验 recruitment 分类及编号。独立公开详情核验原编号、PUBLISHING 状态和 detailRecruitmentType，抽取岗位内容区域，保留工作经验与完整中英文要求，不混入导航、登录表单或客户端脚本。校招分类不意味着所有岗位具有相同毕业年份，资格判断仍以各岗位原文为准。

海尔仅接收 `https://maker.haier.net/client/campusmobile/activity/id/68/fid.html`。公开页面核对“海尔集团2027校园招聘”和所属类别链接，POST `/client/campusmobile/researchlist.html` 按项目 68、空类别/关键词、每页 50 条读取。官网不返回总数，`maxPage=1` 是终止标记而非总页数；预览也必须读取到终止标记才能显示总数。核验 status=1、activity_stop=false、终止标记存在、非终止页满页、编号去重、行内详情链接的项目/类别/岗位身份，最多 500 岗；无法核实终止时整次失败。详情链接名称 deliverfirst 只是官网公开内容页，读取不会执行投递、收藏或登录操作。详情核验 data-aid/data-rid，保留名称、职责、要求、地点及招聘部门，不据部门推定签约子公司。

上述 HTML 查询复用来源限速、一次网络/5xx 重试、1 MiB 响应上限及 GET 条件缓存。HTML 与 JSON 分别校验响应类型；POST 不使用条件缓存。新来源均不读取个人 CookieJar，仅允许各自官网同源 HTTPS 跳转，不更改公网 DNS 核验和代理禁用边界。

### SAP 中国 Graduate 来源

预设仅接收 `https://careers.sap.com/search/?q=&optionsFacetsDD_country=CN&optionsFacetsDD_customfield3=Graduate`，scope 为 `CN_Graduate`。查询只用官网公开职业阶段和国家筛选，不使用标题关键词推断应届身份。Student、Professional、其他国家、分页及额外私有筛选参数不作为入口接受。Graduate 可能包含有一定经验的毕业生，具体届别与经验要求仍以岗位原文为准。

公开 facet 查询核对 `customfield3=Graduate` 与 `country=CN`。HTML 列表每页25条，检查页码、页范围、总数、唯一原始编号及重复的桌面/手机版标题；空结果必须有明确的同范围提示，不能把推荐岗位当成结果。详情独立核对 canonical URL、标题、SAP 主体、Graduate 阶段及中国地点，只保留岗位正文和官网用工类型。列表没有用工类型，元数据保留 UNKNOWN，不将 Graduate 擅自标为全职或统一2027届。来源私有、最小周期1800秒，站点共享30次/分钟限制，同源HTTPS、响应上限、候选人会话隔离与条件缓存沿用。


### Manual chat result import

`POST /api/matching/export` returns `version=campustrace-chat-v3`, `candidate_hash` and each job's `input_key`. Chat returns only `{version,candidate_hash,jobs:[{job_id,input_key,requirements,matches}]}`. The package specifies requirement IDs, exact source quotes, optional supported qualification claim types/values and match citation IDs. Chat scores/ranks stay outside the JSON; the service calculates scores, section counts and eligibility.

`POST /api/matching/import/preview` accepts `{document,mask_name}` and returns `{preview_key,jobs:[{job_id,title,company,replaces,result}],evidence_reviews}`. It reads local records and makes no model request. `POST /api/matching/import/confirm` accepts the same document and mask plus `preview_key`. The preview is bound to current candidate/job inputs, normalized results and existing saved-result versions. Confirm revalidates and saves the whole batch atomically under the account lock; a change aborts all writes. Duplicate confirmation with an old preview is rejected. Both routes are authenticated and use `Cache-Control: no-store`.

Import is bounded to 100 jobs, a 2 MiB request and 36 requirements per job; each supplied job is processed once, incomplete per-requirement matches and duplicate IDs are rejected. It does not assert that an uploaded result includes every exported job. Fixed `MATCH_CHAT_FORMAT`, `MATCH_CHAT_INVALID`, `MATCH_CHAT_CAPACITY`, `MATCH_CHAT_STALE` errors carry only validation codes and numeric positions. Larger bodies use an explicitly bounded strict decoder; other routes retain their 64 KiB decoder limit. Nulls and unknown fields remain invalid.

Saved results have `source=CHATGPT_IMPORT` and a local `source_context_key` covering current default-redacted inputs. This hash lets an additionally name-masked export survive page reload without storing the mask. Imported results remain readable with another selected API model, while any candidate revision or job-text change marks them stale. Their existing paid-analysis record is replaced only after an explicit preview confirmation. Import does not populate paid requirement caches, run a model or increment daily model usage.

## Agent tasks and resources

All routes below require the current account token. Keys and MCP tokens are transient request fields, never stored in execution records. Full limits and request semantics: [assistant guide](agent-harness.md).

| Route | Behavior |
| --- | --- |
| `GET /api/agent/skills` | Reviewed, versioned task definitions |
| `GET /api/agent/executions` | Latest twenty task summaries |
| `POST /api/agent/executions` | Start or replay `{skill_id, request_key, company?, job_id?, topic?, mask_name?, model_config?}` |
| `GET /api/agent/executions/{id}` | Current account task details |
| `POST /api/agent/executions/{id}/resume` | Explicitly resume remaining reads; optional current model/mask identity |
| `POST /api/agent/executions/{id}/cancel` | Cancel and fence late worker writes |
| `GET /api/agent/todos` | Refresh event cursor and list local reminders |
| `POST /api/agent/todos/settings` | Set `{enabled}` |
| `POST /api/agent/todos/{id}/dismiss` | Mark current account reminder done |
| `GET /api/agent/connectors` | Current account resource configuration, without tokens |
| `POST /api/agent/connectors/discover` | Bounded catalog read `{name,url,token?}` |
| `POST /api/agent/connectors` | Save selected `allowed_resources`; repeat catalog check; optional `id,version` for updates |
| `DELETE /api/agent/connectors/{id}` | Remove current account connection |

`/agent/decide` and its streaming route accept optional `skill_id`, `local_lookup`, `token_budget`, `complex_model_config`, and account-scoped `mcp_credentials`. Tool resource reads use opaque `resource_id`, not arbitrary URIs. Budget exhaustion returns `BUDGET_LIMIT`; recorded provider usage is separated from conservative admission estimates.

Agent execution responses include server-computed `can_resume` and optional `lease_until`. `can_resume` follows the SQL lease and five-attempt limit; clients must not infer it from `updated_at`. A fifth unsuccessful attempt transitions to `FAILED`; replay of a terminal task never executes more tools. Execution details, summaries and connector listings return `Cache-Control: no-store`.


## 公司投递决策与匹配评测

- `GET /api/matching/company-catalog`：当前账号可见公司的数量目录；加 `?company=...` 返回该公司岗位元数据，可在读取完整决策前缩小范围。公司目录上限 1,000，公司岗位目录上限 10,000，超限明确拒绝。
- `POST /api/matching/company-workspace`：接收匹配模型身份、company 与可选 job_ids，返回 comparison 和 workflow。完整读取上限 200 个岗位；workflow 包含当前范围岗位的截止、官网链接、投递状态，以及整家公司的个人限投规则和可见投递记录。只读、不调用模型，响应禁止缓存。
- `POST /api/matching/evaluation/export`：明确选择 1～16 个岗位，提交 expected_scope_key 与人工 reference（acceptable_top_job_ids、reason、reviewed=true；可附 pairs），返回 `campustrace-matching-eval-v1` 案例与当前可复用报告。输入变化返回 MATCH_INPUT_CHANGED，越界参考结论拒绝；不创建任务、不调用模型、不修改个人资料。前端展示完整脱敏文字后再下载。

这几个接口沿用账号认证、资料脱敏和岗位可见性检查。公司比较缓存仍单独绑定精确范围；名额、截止与应用状态使用资料快照的只读事务。创建计划复用已有写接口的事务与限投检查，不以页面显示的剩余数量作为授权。


### Learning retrieval

- `GET /api/documents?offset=0`: account-owned summaries, up to 51 for 50-item pagination.
- `GET /api/documents/{id}`, `DELETE /api/documents/{id}`: read/delete an owned document and its vectors.
- `POST /api/knowledge/search`: `{query,k,retrieval:{embedding?,rerank?,mask_name?}}`; returns `hits` plus retrieval mode, coverage, warnings, latency and model-call counts. Defaults to local keyword retrieval.
- `POST /api/knowledge/index/preview`: `{retrieval}`; returns sanitized inputs, model, endpoint, coverage and an input-bound preview key without calling a provider.
- `POST /api/knowledge/index`: `{retrieval,key,confirm:true}`; account lease and atomic commit, up to 32 inputs; stale previews refuse before dispatch.
- `DELETE /api/knowledge/index`: clear this account’s vectors and fence in-flight index writes, preserving documents.

`retrieval.embedding` and `retrieval.rerank` each use `{url,model,api_key}`. Keys are transient. They may also be supplied with `/agent/decide`, `/agent/stream`, or task start/resume; the server binds the account and reviewed name mask. The existing `GET /api/knowledge?q=...` remains a local keyword-only array response. Standalone stdio MCP also defaults to local retrieval because it does not have browser-held retrieval credentials. See [retrieval guide](knowledge-retrieval.md).
# Agent 下一步入口

问答结果与 `GET /api/agent/executions/{id}`、任务开始/继续响应包含可选 `next_actions`。每项含 `kind`、`label`、`reason`、`source_tool` 及适用的 `job_id`、`company`、`document_id`、`topic` 或 `run_id`，最多六项。它们是程序从成功业务工具观察中生成的只读导航提示，没有外部 URL、执行指令或写操作确认编号；进入目标页面时按当前账号重新查询。失效或取消的常用任务不生成提示，列表摘要不携带完整观察与提示。
