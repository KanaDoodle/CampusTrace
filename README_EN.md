# CampusTrace

**A little radar for graduate job hunting: follow openings, compare roles, keep track (ง •̀_•́)ง**

[中文](README.md) | **English**

Too many careers tabs. Several roles at one company, but only one application allowed. A resume version here, an interview date there.

CampusTrace is a personal job-search tool built in **Go**. It brings recruiting updates, candidate profiles, role matching, applications and interview notes into one workspace, so you have fewer things to remember.

```text
Follow recruiting sources → Screen locally → Analyze selected roles → Plan applications → Interview and review
```

## What it helps with

| The everyday problem | What you can do |
| --- | --- |
| Checking careers pages over and over | Follow supported sources and let background checks record new postings and changes |
| Hundreds of unrelated roles | Filter by target direction, company and city; bulk-ignore clearly unrelated jobs and restore them later |
| Repeating your education and project details | Maintain one profile or import a reviewed resume draft |
| Wondering whether your projects fit a role | Start with local screening, then request model analysis with job excerpts and project evidence |
| Choosing among roles at the same company | Compare complete candidate experience and JDs, with first choices, alternatives and tradeoffs; individual analysis is optional |
| Losing track after applying | Record application stages, resume versions, interviews and reviews; check the Today list for next steps |

**Missing evidence does not mean missing ability.** Deep analysis reads complete saved projects and JDs, explains overall relevance and key gaps, and compares roles within a company. It does not score by counting requirements or technology names; generic soft expectations do not affect the ordering.

![CampusTrace job radar, with a job list and an evidence and action pane](docs/images/job-radar.jpg)

*The screenshot uses fictional companies, roles and projects. Starting the app does not seed sample jobs.*

## Get it running

You need **Go 1.25.9+, Git and a running Docker installation with Compose**. The first build downloads dependencies and images.

```sh
git clone https://github.com/KanaDoodle/CampusTrace.git
cd CampusTrace
./campustrace start --open
```

