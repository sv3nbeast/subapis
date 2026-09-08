# Constrained Office renderer (protocol v1)

This is a **private, data-only worker**, not a general code sandbox. It creates
editable PPTX, DOCX and XLSX files and PDF previews from the structured contract
in `schema.py`. It does not call models, fetch URLs, execute model-authored code,
accept uploaded Office archives, access gateway credentials or write to the
gateway's database. Native chart data remains editable.

Status: renderer and artifact persistence slice only. The main application's
task executor remains disabled until model planning, budgets, artifact-backed
completion and storage lifecycle are wired. Do not advertise full Agent execution
from this worker's health response alone.

## Deployment boundary

- Build the included Dockerfile. Runtime dependencies include LibreOffice and
  Noto Sans CJK SC; missing CJK fonts make startup fail rather than return blank
  Chinese previews. The renderer must not depend on a developer's desktop Office.
- `compose.example.yml` shows a private internal network, one CPU, 1 GiB memory,
  bounded temporary storage, read-only root, non-root UID 10001, dropped Linux
  capabilities and no-new-privileges. No host port is exposed by default.
- Set a separate random `WEB_AGENT_RENDERER_TOKEN` (at least 32 characters).
  Never reuse the gateway JWT secret or a model/provider key. Keep it out of Git.
- Attach the gateway to the worker's private network when integration is enabled;
  do not mount production env files, a Docker socket, database volumes or the
  artifact storage directory in the worker.
- The gateway's separate artifact directory must be private (0700), files 0600.
  Only authenticated user-owned metadata resolves a blob key; there is no public
  object URL and storage paths are excluded from API responses.
- Image base/OS packages are not release-pinned yet. Before a production rollout,
  lock the tested image digest and run the normal release/security review. This
  example is not authorization to deploy.

## HTTP contract

`GET /health` returns protocol version 1 and supported kinds. It describes this
worker only. One render runs at a time, so a busy worker may delay health checks.

`POST /render` requires `Authorization: Bearer <dedicated token>` and a bounded
JSON Content-Length. The request root contains `kind`, `title`, optional `theme`
(`mono`, `blue`, `warm`) and exactly one of:

- `slides`: up to 40 slides, layouts `cover`, `bullets`, `table`, `chart`, native
  notes. The layout budget rejects overly dense text instead of silently clipping.
- `sections`: document headings, paragraphs, bullets and tables; at most 50,000
  body/table characters. No arbitrary HTML, scripts or remote assets.
- `sheets`: at most 8 sheets / 20,000 cells, typed values, number formats, bounded
  allowlisted formulas, optional bar/line charts. Plain strings beginning with `=`
  remain plain text. Formula functions, cell ranges and referenced sheet names
  are validated; external books, DDE and network formulas are forbidden.

See `test_office.py` for complete synthetic examples of all three kinds. The
schema is the authoritative contract; unknown fields are rejected.

Successful responses contain `protocol_version`, `extension`, `mime`,
`file_base64`, `file_sha256`, `size_bytes` and `preview_pdf_base64`. The gateway
checks the MIME/extension, SHA-256, size, ZIP CRCs, required Office parts,
relationships and embedded chart workbooks before accepting a result.

Limits: request 1 MiB; combined file+preview 32 MiB; each Office conversion 90 s
with whole-child-process-group termination on timeout. XLSX is recalculated
before download and formula error cells fail the render. Gateway disconnects
prevent publication but do not immediately interrupt the private Office child;
its own deadline and isolated temporary directory still bound that work.

No response body, prompt, document content, authorization header or Office
stderr is logged. Errors use a small stable classification. A failed render is
not an invitation to replay a model request or switch accounts.

## Verification

Schema/protocol tests:

```sh
python -m unittest -v test_office test_server
```

Full integration additionally needs `OFFICE_BINARY`, the packaged Chinese fonts,
`WEB_AGENT_OFFICE_INTEGRATION=1`, and a writable synthetic-only
`WEB_AGENT_OFFICE_TEST_OUTPUT` directory. Run inside the isolated image with
`--network none`, mounting the two test modules read-only. The integration tests
reopen native files, assert chart/table editability, check cached formula results,
verify literal formula-like strings and generate all PDF previews.

Render every preview page to PNG and inspect it; file existence alone is not a
visual acceptance test. Feed the same synthetic sample directory to the Go
service tests with `WEB_AGENT_OFFICE_TEST_OUTPUT` to verify renderer-to-gateway
compatibility. Never use production user documents as committed fixtures.

For the complete Go task/planner/renderer/storage/version integration, run
`bash scripts/test-web-agent-office.sh` from the repository root with
`WEB_AGENT_TEST_DSN` pointing at a **disposable loopback PostgreSQL database**.
The Go tests use unique temporary schemas and synthetic users; never point this
at a production database or a tunnel to one. The script builds a test image,
creates a dedicated random worker credential, publishes a random loopback-only
port for host-side Go tests, and removes its test container on exit. Production's
internal-only network topology is not changed. No external model is called.

The integration covers all three Office kinds and one metadata-title revision
each: persistent task creation, one synthetic model HTTP request per action,
real rendering, native file/PDF download, unchanged old versions and owner
isolation. This is not evidence of real-provider quality, production billing,
browser UI acceptance or artifact retention/reconciliation.

The Go planner has a one-generation-per-task policy (no automatic repair call),
a 384 KiB serialized input ceiling, a four-minute model request deadline and the
session's selected output-token limit (at most 32768). Overall task deadline is
ten minutes. Normal completion is required before a complete JSON specification
is accepted. A truncated, refused, malformed or unrequested tool response cannot
publish a file. Missing upstream usage remains unknown, not a fabricated zero.
These limits must be shown by the task UI when execution is enabled.

## Artifact storage lifecycle

The gateway now journals both artifact keys **before** model generation or disk
writes. A task reserves 33 MiB (up to 32 MiB file+preview and 1 MiB specification)
against a 500 MiB per-user quota. After writing, the reservation becomes the
actual data size. Occupied quota includes active reservations and pending
deletions; it is not a physical-disk-usage or billing-dollar measurement.

The task must still own its execution lease before writing and publishing.
Publication atomically marks the journal entry published with the artifact and
task terminal state. Abandonment and failed/deleted task cleanup only remove
registered keys, never guessed files found by directory scanning. Cleanup keeps
its journal record and quota until removal succeeds, so retries after a disk or
database failure are idempotent. A successful artifact whose commit reply was
lost remains protected by the committed database reference.

The private store uses a permanent cross-process lock and a persisted identity:
`.web-agent-store.lock` and `.web-agent-store-id`. Writers hold a shared filesystem
lock; cleanup holds it exclusively. This also protects against filesystem work
finishing after a database/request context expires. Supported locking runtimes
are Linux/macOS and the listed BSD targets; unsupported platforms fail closed.

All gateway instances serving the same database must use the same shared artifact
volume with reliable advisory locking. A different volume identity is rejected
before writes, downloads or cleanup. Back up/restore the whole artifact volume
with its identity and database, not isolated metadata files. Never copy or replace
the identity just to bypass a mismatch. Legacy beta artifacts without a registered
store require a separately verified adoption; automatic guessing is blocked.

Authenticated DELETE hides only the selected version immediately and reports
`cleanup_pending`. The background task maintenance loop then removes the files
and clears their private specification/title, retaining a version tombstone so
deleted version numbers are not reused. The ownership journal intentionally
outlives user/session/task deletion until the physical files have been removed.

The main application still needs runtime configuration/readiness/shutdown wiring
and the MONO task/preview UI binding. These storage mechanisms alone do not enable
tasks in the default application factory.
