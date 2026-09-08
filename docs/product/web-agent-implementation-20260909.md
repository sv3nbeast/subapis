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
- Backend: http://127.0.0.1:58080, go run ./cmd/server, session 82294.
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

Next concrete step: implement and test the owned persistent task/artifact
repository and service boundary, then wire a constrained artifact worker.
