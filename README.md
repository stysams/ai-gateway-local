# ai-gateway

Local AI proxy gateway for Codex, Claude Code, Grok Build, and any OpenAI- or
Anthropic-compatible app. The agent stays in those clients. This process keeps
keys, routes, and protocol translation on a configurable local listener
(loopback by default).

OpenCodex hijacks and extends Codex. This project is a local gateway that
clients point at themselves.

## Status

The implementation-ready v1 specification is in
[docs/v1-scheme.md](docs/v1-scheme.md). It includes frozen architecture
decisions, API and protocol contracts, task packages, tests, and acceptance
criteria. Phase-1 progress and the next handoff live in
[docs/progress.md](docs/progress.md). The file-by-file code map for agents
is [docs/code-map.md](docs/code-map.md).

**Task package A (repository bootstrap & headless skeleton) is implemented.**
Scope delivered:

- Go module `ai-gateway`, `go 1.26`, toolchain pinned to `go1.26.6` (fetched
  automatically via `GOTOOLCHAIN=auto`).
- Config model, defaults, full validation and same-directory atomic writes.
  Unknown top-level YAML fields are retained on read and preserved on
  write-back. A missing config file is generated on first `serve`.
- Single-instance lock (`gateway.lock`, exclusive) with platform-isolated
  implementations (Windows `LockFileEx`, Unix `flock`) and the diagnostic
  `gateway.pid.json`.
- CLI: `serve`, `stop`, `status`, `version` with the unified exit-code table.
- HTTP on `127.0.0.1` by default, with an explicit `0.0.0.0` local-network mode;
  port conflicts fail loudly and never silently switch ports
  ports; graceful shutdown (signals, console close, management API).
- Endpoints: `GET /healthz`, `GET /readyz`, `GET /api/v1/status`,
  `POST /api/v1/shutdown`.

**Task package B (system key store) is implemented.** Scope delivered:

- `secret.Store` (Put/Get/Delete/Available) with distinct not-found and
  unavailable errors; API keys enter only through the write path and never
  appear in YAML, responses or logs.
- Windows: per-user DPAPI ciphertext files in `<data root>/secrets/` with
  atomic writes and strictest file permissions. macOS and Linux ship
  explicit build-time implementations (Keychain / Secret Service) that fail
  loudly — no plaintext fallback of any kind.
- Provider CRUD (`GET/POST /api/v1/providers`, `GET/PUT/DELETE
  /api/v1/providers/{id}`) with the §6.3 secret transaction: validate → write
  key → atomic config write → restore old key on failure; partial failures
  are reported explicitly for doctor. Delete never restores a provider, but
  surfaces key-deletion warnings.
- `readyz` and `GET /api/v1/doctor` check store availability and every
  required secret (missing + orphan refs); `serve` refuses to start when a
  configured provider needs a key that is missing or the store is
  unavailable, with a remediation hint.

**Task package C (routing & OpenAI Chat same-protocol forwarding) is implemented.**
Scope delivered:

- `internal/route`: the four fixed client ids (codex/claude/grok/generic),
  the reserved `gateway-default` model, and the §7.4 resolution order
  (route default → empty/gateway-default → provider-prefix override with
  prefix stripping → full-model passthrough to the route provider).
- `internal/inbound/chat` + `internal/outbound/openaichat`: Chat
  Completions parsing/rewriting that only touches `model` and `stream` and
  preserves every unknown field with its exact raw value (field-level
  lossless; document key order/whitespace are not guaranteed); upstream URL
  built exactly as
  `<base_url>/chat/completions` without double slashes or a duplicated
  `/v1`.
- Data plane: `POST /v1/chat/completions` (≡ generic) and
  `POST /c/{client}/v1/chat/completions` (unknown client or path → 404),
  JSON-only with the 128 MiB limit and Content-Type validation (missing or
  non-JSON → 415), OpenAI-native error shape, adapter dispatch gate
  (non-`openai-chat` providers answer 422 and never receive Chat
  requests), upstream status/body/key-header forwarding (Retry-After,
  X-Request-Id, OpenAI-Request-ID, rate-limit headers, Location — never
  Set-Cookie, Authorization or hop-by-hop), live SSE piping with per-chunk
  flush (never buffered), client disconnect cancelling the upstream,
  502/504 mapping (shared connection pool with `ResponseHeaderTimeout`,
  no overall stream deadline), redirects never followed (status + Location
  passed through, provider Authorization cannot leak to a second target),
  per-provider `Authorization: Bearer` injection from the key store
  (zeroed after use, never logged; store failures map to 500, not 502) and
  full inbound auth/header dropping.
- `GET /v1/models` and `/c/{client}/v1/models` returning
  `gateway-default` plus each persisted or discovered
  `<provider-id>/<model-id>`. `display_name` equals that selectable id,
  except `/c/claude/v1/models`, whose wire `id` is a reversible
  `claude-gw*` picker alias so Claude Code's `/model` command lists every
  enabled model.