Once startup finishes, open [http://127.0.0.1:8080](http://127.0.0.1:8080/) and register your account. The UI is currently in Chinese.

The CLI starts the application, MySQL, Redis, etcd and two Analysis instances, then waits for them to become healthy. They keep running when you close the terminal.

```sh
./campustrace status                # Check services
./campustrace open                  # Open the UI
./campustrace logs api --tail 100    # Inspect recent logs
./campustrace doctor                # Check Docker and configuration
./campustrace stop                  # Stop apps; keep dependencies and data
./campustrace stop --all            # Stop dependencies too; keep volumes
./campustrace start                 # Start again
./campustrace restart --build       # Back up, rebuild and restart after changes
./campustrace backup                # Write a local backup to bin/backups/
```

The Compose project is always named `campustrace`. Put local settings in the ignored `.env` file. You can set `CAMPUS_HTTP_PORT`, `JWT_SECRET` and an optional server-default model. Use a separate JWT secret of at least 32 bytes outside local use. Backup and restore steps are in [the recovery guide](docs/recovery.md).

## Your first session

1. **Set up your profile — 求职资料.** Add education, graduation cohort, target roles, city preferences, languages and projects. Text-based PDF, DOCX and TXT resumes are read in the browser. Check the redacted preview before sending it for draft extraction. Draft items start selected; edit or uncheck them before saving. Bachelor’s and master’s records stay separate, and you choose which education applies to this recruiting round.
2. **Follow companies — 招聘来源.** Choose a source with an automatic-import adapter, preview its scope and set keywords and a check interval. Or choose **一键导入全部岗位** for a one-time background import from all supported presets. Progress survives refreshes, failed sources can be retried, and existing watches remain unchanged. Full imports can start once every 30 minutes; importing does not trigger deep model analysis. A directory link alone does not mean automatic import works. See [the source inventory](docs/campus-source-inventory.md) for supported scopes and verification notes.
3. **Screen before spending — 岗位雷达.** Start with relevant directions and filters. Local screening uses bounded rules and vocabulary without model calls. Select promising roles for deeper analysis, review the complete outbound material, then explicitly start. The default round contains up to 30 pending jobs; successful results are saved and failed items can be retried separately.
4. **Turn analysis into action.** Compare saved results for the same company, add an application plan and work through the preparation checklist. Comparison and checklist reads do not trigger model calls. Maintain explicit application limits for the same company and recruiting round; actual submission still happens on the company’s website.
5. **Keep the trail.** Record application stages, resume versions, interview dates and reviews. The Today list shows upcoming deadlines and interviews, missing reviews and recent analysis failures.

Local screening, whole-context relevance, eligibility and whether a role is open are separate judgments. Company comparisons use the complete candidate shortlist and explain first choices and tradeoffs; no item-count score predicts an offer.

The directory has **银行** (banks) and **证券** (securities) filters. Bank presets cover CMB technology, CITIC IT, Bank of China software and IT operations, and Bank of Communications headquarters fintech roles. Securities presets cover China Merchants Securities, Huatai headquarters, CSC, Guosen, Galaxy and CICC. Each preset shows its actual scope; graduation dates, internship assessments and employment requirements remain in the original text. Other entries link to official sites and explain what still needs adaptation.

## Models and privacy

In **模型设置**, choose an OpenAI or DeepSeek preset and enter your API key. A server-default model is also supported.

- Original resume files and original text are not uploaded. Resume extraction sends the redacted preview you approve. Automatic masking can miss details, so review it.
- Saved profile fields and project facts are stored in CampusTrace’s MySQL database, which runs locally in the default Docker setup.
- Browser-entered keys are stored per account in the current browser, without encryption. Calls pass them through CampusTrace to the selected provider; the app does not save them in its database.
- API calls can incur provider charges. The matching-call allowance is a request-count limit, not a spending cap for your entire model account. Failed or timed-out calls can still cost money.

You can use local screening and application tracking without an API key. You can also select jobs and use **导出到 ChatGPT** to review, download and manually upload analysis packages. Export itself makes no model calls. Return JSON through **导入聊天分析** to review the results locally. Confirm the valid jobs first; copy the repair checklist for the remaining jobs back into chat. New packages return whole-context assessments and optional company rankings; imports check their source bindings and key quotations.

## What the Agent does

Open **更多工具 → 求职问答** to ask about job evidence, project facts, preparation or analysis tasks. The Agent chooses from allowed business tools and reads data you can access.

Querying a saved match does not start a new deep-analysis round. Allowed writes, such as proposing an application plan or changing a watch, first produce a preview and require your confirmation. With an external model, the question and relevant tool results are sent to that provider.

Four built-in tasks collect context for company choices, interview preparation, review and today’s next steps. These are fixed, versioned read workflows: they save progress and can resume unfinished steps without starting paid analysis. Ordinary model-backed questions can choose permitted tools; business answers come from successful tool results.

You can maintain reviewed memories, continue a prior discussion, enable recruiting-change reminders and select textual resources from an HTTPS MCP service. Model settings support a separate model for complex questions and a bounded request budget. The optional Go practice runner executes user-submitted code and tests in restricted containers. See [assistant capabilities and limits](docs/agent-harness.md) and [memory and practice](docs/agent-workspace.md).

Without one, a limited offline router lets you inspect the tool flow. It is not a full chat model.

## Under the hood

This is also a Go backend practice project. Its main mechanisms address concrete problems:

| Problem | Implementation |
| --- | --- |
| A saved update must reach background processing | MySQL transactions and Outbox, Redis Stream delivery, database idempotency for duplicate messages |
| Closing a tab must not erase analysis progress | Durable task and item state in MySQL; reviewed continuation after interruption |
| Old analyses must not masquerade as current results | Input-bound whole assessments and company reports, exact-scope cache identities and guarded commits |
| Concurrent plans must not take the last slot twice | Transactional checks for explicit campaign limits and record versions |
| Fetching and models need bounded work | Worker pools, independent concurrency limits, deadlines, rate limits, backoff and dead-letter handling |
| Failures need an explanation | Request IDs, task-stage events, Prometheus metrics and reproducible performance measurements |
| Analysis instances run independently | The public [KanaRPC-Go](https://github.com/KanaDoodle/KanaRPC-Go) facade and etcd discovery |

Recruiting observations, source evidence and rule assessments are kept separate. An accessible page alone cannot establish that a job is open, and a closed job does not automatically end an existing application.

## Development and checks

To run application binaries on the host while keeping dependencies in Docker:

```sh
./campustrace stop
make deps
make dev-run
make dev-stop
```

Normal tests need no model key. Frontend tests need Node.js.

```sh
export GOWORK=off
go build ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/...
node --test web/*.test.cjs
make verify-boundary
```

Use `RADAR_RACE=1 ./scripts/verify-radar.sh` for real-dependency integration tests in isolated databases. `make eval` defaults to synthetic questions and scripted models, without external charges. Performance figures describe the documented test scenarios; see [measurement conditions](docs/performance.md).

## Further reading

Detailed guides are currently in Chinese unless noted otherwise.

- [Matching and profiles](docs/matching.md): resume import, local rules, whole-context analysis, company comparisons and cache behavior.
- [Architecture](docs/architecture.md): observations, queues, RPC, Agent / RAG / MCP, versions and limits.
- [Backend additions](docs/backend-upgrade.md): durable analysis tasks, transactional application limits and adaptive checks.
- [Source inventory](docs/campus-source-inventory.md) and [adapter notes](docs/campus-sources.md): scopes, verification and access limitations.
- [HTTP API](docs/api.md): endpoints and operator interfaces, with English technical descriptions.
- [Operations](docs/operations.md), [recovery](docs/recovery.md) and [Agent evaluation](docs/agent-evaluation.md).

## Still learning (´・ω・`)

Website changes, access restrictions and network conditions can break imports. Unsupported sources need manual browsing. Scanned resumes need OCR that is not implemented yet, and complex requirements still need human review.

CampusTrace targets personal use. There is no automatic application submission, notifications are currently in-app, and preparation-checklist progress does not sync across devices. Authentication, capacity and operational limits are listed in [the architecture guide](docs/architecture.md#当前限制).

Issues are welcome. Reproduction steps and a redacted error message help; keep real resumes, keys and database backups private.

License: [AGPL v3](LICENSE). See [dependency and upstream notices](docs/dependency-audit.md); KanaRPC-Go retains its KamaRPC-Go source history and attribution.

## Reviewed context, memory and Go practice

Matching now reads a single document assembled from saved, reviewed education, skills, complete project paragraphs and confirmed facts. Add omitted internships or research in Additional experience, then preview the saved analysis document. API calls and ChatGPT exports use the same document; removed extraction mistakes are not reintroduced from the original résumé.

In Job Q&A, manage reviewed preferences, decisions and corrections, or ask the Agent to remember something and confirm its preview. Continue a saved discussion explicitly; changed profile or memory prevents stale summaries from being loaded. Memory is context, never proof of skills or current business state.

Enable optional Go exercises with `./campustrace practice start`, then open Go practice under More tools. Run your implementation and tests, inspect real results and reload saved code. A separate trusted Docker controller enforces a fixed image, no network, read-only root, non-root execution, bounded resources and cleanup. This is a personal exercise runner, not a public multi-tenant isolation claim. See [reviewed context, memory and practice](docs/agent-workspace.md) for usage and limits.

### Recruiting assistant workflows

The assistant now has four versioned read workflows: company choices, interview preparation, review planning, and daily recruiting tasks. MySQL checkpoints preserve completed steps across interruptions. Relevant memory/tool selection, bounded read retries, optional public HTTPS MCP text resources, explicit model routing, usage reporting, and opt-in event reminders are documented in [the assistant guide](docs/agent-harness.md). Workflows reuse existing records and do not start paid job analysis or submit applications.

Learning notes now have their own page. Local BM25 is the default; an explicitly configured embedding service supports semantic retrieval, with optional reranking of up to 20 candidates. Indexing requires a redacted-text preview. Keys stay in the browser. [Usage, retrieval evaluation and limits](docs/knowledge-retrieval.md).
