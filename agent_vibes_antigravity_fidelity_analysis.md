# Agent Vibes Antigravity Fidelity Analysis

## 1. Project business architecture

- VSCode extension starts and reconnects to a local bridge daemon, manages first-run onboarding, status UI, and commands.
- Extension manages certificates, trust, forwarding, Cursor domain takeover, and account sync into JSON files.
- `apps/protocol-bridge` is the actual gateway service.
- Anthropic `/v1/messages` is the unified entry for Claude Code CLI.
- Cursor uses a native ConnectRPC/gRPC protocol path instead of only compatibility endpoints.
- Model routing selects among Google/Antigravity, Claude API, Codex, and OpenAI-compatible backends.
- Context subsystem handles compaction, token accounting, tool protocol integrity, and continuation safety.

## 2. Antigravity native upstream fidelity implementation

### 2.1 Native transport layer

- Go worker is compiled dynamically or loaded from bundled binary.
- Build uses `GOEXPERIMENT=boringcrypto` and `CGO_ENABLED=1` to align TLS behavior closer to official LS.
- Worker transport explicitly forces HTTP/2 and registers h2 with `http2.ConfigureTransports`.
- Proxy support is implemented at transport level: HTTP/HTTPS/SOCKS4/SOCKS5.
- Requests use `Authorization: Bearer`, `User-Agent`, `Accept-Encoding: gzip`, and `x-goog-user-project`.

### 2.2 Endpoint and identity fidelity

- Worker defaults to `https://cloudcode-pa.googleapis.com`.
- Daily/sandbox endpoints are only used when explicitly overridden.
- Worker `User-Agent` is shaped as `antigravity/<version> <os>/<arch>` or `jetski/<version>`.
- `IDEVersion` is carried in account config and defaults to a fixed official version.
- Top-level payload injects Cloud Code-style fields:
  - `project`
  - `requestId`
  - `userAgent`
  - `requestType`
  - `enabledCreditTypes`

### 2.3 Session and request lineage fidelity

- `GoogleService` creates per-conversation logical request lineage.
- `ProcessPoolService` rewrites lineage per worker/account before send.
- Each worker owns a persistent `cloudCodeSessionId`.
- Each worker also owns per-conversation request sequence:
  - `agent/<timestamp>/<uuid>/<seq>`
- This preserves Cloud Code session identity separately per account.

### 2.4 Official payload shape fidelity

- Cloud Code request body is built with:
  - `contents`
  - `generationConfig`
  - `toolConfig`
  - `systemInstruction`
- `systemInstruction` includes official Antigravity prompt by default.
- Cursor-specific artifact/planning sections are stripped and replaced with Cursor adaptation text.
- Tool calling mode is set to `VALIDATED`.
- Search uses `requestType=web_search`.

### 2.5 Official tool surface fidelity

- Claude-via-Google path can use official Antigravity tool declarations.
- Official tool names are normalized and mapped centrally.
- Canonical invocation adapters convert tool names and parameter shapes to official Antigravity forms.
- Artifact metadata extraction and projection are also mapped from official tool contracts.

### 2.6 Thinking and thought-signature fidelity

- Anthropic `thinking` / `tool_use` / `tool_result` / images are converted into Cloud Code `parts`.
- `thoughtSignature` is attached to the next valid part when required.
- A persistent `tool_use.id -> thoughtSignature` cache is maintained across turns.
- Missing signature fields from Claude-side traffic are repaired from cache where possible.

### 2.7 Tool protocol integrity fidelity

- Tool protocol integrity is enforced in a shared pure-function layer.
- Invariants:
  - every `tool_use` must have a later `tool_result`
  - every `tool_result` must reference an earlier `tool_use`
- Orphan `tool_result` blocks are removed.
- Synthetic `tool_result` blocks can be injected for orphan `tool_use`.
- Strict-adjacent mode exists specifically for strict backends like Cloud Code.

### 2.8 Cloud Code recovery fidelity

- When Cloud Code rejects invalid tool history, Cursor path emits an interactive recovery query.
- Recovery options include:
  - start a new session
  - remove the bad tool call and continue
- Tool results are persisted into history before continuation.
- History is kept in official per-message shape to avoid Cloud Code rejection.

### 2.9 Pool, quota, and availability fidelity

- Each Antigravity account gets a dedicated worker.
- Worker pool tracks:
  - global cooldown
  - model cooldown
  - quota exhaustion
  - disabled workers
  - bootstrap completion
- `loadCodeAssist`, `fetchUserInfo`, and `fetchAvailableModels` are used for bootstrap/quota/probing.
- Model quota snapshots are cached and used in scheduling decisions.

## 3. Subapis current status vs Agent Vibes

### 3.1 Already present in subapis

- Antigravity request transform into Cloud Code/v1internal payload.
- Identity patch injection.
- MCP XML injection option.
- `requestType=web_search` handling.
- Tool config `VALIDATED`.
- Stable session ID generation from content or `metadata.user_id`.
- Claude/Gemini thought-signature related fallback logic.
- OAuth refresh and `project_id` backfill.
- URL fallback and smart retry / cooldown / failover logic.
- Optional TLS fingerprint profile framework.

### 3.2 Missing or weaker than Agent Vibes

- No dedicated per-account native Go worker pool for Antigravity.
- No persistent per-account Cloud Code `sessionId`.
- No per-worker conversation lineage rewrite `agent/<ts>/<uuid>/<seq>`.
- No explicit `Accept-Encoding: gzip` and gzip response path in Antigravity main request path.
- No `x-goog-user-project` usage on main Antigravity generation path.
- No BoringCrypto-aligned native transport build path.
- No official Antigravity tool declaration layer equivalent to `official-antigravity-tools.ts`.
- No persistent `tool_use.id -> thoughtSignature` cache across turns comparable to Agent Vibes.
- No unified strict tool protocol integrity framework for Cloud Code Claude path.
- No interactive Cloud Code tool-history recovery path.
- No dedicated `loadCodeAssist`/`fetchUserInfo`/`fetchAvailableModels` native bootstrap loop per worker.

## 4. Porting priority for subapis

### P0

- Add per-account Cloud Code identity state:
  - stable worker/account-level `sessionId`
  - per-conversation request lineage `agent/<ts>/<uuid>/<seq>`
- Ensure main Antigravity request path carries:
  - `Authorization`
  - `User-Agent`
  - `Accept-Encoding: gzip`
  - `x-goog-user-project` when available
- Add gzip response support in Antigravity main request path.

### P1

- Add persistent `thoughtSignature` cache keyed by `tool_use.id`.
- Add strict tool protocol integrity repair for Cloud Code-facing message history.
- Normalize Anthropic tool traffic into Cloud Code-safe per-message transcript shape.

### P2

- Add official Antigravity tool declaration/mapping layer for Claude-via-Antigravity path.
- Add model/bootstrap probe path:
  - `loadCodeAssist`
  - `fetchUserInfo`
  - `fetchAvailableModels`
- Use probe output to inform scheduling/quota visibility.

### P3

- Consider dedicated per-account worker model if in-process Go path still diverges materially from official upstream behavior.
- If transport fidelity remains insufficient, evaluate BoringCrypto-specific build path for Antigravity client execution.

## 5. Recommended next implementation order

1. Per-account/session/request identity lineage.
2. Main request headers and gzip/user-project parity.
3. `thoughtSignature` persistence and replay.
4. Tool protocol integrity layer for Cloud Code strictness.
5. Official Antigravity tool declaration surface.
6. Bootstrap/probe path and quota snapshots.
