# Web Agent / MONO implementation ledger

## Authority and scope

User approved the compact white MONO design and explicitly requested a goal and
implementation of UI + planned product capabilities. The goal is active; this is
NOT a claim that the complete product has shipped. Production/domain changes are
not authorized. Preserve the shared main checkout.

Source baseline: sv3nbeast/main 92e94fac4.
Branch: codex/web-agent-mono-20260909.
Worktree: /private/tmp/sub2api-web-agent-20260909.EAYQlL.

## Current slice: workspace foundation

Implemented:

- Standalone workbench layout at /workspace (existing /chat remains compatible),
  still behind existing authentication and web-chat feature/permission guards.
- White MONO tokens, compact controls, mobile sidebar, reduced-motion fallback.
- Task starting screen using real session data; project/file management entries
  and existing template assistant library, not fabricated task/file cards.
- Optional model/channel/price configuration dialog instead of a permanent
  right-hand parameter column.
- Conversation restored by URL session query, local search that does not remove
  the active session from canonical state, late-fetch protection when switching.
- Draft transfer when creating a session; accepted-only draft/attachment clearing;
  errors shown to users; keep partial content if final message reload fails.
- Stop control, IME-safe Enter handling, shift-enter multiline, disabled-submit
  guards; auto-scroll yields when the user scrolls up.
- Group sources by document identity and expand individual cited locations.
- Historical token accounting remains available under details. Removed the old
  per-message money estimate based on the currently selected model: it was not a
  reliable historical billing snapshot. Current model pricing remains visible.

Verification:

- 24 frontend tests pass across workspace entry, composer, sources, stream API,
  document attachments, Markdown safety and templates.
- Typecheck passes; targeted lint passes; production build passes with existing
  chunk-size, mixed-import and outdated Browserslist warnings.
- Local API smoke: login -> create session -> complete fixture SSE -> reload
  persisted user/assistant messages. Both saved; final assistant completed.
- No browser interaction/screenshot QA yet. Preview opening was queued via app.
- No backend production forwarding, scheduler, cache identity or billing code
  changed in this slice. Future task execution requires gateway regression gate.

## Local-only development environment

These are development resources created for this goal, NOT production.

