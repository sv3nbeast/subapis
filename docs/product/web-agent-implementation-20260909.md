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