- Desensitized fixtures under `testdata/protocols/chat/` and fake-upstream
  integration tests covering every C-package branch (four-client
  isolation, prefix override/passthrough, unknown-field preservation,
  auth injection/dropping without leaks, non-streaming, upstream 4xx/5xx,
  prompt SSE flush, client cancel).

**Task package D (IR & three-protocol conversion) is implemented.** Scope
  delivered:

- `internal/ir`: protocol-independent Request/Message/Block/Tool/ToolCall/
  ToolResult/Usage/Event types plus a stateful `Sequencer` validating the
  unified event stream (stable tool ids, ordered argument-delta
  concatenation, single `response.completed`, no success events after an
  error) and aggregating non-streaming responses. ir imports no concrete
  protocol package and adapters never call each other.
- Inbound: `internal/inbound/chat`, `responses`, `messages` parse requests
  into ir (text, system, tools, tool_choice, tool calls/results) with
  unknown-field preservation for same-protocol passthrough, and encode ir
  events as each protocol's non-streaming and SSE responses.
- Outbound: `internal/outbound/openaichat`, `openairesponses`, `anthropic`
  generate requests from ir and parse non-streaming and SSE responses into
  ir events; URLs are exactly `<base>/chat/completions`, `<base>/responses`,
  `<base>/v1/messages` (no duplicate slashes or /v1); OpenAI-style Bearer,
  Anthropic `x-api-key` + fixed `anthropic-version: 2023-06-01`; secrets
  zeroed after use, store errors → 500, never 502.
- Data plane: `POST /v1/responses`, `/v1/messages` and all
  `/c/{client}/v1/...` endpoints (no prefix ≡ generic). Same-protocol paths
  rewrite only model/stream/auth and forward upstream bytes; all six
  cross-protocol directions go through the IR pipeline only, streaming via
  a per-event SSE pipeline with flush after every event. Broken upstream
  streams end with a protocol error event, never a fabricated completion;
  client disconnect cancels the upstream.

**Task package E (images, reasoning & capability downgrade) is implemented.**
Scope delivered:

- URL and base64/data-URL image inputs are parsed into the common IR and
  generated in the native Chat, Responses and Messages request forms.
- Every data-plane path checks `capabilities.image_input` before contacting
  the upstream. Unsupported images return 422 and the upstream receives no
  request; supported images retain their URL/base64 source and detail.
- Request-level `reasoning_effort`, Responses `reasoning`, and Messages
  `thinking` are represented explicitly in IR. The directly equivalent
  Chat/Responses effort settings convert in both directions; Anthropic
  thinking modes and budgets are never guessed into OpenAI effort levels.
- Non-streaming and SSE reasoning/thinking output is parsed into unified
  reasoning events and encoded separately from answer text in all three
  inbound protocols.
- When provider capability is disabled or the target protocol cannot express
  reasoning without semantic loss, the gateway removes it before the
  upstream call and appends a `reasoning_dropped` warning JSONL event.
- Protocol fixtures and tests cover URL/base64 preservation, provider
  rejection with zero upstream access, OpenAI reasoning preservation,
  explicit cross-protocol downgrade, warning files, non-streaming responses
  and SSE reasoning events.

**Task package F (request logs and usage) is implemented.** Every accepted
data-plane request receives an `X-Request-Id` and an independent JSONL file
containing request, route, authentication-free upstream request, upstream and
client events, warnings, and a terminal result. `GET /api/v1/logs`,
`GET /api/v1/logs/{request_id}`, and `GET /api/v1/usage` provide filtered
queries and actual upstream token accounting; missing usage is explicitly
incomplete and is never estimated. `PUT /api/v1/logging` takes effect on the
next request, and doctor reports log writability, size, parseability, and
interrupted files.

**Task package G (complete management API) is implemented.** Config reads and
validated atomic updates preserve unknown top-level YAML fields. A provider is
a multi-model container: its editable model catalog persists model identifiers,
display names, context windows, and maximum output-token metadata. Provider
CRUD, real upstream probes, and draft model discovery use adapter-specific
authentication; upstream metadata is used only when published and unknown
limits remain unknown. Route updates cover all four clients and apply on the
next data-plane request.
Log toggling, filtered log queries, and usage queries complete the headless
management surface.

**Task package H (transactional client point and restore) is implemented.**
Codex, Claude Code, and Grok Build adapters preserve unrelated configuration,
create SHA-256 backup manifests, atomically point at client-specific loopback
paths, detect drift, and restore exact original bytes and the prior Codex
environment value. A route is the client's preferred model at startup only:
pointed clients keep the provider-neutral `gateway-default` model in that slot,
so route changes apply at request time without rewriting the pointed client or
replacing the original restore point. Every enabled
`<provider-id>/<model-id>` stays selectable inside the client — through
`/c/{client}/v1/models` for all three clients, as a cloned
`model_catalog_json` sidecar for Codex, as
`<CLAUDE_CONFIG_DIR>/cache/gateway-models.json` for Claude Code, and as
native `[model."ai-gateway:<provider-id>/<model-id>"]` entries in Grok
Build. Client status, point, restore, and doctor APIs are covered by
unit and HTTP integration tests. See [docs/install.md](docs/install.md) for the
operator flow and the required Windows client compatibility check.