- Frontend: http://127.0.0.1:3000/workspace
  retained terminal session 86845 (VITE_DEV_PROXY_TARGET=http://127.0.0.1:58080).
- Backend: http://127.0.0.1:58080, go run ./cmd/server, latest retained session 89556.
- Loopback fixture provider: http://127.0.0.1:58081, session 22284.
  Its responses explicitly say they are local protocol tests; it never calls an
  external model and never executes returned tools.
- PostgreSQL: container sub2api-webagent-pg-20260909, 127.0.0.1:15439.
- Redis: container sub2api-webagent-redis-20260909, 127.0.0.1:16389.
- Private configuration and fixture scripts:
  /private/tmp/sub2api-webagent-dev.KY9nYY/dev.env (0600), fixture-provider.cjs,
  smoke.cjs. Do not commit credentials or print them.
- Local model catalog group 2, model gpt-4o-mini, fixture account 1. Display group
  clearly states it does not call upstream. Models are fixtures, not live AI.
- The initial seed via admin API hit the compliance acknowledgement gate. No
  legal acknowledgement was made. Disposable local data was seeded as test
  fixtures; do not mirror this process on production.
- Current GOCACHE is an isolated /private/tmp/sub2api-webagent-cache.* directory
  returned by tool state. Retain during development and clean at final teardown.
- Existing host port 8080 was occupied; it was NOT stopped or modified.

## Remaining work (must not be marked complete prematurely)

1. Foundation hardening: view data loading failures, keyboard/focus flow,
   attachment lifecycle races, source relevance gate, mobile/large-text QA.
2. Persistent Agent tasks: tasks/steps/events with owner checks, durable state,
   worker leases, bounded execution, cancellation and reconnect without replay.
3. Real artifacts: deterministic constrained document renderers for PPTX, XLSX,
   DOCX; tenant-scoped storage/download, previews, revisions and validation.
4. Model-driven task planning/tool loop through existing user-managed billing
   identity. Capability validation, budgets, no fake completion, true terminal
   events; review all four gateway invariants.
5. Workflow UI: persisted task progress, artifact pane only when applicable,
   mobile full-screen preview, error/retry behavior and resume.
6. Productization: scoped custom assistants, sharing permissions, team/workspace
   design, connector approval boundaries, deployment documentation. External
   provider credentials and domain choices must not be invented.

Architecture direction: reuse Vue/Go and existing user/group billing; add a
separate task/artifact layer rather than turn a browser SSE request into a job
runner. File execution must not receive production DB or Docker socket access.
Prefer structured allowlisted rendering tools before arbitrary model-authored
code. Existing project/document APIs are reusable, but do not pretend a prompt
template is an executing Agent.

## Durable task foundation (second slice)

Implemented in migration 234 and web_agent service/repository/handler modules:

- Task and event persistence, per-user idempotency keys with request-hash
  conflicts, attachment-order preservation, session/document ownership checks.
- Admission serialized per user, maximum three active tasks. Default task
  deadline ten minutes and sixteen started execution steps.
- PostgreSQL SKIP LOCKED claiming with unique per-claim lease tokens, heartbeats
  and lease-fenced writes. Expired/uncertain executions become interrupted, never
  implicitly requeued. Expiry sweeps are bounded to 100 rows.
- Queued cancellation is immediate; running cancellation is cooperative and
  wins over a later completion commit. Delete/expire invalidates publication.
- A worker lifecycle independent of the HTTP request; browser observation abort
  is distinct from explicit task cancellation. Runtime shutdown interrupts work.
- Private session snapshots retain the enqueue-time context; keys/leases and
  snapshots are excluded from public task DTOs.
- Authenticated create/list/get/cancel/event-cursor APIs and typed frontend client.
- Options include tasks_enabled. It remains FALSE until a real executor has
  been configured and started. Default application wiring intentionally does not
  enqueue jobs with no executable consumer.

Validation:

- Worker/admission/DTO/handler unit tests plus race detector pass.
- Six real PostgreSQL integration tests pass in unique disposable schemas:
  ownership/idempotency, concurrent admission, exclusive leases/terminals,
  cancellation/expired execution, cancellation-vs-finish race, deleted-session
  fencing and step limits.
- Full service, repository, handler, routes and migration packages passed.
- Three frontend task-client tests plus existing stream/workspace tests pass;
  frontend typecheck passes.
- Migration applied only to the isolated local dev database by normal startup.
  HTTP smoke: options.tasks_enabled=false, list=200, unconfigured create=503,
  zero tasks enqueued; existing local chat still receives done and saves messages.

Regression review scope: workspace task foundation only. Existing model
forwarding, conversation streaming and cache/billing implementations are unchanged.
No user generation is replayed by this task state machine; attachment identity
and ordering are retained. DB waits and maintenance are bounded, and there are no
new model calls in the default-disabled runtime. No attributable P0/P1 found in
enabled paths. This is NOT a PASS for the unfinished file executor/model loop.

Known remaining integration requirements:

- A real allowlisted executor/renderer, artifact metadata+blob persistence,
  preview/download/version operations and confirmation that produced files open.
- Revalidate file availability/ownership in the executor, budget actual model
  calls and validate artifact-backed completion (not merely valid JSON).
- Wire executor lifecycle into startup/shutdown and advertise capabilities only
  after it is functional. Task UI is not yet bound to the new API.
- Interrupt/restart recovery is safe but currently manual, not checkpoint resume.
- No production deployment, domain configuration or live upstream replay occurred.

Next concrete step: implement the constrained Office artifact schema, renderer
worker and artifact repository, then wire model planning through the existing
user-owned gateway identity. Keep the full goal active.

## Constrained Office artifacts (third slice)

Implemented on top of 5314647d9; this does not enable the unfinished model loop:

- Private data-only renderer in `runtimes/web-agent-office`: validated native
  PPTX/DOCX/XLSX, editable text/tables/charts, bounded allowlisted formulas,
  spreadsheet recalculation, actual PDF previews and native file reopen checks.
- Separate non-root, read-only Docker worker with Noto CJK fonts, bounded memory,
  CPU, processes and temporary storage. No production credentials, Docker socket,
  arbitrary model code, uploaded Office archive parsing or URL fetching.
- Gateway client with separate renderer authentication, no proxy inheritance or
  redirects, response/expansion bounds, MIME/SHA-256/ZIP CRC checks, external
  relationship rejection and recursive validation of embedded chart workbooks.
- Private artifact blob store (0700 directory / 0600 files), immutable random
  keys, owned metadata-first access, safe download headers and authenticated PDF
  preview. Storage keys and revision specifications never appear in public DTOs.
- Migration 235: task-associated artifacts, lineage/version/parent links and a
  source-artifact task reference. Root and revision publication atomically write
  the artifact, task success and terminal event under the current live lease.
- Cancellation wins over later publication. Stale workers cannot publish, and a
  missing/deleted/cross-user revision source cannot silently become a new file.
  Quota admission includes PostgreSQL's actual JSON serialization size.
- Metadata listing avoids loading private 1 MiB revision specifications for every
  row. Typed frontend APIs cover artifacts, authenticated blob fetches, version
  cursors and source-version revision requests; no bearer token in preview URLs.

Evidence and issues found during validation:

- Local bundled Office first produced unreadable Chinese glyphs despite valid
  native files. That run FAILED visual acceptance. The independent image includes
  the required font and fails startup without it. Its final 4 slide pages,
  1 document page and 2 spreadsheet pages were rendered to PNG and inspected;
  Chinese text, native tables/charts and cached totals are visible and unclipped.
- Actual renderer-to-Go archive validation caught a library-default binary
  printer setting in PPTX. Removed the unrelated template relationship/part;
  retained the gateway binary-part rejection. Final real PPTX/DOCX/XLSX samples
  all pass the same gateway validator, including the PPTX chart workbook.
- Ten Python schema/native-render/protocol tests pass in the final isolated
  image with network disabled. Formula cached values 22 / 11 / 5 verified;
  formula-like input text remains literal. HTTP auth precedes rendering and
  runtime errors do not disclose private paths/content.
- Nine PostgreSQL task/artifact integration tests pass with `-race` in disposable
  schemas. Metadata tests use synthetic entries, not real user documents.
- Full Go service/repository/handler/routes/migration packages pass on the final
  code (service 174.486s; handler 34.374s). Focused real-archive tests pass too.
- Five frontend task/artifact-client tests, typecheck, targeted ESLint and the
  production build pass. Existing mixed-import/chunk-size/Browserslist warnings
  remain; no dependency upgrade was mixed into this slice.
- Presentation/document/spreadsheet skills influenced native editability,
  recalculation and render/visual validation. The private artifact-tool package
  was unavailable for a standalone production runtime; public Office libraries
  are the explicit fallback. No claim of artifact-tool-based generation.

Synthetic QA output: `/private/tmp/sub2api-webagent-office-docker-samples`.
Local-only image: `sub2api-webagent-office:local-test`, manifest-list digest
`sha256:2ae3eb6934568c846add139e2193ffd8d3c2df3b28447b40e9b764f7d840344f`.
Test containers exited and were automatically removed. No external model calls.

Review gate for this slice:

- Findings: no attributable P0/P1 in default-enabled paths. The incomplete
  executor/storage lifecycle remains a BLOCKER to enabling Agent execution.
- Range: 5314647d9 plus task-owned artifact/runtime/client changes.
- Matrix: new authenticated metadata/download APIs and a private, currently
  unconnected renderer; no existing upstream protocol/provider/cache/failover
  implementation is changed.
- Four invariants: existing conversation stream/cache-hit/cache-creation/TTFT
  paths are unchanged; no new model request or response buffering is introduced
  into those paths. Publication's one-terminal/ownership/lease boundaries have
  direct repository tests. This is not a live-provider performance verdict.
- Verdict: PASS for the disabled artifact foundation; BLOCKED for enabling or
  shipping the full Agent. No production/domain action was performed.

Required next steps (goal remains active):

1. Implement a real model planner using the existing user's group/model/billing
   identity, revalidating selected documents and source versions before use.
   Bound calls, token/cost budgets and structured output; no ambiguous replay.
2. Wire an artifact-backed executor. Success must use `PublishArtifact` rather
   than the generic `FinishTask` JSON path. Never publish a guessed file card.
3. Configure/start/stop the worker and blob store only after readiness checks.
   `tasks_enabled` remains false, the executor nil and artifact store unconfigured
   in default application wiring. No new task can be submitted yet.
4. Add staged/orphan reconciliation and deletion/retention before enabling file
   execution. On an uncertain DB commit, never delete blobs that may already be
   referenced by a committed artifact. Session/user deletion needs blob cleanup.
5. Bind actual task progress, cancel/reconnect, files/preview/versions and source
   revisions to the MONO UI; verify browser/mobile/keyboard workflows. Current
   API tests and sample previews are not end-to-end user-workflow acceptance.

## Artifact-backed model execution (fourth slice)

Base: 0bc04bda2. Previous goal turn was concrete progress, not a wait/blocker.
This slice implements the actual backend execution chain, while leaving the
application factory disabled until its remaining lifecycle gates are complete.

Implemented:

- Replaced generic executor JSON success with a typed artifact contract. A
  successful task must atomically publish a validated artifact/version and its
  terminal event; arbitrary `{}` or a claimed artifact ID is no longer success.
- Model planner revalidates current session/group/model access and selected
  document readiness/ownership/scope, uses the enqueue-time conversation
  snapshot and an explicitly owned source version, and refuses missing sources.
- Reuses the existing per-user/per-group managed Web Chat key. It verifies the
  returned key identity before a request. No admin/provider key reaches the model
  tool or browser. Existing gateway auth/subscription/billing middleware remains
  on the actual application's `/v1/messages` or `/v1/chat/completions` path.
- A loopback-only model caller sends a non-streaming request for one complete
  structured artifact, without changing existing chat stream adapters. One model
  request per task; no hidden schema-repair, retry, account or protocol replay.
- Preserves selected model/output limit, stable system/schema prefix, historical
  message ordering and Anthropic user-block `5m` cache controls. Limits: 384 KiB
  serialized input, 32768 maximum selected output tokens, four-minute model
  deadline, 4 MiB response and bounded headers, within the ten-minute task limit.
- Requires normal terminal reasons, rejects incomplete/refused/tool responses,
  excludes thinking from file content and accepts only complete JSON (or one
  exact JSON fence). The renderer independently validates the data-only schema.
- Real execution phases cover generation, rendering/preview and private storage.
  Partial storage failure cleans up its own files, including panic/cancellation
  paths. Known pre-commit publication rejection fails explicitly and cleans up;
  an uncertain commit preserves files and never repeats model generation.
- Persists stable non-sensitive failure codes and generation request ID/usage
  when available, including rendering failures after a paid model generation.
  It never publishes a partial artifact in a failure result. Missing usage is
  absent/unknown, not invented zero consumption.

Verification:

- New planner/client/executor tests pass under `-race`: both protocol builders,
  normal terminal text, reasoning separation, EOF/truncation/invalid responses,
  cancellation, redirect-secret protection, one-call behavior, context/input
  bounds, stable prefix, frozen revision data, wrong identity/source/document
  access, partial-file cleanup and uncertain commit/cancel publication races.
- Full service/repository/handler/routes/migration packages pass on the final
  code (service 175.436s, handler 34.410s); focused race tests also pass, including
  real transfer EOF and internal timeout distinct from caller cancellation.
  The first internal-timeout test fixture blocked in httptest.Server.Close:
  a stack dump showed its handler waiting without consuming the request body.
  Fixed the fixture by draining the body and bounding teardown; retained the
  cancellation/timeout assertions. No production timeout was relaxed to pass it.
  `go build ./...` and `git diff --check` also pass. No frontend code changed in
  this slice.
- `scripts/test-web-agent-office.sh` is a reproducible local integration harness.
  It builds a private Office test image, supplies only a fresh renderer token,
  publishes a random loopback test port and removes its test container on exit.
- Full real pipeline test passes under `-race` for document, slides and spreadsheet:
  six durable tasks (create + metadata-title revision for each kind), six model
  HTTP requests, actual Office/PDF rendering, actual private file reads, distinct
  revision hashes, retained original versions and cross-user download denial.
  All task/artifact PostgreSQL integration tests pass in the same harness (14.880s).
- The model HTTP endpoint is explicitly synthetic. These tests prove backend
  orchestration/identity propagation, not real-provider quality or production
  billing. The existing full gateway ingress/usage ledger must be canaried later.
- The first host-to-worker test failed before model generation because the
  internal Docker test topology did not expose the renderer to host Go tests.
  A loopback-published bridge test succeeded. Production's private internal
  network example was not changed. No production server or external model used.

Review gate:

Verdict: BLOCKED for enabling/releasing the full Agent; artifact-backed execution
code is safe to checkpoint behind the existing disabled application wiring.
Reviewed range: 0bc04bda2 plus this slice's task-owned service/repository/tests.
Affected matrix: new non-streaming OpenAI Chat and Anthropic Messages task caller;
existing provider dispatch/auth/cache/billing/streaming implementations unchanged.
Four invariants: terminal/lease/cancel/no-replay boundaries have direct tests;
cache prefix/TTL stability has request-builder evidence, but live cache hit and
creation accounting are not yet canaried; ordinary chat TTFT path is unchanged,
new task I/O has explicit bounds, but no live-provider latency verdict is claimed.

Remaining release/goal gates (do not mark the goal complete):

1. Startup/shutdown and readiness wiring. Default `NewWebChatService` still uses
   nil executor/store; `tasks_enabled=false`. A real worker must not claim old
   queued tasks before the local gateway has started accepting connections.
2. Durable staged blob ownership, quota admission and reconciliation/retention,
   including user/session deletion. Coordinate publication and garbage collection
   so a lost commit acknowledgement can never cause a referenced file deletion.
3. Bind the MONO task controls, progress/error codes/budgets, artifact preview,
   downloads, version selection and edits; verify desktop/mobile/keyboard flows.
4. Full local gateway/auth/billing canary with a controlled upstream fixture, then
   separately authorized live-provider validation. No production/domain action.
5. Continue the remaining planned workspace/assistant/source relevance and
   product lifecycle requirements from the goal; this is not a reduced objective.

## Registered file lifecycle (fifth slice)

Base: 4b9538b33. The preceding turn was progress. This slice closes the physical
artifact ownership/cleanup gap; it does not complete runtime/UI integration.

Implemented:

- Migration 236 adds a durable blob ownership journal that survives task/user
  deletion, and a database-bound storage-volume identity. Artifact specifications
  retain bounded original JSON text rather than JSONB numeric expansion.
- Reserve 33 MiB before any model call (32 MiB file+preview plus 1 MiB spec), against
  a 500 MiB per-user artifact-data quota. Record both server-generated file keys
  first; after writing, reduce the reservation to actual data size. Outstanding
  cleanup still occupies quota until removal is confirmed.
- Cross-process shared/exclusive file locking coordinates writes and cleanup,
  including filesystem I/O that outlives a cancelled database/request context.
  Permanent lock/volume identity files are private and must not be replaced.
- Different volume identities are rejected before writes, downloads or cleanup.
  Legacy beta records cannot be silently adopted from an arbitrary directory.
- Writing revalidates the stage and live execution lease while holding the shared
  store lock. Publication locks the ready journal row, then rechecks the task
  against the current database clock; artifact/task/journal commit together.
- Cleanup uses the exclusive store lock, locks a registered journal candidate,
  takes a fresh visibility snapshot, and only deletes its recorded keys. It never
  scans filenames to infer ownership. Active published references are protected,
  including when the caller lost a successful commit acknowledgement.
- Task/storage transactions explicitly use READ COMMITTED so per-owner admission
  and post-lock visibility checks do not depend on a server default snapshot.
- Authenticated version deletion and occupied-quota APIs plus frontend clients.
  Deletion hides the selected version immediately; physical cleanup is asynchronous.
  Successful cleanup purges private spec/title, retaining an inaccessible version
  tombstone so deleted version numbers are not reused.
- Background task maintenance performs bounded expiry and file cleanup separately
  from model execution. Known cancellation retains available generation tracing
  but never exposes a partial artifact as a successful result.

Verification:

- Real PostgreSQL tests cover active-reference protection, owner deletion/cascade,
  idempotent cleanup after disk failure, quota release only after physical removal,
  wrong-volume refusal, pre-generation reservation limits, deleted version numbers,
  and cleanup blocked by an in-flight write even after context/lease expiration.
- Publication test observes a real PostgreSQL lock wait, expires the lease while
  blocked, and verifies publication is refused after the lock is released.
- A subprocess test proves filesystem locking works between separate processes,
  not only through a Go mutex. Reopening the same store preserves its identity.
- Full native Office pipeline (create + revision for all three kinds) and all
  task/storage repository integration tests pass under `-race` (16.452s).
- Service/handler task tests pass under `-race`, including no model/renderer call
  when storage reservation fails. Frontend six-client-test suite, typecheck and
  targeted ESLint pass. Full backend service/repository/handler/routes/migrations
  pass (service 176.345s; handler 34.591s); `go build ./...` and diff whitespace
  check pass.
- Initial old executor mock did not implement the new staged-store contract and
  failed as unavailable. Replaced it with a stage-aware mock and asserted actual
  abandonment followed by registered cleanup; the updated test passes.
- All deletion tests used unique disposable schemas/directories. Test containers
  were removed by the harness; no production or user-owned business files deleted.

Review: no attributable P0/P1 in the implemented, default-disabled artifact
lifecycle. Existing model request builders, streaming adapters, scheduling,
cache identity and charge accounting are unchanged. Direct tests cover task
terminal/lease/cleanup races; new waits and sweeps are bounded and off ordinary
chat's first-output path. This is not a production latency/cache canary verdict.

Verdict: PASS for this lifecycle slice; BLOCKED for opening/releasing the complete
Agent product. Goal remains active. The ordinary shared checkout remains untouched.

Next: runtime configuration + readiness + shutdown wiring (do not claim jobs before
the local gateway is accepting connections), then the MONO task/progress/artifact
pane and full browser/local-gateway canary. Also retain the outstanding planned
assistant/project/source-relevance and product lifecycle scope. Artifact quota is
not a total database-history retention policy; history policy must be explicit.

## Runtime wiring and task/artifact UI (sixth slice)

Base: 0471156f6. Concrete progress, not a blocker/wait-only turn.

- Added opt-in `web_agent` configuration (enabled, renderer URL/token, private
  storage path). Wire provider attaches the real planner/executor/store and starts
  the task service. Invalid optional configuration leaves ordinary chat/gateway
  available. Generated Wire output was regenerated, not hand-maintained.
- Startup readiness gates job claiming until local gateway and authenticated
  renderer health succeed. Renderer health now checks its dedicated token and
  protocol/kinds. A known active render is not marked unhealthy by a busy probe.
  Shutdown stops Agent work/maintenance before shared resources close.
- Options report availability and execution limits. File tasks accept an explicit
  allowed group/model and freeze it without mutating another browser's conversation
  target. Both gateway request ID and server-assigned client/billing correlation
  ID are retained; gateway deduplication/auth behavior is not changed.
- MONO workspace now has explicit Chat/Slides/Spreadsheet/Document modes, durable
  task feed with actual execution events, cancel, model/usage/error details,
  authenticated artifacts, version selection, revision requests and confirmed
  single-version deletion. Desktop artifact pane is optional; mobile uses a
  full-screen pane. Existing project/template/attachment APIs are preserved.
- Session-scoped observation cancels on navigation without cancelling execution.
  Local submission intents retain an operation key for an explicitly requested
  retry after an ambiguous response/reload. Task state remains server-owned.
  Guards cover stale fetches, deleted-file reappearance and history cursors.
- Preview uses PDF.js 6.3.289 as a canvas/text renderer, no embedded document HTML,
  PDF actions/forms or scripting layer. Page changes/closure cancel rendering and
  destroy document resources. The PDF library is lazy-split from shared vendor
  code (shared vendor restored to 337.93 kB; separate PDF chunk 483.12 kB).

Verification and evidence:

- 29 frontend tests pass across real workspace entry, explicit file submission,
  intent recovery without automatic replay, user/session isolation, preview
  cleanup and stale-page refusal, deletion confirmation, pagination, composer and
  sources. Typecheck, targeted ESLint and production build pass. Existing build
  warnings remain (large unrelated chunks, mixed imports, Browserslist age).
- Agent service/handler tests pass under race detection, including readiness gate,
  stop-before-ready, unavailable configuration isolation, authenticated health,
  and task-only model override. Configuration/repository/handler/routes/migration
  package tests pass; Wire generation and server build succeed.
- Updated real-Office integration now uses `ConfigureAgent` rather than manually
  assembling the executor. All six create/revision actions and repository/storage
  integration tests pass under race detection (16.502s).
- Full local application canary: authenticated disposable user, real gateway,
  real usage accounting, private Office worker, native file/PDF bytes and SHA-256
  download checks. Session 4, tasks/artifacts 7–12 all succeeded. Each task joins
  to exactly one usage row via `client:` + generation.client_request_id, all for
  local user 1 / group 2 / API key 1, 50 input / 30 output fixture tokens, test
  actual cost 0.0000255000. Six file actions plus ordinary chat produced exactly
  seven fixture requests; ordinary chat still delivered its done event.
- The fixture is explicitly synthetic and loopback-only. These are not real
  upstream model quality or production billing/latency samples.
- Full service-package run is NOT all green: exactly two unchanged tests fail
  after UTC midnight, both using `now.Add(-time.Hour)` as an assumed current daily
  quota window. Same command and same failures were reproduced at clean base
  0471156f6 in `/private/tmp/sub2api-webagent-baseline.MeyEvG`:
  `TestCheckSubscriptionModelQuotaRejectsOnlyConfiguredModelAtLimit` and
  `TestSubscriptionModelQuotaSurvivesAuthSnapshotForPreflightAndBilling`.
  Calendar-day reset correctly sees that timestamp as yesterday. No subscription
  billing source or those tests was changed to mask the failure. Classification:
  strictly reproduced baseline failures, not a passing full-suite claim.

Skills affected this work: gateway regression review added lifecycle/identity
tests; Apple Design guided compact controls and on-demand panels; Sites existing-
project/local-only workflow preserved Vue/Go/auth/storage rather than scaffolding
or hosting elsewhere. Browser screenshot/DOM/click QA was not performed, per the
Sites explicit-browser-testing boundary. Local preview request returned HTTP 200;
`open_in_codex` was queued, not proof that the user saw an authenticated workspace.

PDF references: [Mozilla examples](https://mozilla.github.io/pdf.js/examples/)
and [Mozilla removal of the eval compiler/API option](https://bugzilla.mozilla.org/show_bug.cgi?id=2029536).
The removed `isEvalSupported` option was not bypassed with a type cast.

Local runtime now running:

- Backend binary `/private/tmp/sub2api-webagent-dev.KY9nYY/server-agent-runtime-v2`,
  retained session 65857, loopback 58080. Log `env=production` is its default label,
  not a production deployment; it uses the isolated local database/Redis.
- Authenticated Office container `sub2api-webagent-office-dev-20260909`, loopback
  58082, private token held only in its owning local process environment.
- Synthetic provider `scripts/web-agent-fixture.cjs`, session 41203, loopback 58081.
- Frontend retained at loopback 3000/workspace. Local runtime artifacts live in
  `/private/tmp/sub2api-webagent-dev.KY9nYY/artifacts` with the volume identity.
- Original local backend/provider were stopped only after confirming their owned
  processes. Shared primary checkout and production remained untouched.

Review verdict: BLOCKED for production release/full-goal completion. No attributable
P0/P1 was found in exercised paths, but live-provider/cache/latency validation and
browser visual/interaction acceptance remain unproven. Ordinary gateway streaming,
cache prefix and charge implementation were not changed. Remaining scope includes
project source relevance, assistant/product lifecycle requirements, task-history
policy and final requirement-by-requirement audit. Keep the complete goal active.

## Explicit attachments and reference provenance (seventh slice)

Base: 6fbcb9817. The previous goal turn was progress. This slice fixes evidence-
backed reference semantics, not provider scheduling or similarity thresholds.

Root causes verified in source:

- `MessageDocumentIDs` combined explicit attachment links with all prior retrieved
  sources. Regeneration therefore promoted automatic retrieval into explicit
  attachment intent. `prepareBranchGeneration` additionally passed `true` for
  project retrieval even when the current session toggle was off.
- Leading attachment chunks were sorted by document ID, not selection order.
  Context assembly let a large first attachment consume the whole budget.
- Attachment lookup errors were ignored during replay, and missing/disabled
  explicit files could silently disappear from context.

Changes:

- Migration 237 stores ordered explicit attachment IDs on the user message.
  Assistant replay resolves its actual parent user turn. Legacy fallback uses
  only link rows; retrieved-source snapshots never become explicit selection.
  The ordered intent survives document-FK deletion so a retry reports missing
  files rather than pretending the user never selected them.
- Attachment linking is owned, scoped, enabled/ready checked, atomic and
  idempotent for the same selection; a saved turn's order cannot be overwritten.
  Session and active same-project files are supported. No-attachment turns retain
  the existing zero-extra-write fast path.
- Replay honors the current project-knowledge toggle and propagates lookup
  failures before creating a replacement turn. Missing explicit content is an
  error, not partial unannounced context.
- Retrieval and context preserve explicit selection order; each selected file
  gets a bounded excerpt before optional candidates. Repeated identical inputs
  produce identical context. Added origin, provided-character count, content
  fingerprint and truncation metadata; the model is told these are excerpts.
- File-task generation retains prepared reference snapshots for later inspection.
  Task UI reuses grouped sources; labels distinguish user-selected and retrieved
  references. UI calls them excerpts, not evidence that the model cited them.
  Stale source detail is cleared when its content changes.

Evidence:

- Service/repository/handler focused tests pass under race detection, including
  edited/regenerated turns with explicit files and project retrieval off,
  attachment lookup failure, deterministic ordering and fair budget allocation.
- Real PostgreSQL explicit-intent integration covers parent-user resolution,
  automatic-source exclusion, idempotence/order immutability, deleted-file intent,
  project/user boundaries and disabled files. Updated Office integration harness
  includes it; all repository and six native-file create/revision actions pass
  (19.307s).
- 24 frontend tests pass, including grouped-source provenance and stale-detail
  handling; typecheck, targeted lint and production build pass.
- The preexisting SQL already filters candidate relevance. A local synthetic
  PPT-vs-usage-text check returned similarity zero. No fuzzy threshold was tuned,
  and no claim of universal semantic relevance is made from this narrow example.
- Full repository/handler/routes/migration packages and `go build ./...` pass.
  Full service execution still reports exactly the two previously verified
  midnight-dependent subscription-model-quota baseline failures; a filtered JSON
  test run confirmed their names. No new Web Chat/Agent test fails. This is not
  an all-green full-suite claim. Migration 237 was exercised only in disposable
  integration schemas; the retained local gateway binary is still the prior
  runtime-v2 build and must be refreshed for a live reference-flow canary.

Review scope: web-chat/file-task knowledge preparation and source snapshots.
No provider transport, retry/account choice, billing or cache implementation was
changed. Prompt/reference content changes intentionally only to correct selection
and budgeting; cache identity remains stable for repeated equivalent inputs.
Live provider latency/cache and browser acceptance are still not established.
The full goal remains active; production/domain configuration is not authorized.

Verdict: PASS for the exercised reference-intent correction; BLOCKED for release
and overall goal completion pending remaining product and browser/live-flow gates.

## Artifact library and reference navigation (ninth slice)

Base: 36ff52830. Added the missing “file-first” product flow without changing
provider scheduling or chat streaming.

- The global 我的文件 view lists owned generated artifacts across sessions, groups
  the latest loaded version per lineage, filters by kind/title/file/session, and
  loads earlier pages without replacing already loaded results.
- Each artifact can open its authenticated preview/download/version pane, continue
  editing from the selected version, delete only that version after confirmation,
  or return to the original owned conversation. Sessions outside the recent list
  are fetched through a new owner-checked GET endpoint; stale navigation results
  cannot replace a newer selection.
- Project source material remains a separate expandable section. Generated files
  do not require reference-file storage to be enabled.
- Added loading/error/empty states and 44px mobile hit targets; file preview and
  version panels are full-screen on narrow screens. Existing MONO compact spacing,
  reduced-motion and keyboard focus rules remain in force.
- Source cards now identify explicit attachments versus automatically retrieved
  excerpts, their included character count and truncation status. This prevents
  users mistaking a budgeted excerpt for a complete file or model citation.

Evidence:

- 36 frontend tests pass in the preceding slice; after this library/navigation
  change the full affected frontend set passes **36 tests in 8 files** (including
  4 artifact-library tests and 7 workspace tests), plus typecheck, targeted ESLint
  and production build. The build creates a lazy `vendor-pdf-preview` chunk.
- Backend handler/routes, service and repository focused tests pass. Full affected
  backend packages pass after the latest edits: service 177.022s, repository
  1.692s, handler 34.383s, routes 1.650s, config 0.517s, migrations 0.022s.
  `go build ./...` and `git diff --check` pass.
- Local authenticated gateway canary remains valid: six Office task artifacts
  (document/slides/spreadsheet create + revision), native files/PDF previews,
  owner checks and ordinary chat completion. The local usage ledger shows one
  row per task, matching user 1/group 2/key 1 and fixture 50/30 tokens. No hidden
  model retry was observed.
- Browser screenshot/DOM/click testing was not performed because the Sites skill
  requires explicit browser-testing authorization. Local HTTP route 200, component
  tests and build are not a visual browser acceptance claim.

Unchanged baseline note: the two midnight-dependent subscription-model-quota tests
still fail in the same clean base and are unrelated to Web Agent. They were not
modified or masked. No production/domain action occurred.

Review verdict: PASS for this artifact-library slice; overall goal remains active
and release remains BLOCKED until browser/live-provider acceptance, history policy,
assistant/product lifecycle completion, and final requirement audit are done.

## Audit evidence: disabled-agent options

Added an actual Options-entry regression test for a valid chat configuration
without a task repository. Inspection and the test establish that the existing
nil-receiver-safe Ready/Availability methods already return tasks_enabled=false
and task_status=not_configured while preserving normal chat options. An initially
proposed duplicate nil guard was removed: this was not a demonstrated bug.
Only the regression test and audit record are needed; runtime behavior is unchanged.

This evidence does not change the overall release verdict: browser visual interaction,
live provider/cache/latency canary, assistant/team lifecycle and final full-goal
audit remain outstanding. No production or domain operation was performed.

## Cross-conversation artifact library (eighth slice)

Base: c25de7553. Previous turn was progress; this turn adds a missing product
navigation flow rather than declaring the previous partial UI complete.

- My files now shows owned artifacts across conversations, groups loaded versions
  by lineage, supports type/loaded-result search and cursor-based earlier pages.
  Search is explicitly labelled as searching loaded results, not the entire DB.
- Generated artifacts are separate from project source documents. The artifact
  entry no longer depends on reference-file/S3 storage being enabled.
- Reuses the existing authenticated preview/download/version/delete pane from the
  global library. Deletion updates both views; stale list responses cannot restore
  a confirmed deletion. Errors preserve already loaded files for retry.
- File-to-conversation navigation resolves the actual owned session, including
  sessions outside the recent list. Added authenticated GET session endpoint and
  API client; no client-supplied owner is trusted. Revision returns to that source
  session before setting its source artifact, rather than submitting to whichever
  conversation happened to be active.
- Older direct session links also use owned lookup. Navigation freshness checks
  prevent a delayed session lookup from overriding a newer session selection.

Validation: 36 frontend tests pass, including cross-session listing, version
grouping, paging failure/retry, user-switch stale-response exclusion, deletion
staleness, file-to-session navigation and old-session lookup. Typecheck, targeted
ESLint and production build pass. Full handler/routes packages pass (34.296s /
1.780s), backend build and diff whitespace checks pass. No provider/gateway,
scheduling, cache, charging or execution code changed in this slice.

Apple Design informed compact controls and grouped working surfaces. Sites'
existing-project/local-only path preserved Vue/Go and existing auth/persistence;
no Site registration, external deployment or browser interaction was performed.
The retained backend remains the earlier local runtime-v2 binary: the new session
GET route and migration 237 require a refreshed local binary for a live canary.

Goal remains active. Remaining audit includes actual browser acceptance (explicit
browser-testing authorization is still needed under the selected Sites skill),
local/live reference flows, assistant and product lifecycle scope, and history
policy. Shared primary checkout, production and domain configuration are untouched.

## Assistant/template execution boundary (eleventh slice)

Audited the selected Assistant flow. Templates are prompt presets, not independent
agents: the user edits the rendered text before sending, so a file task uses that
approved prompt and does not silently prepend a second template body. New sessions
and file tasks validate the selected template ID against the signed-in user, scope
and enabled state. File tasks freeze only template metadata (ID/name/update time)
in their private snapshot; the prompt remains the authoritative user text. Later
template edits or deletion cannot rewrite an accepted task.

Focused tests cover system/personal-owner acceptance, cross-user rejection,
disabled-template rejection and metadata-only snapshots. Existing assistant
library functionality is retained; this closes its permission/audit gap without
pretending a template implements tool execution.

The UI now also passes the selected template ID when submitting a file task, so
the server-side ownership check and metadata snapshot are exercised by the same
user action. The prompt body remains the user-edited text.