**Task package I (Wails desktop main flow) is implemented.** The desktop shell
pins Wails `v3.0.0-beta.8` and embeds a React, TypeScript, and Vite frontend with
an npm lockfile. It starts the loopback gateway when needed and offers Overview,
Providers, Routes, Clients, Logs, Usage, and Settings views through management
HTTP APIs only. The provider editor can fetch an upstream model list before
saving, choose a default model, and manually override token-limit metadata.
A client route sets that client's default selected model; the picker inside
each client lists every enabled model as `<provider-id>/<model-id>`. The interface
includes first-run body-log risk acceptance,
Chinese and English, light and dark themes, keyboard operation, responsive
layouts, and automated 1440×900 and 390×844 browser tests.

**Task package J (tray, login start, and release) is implemented.** The native
tray reports gateway state, opens the main window, independently switches the
Codex, Claude, and Grok routes through management HTTP, toggles request logging, body persistence
and login start, starts or stops the gateway, and exits the desktop without
stopping the separate gateway process. A second desktop launch focuses the
existing window and does not create another process or tray icon. Closing the
window hides it while the tray remains active. Current-user login registration uses Windows Task
Scheduler with XML readback validation, launchd on macOS, and user systemd on
Linux. Windows release packaging injects version metadata into both binaries;
cross-builds produce macOS and Linux headless binaries.

The repository is a release candidate, not a completed Windows acceptance.
The real-client/provider flow and Windows logoff/login secret check in
`docs/v1-scheme.md` §19 still require an environment with installed clients,
credentials, and Task Scheduler permission. On this development machine,
Task Scheduler returned `0x80070005` for current-user task creation; no probe
or product task was left behind.

## Build & verify

Requires Go 1.26+ (the pinned `go1.26.6` toolchain is downloaded
automatically). `go test -race` additionally needs a C toolchain
(mingw-w64 gcc on Windows) because it uses cgo.

```powershell
go build ./...
go test ./...
go vet ./...
.\scripts\verify.ps1   # full suite; -race and desktop steps report SKIPPED when unavailable
.\scripts\build-desktop.ps1 -Version 0.1.0-test
.\scripts\build-release.ps1 -Version 0.1.0-test -Commit unknown
.\scripts\build-cross.ps1 -Version 0.1.0-test -Commit unknown
```

A code change that ships in `ai-gateway.exe` or `ai-gateway-desktop.exe` is
not finished until `scripts\build-release.ps1` has rebuilt the Windows zip.
Commit first so `-Commit` is a real hash. Same `0.1.0-rc1` zip may be
overwritten unless a new version was requested. Documentation-only edits do
not need a package.

## Usage

```text
ai-gateway serve        run the gateway in the foreground (127.0.0.1:12600 by default)
ai-gateway stop         request a graceful shutdown via the management API
ai-gateway status       show gateway status
ai-gateway doctor       show the live diagnostic report
ai-gateway autostart on enable current-user login start
ai-gateway autostart off disable current-user login start
ai-gateway version      print version information
```

`serve --port N` overrides the configured listen port for that run only; the
config file stays the single source of truth.

## Local AI applications

Open the **AI 中台** / **Local API** view in the desktop application to copy
the current connection values and inspect every enabled model. For a local
third-party application that supports an OpenAI-compatible provider, use:

```text
Base URL:     http://127.0.0.1:12600/v1
API key:     ai-gateway
Model:       gateway-default or <provider-id>/<model-id>
Models URL:  http://127.0.0.1:12600/v1/models
```

The API key is a non-secret placeholder for applications that require a
non-empty value. The loopback data plane does not validate it and never sends
the inbound credential upstream. `gateway-default` follows the generic route;
selecting `<provider-id>/<model-id>` addresses an enabled model directly.

The local data plane accepts OpenAI Chat Completions at
`POST /v1/chat/completions`, OpenAI Responses at `POST /v1/responses`, and
Anthropic Messages at `POST /v1/messages`. `GET /api/v1/local-access` returns
the actual runtime URLs and the same enabled model catalog used by
`GET /v1/models`.

Data root: `%USERPROFILE%\.ai-gateway` on Windows, `~/.ai-gateway` elsewhere
(`AI_GATEWAY_DATA_DIR` overrides it).

```powershell
Invoke-RestMethod http://127.0.0.1:12600/healthz   # -> {"status":"ok"}
```

## License

MIT
