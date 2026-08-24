# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ai-gateway` is a single-user, loopback-only AI proxy gateway written in Go. Local
agent clients (Codex, Claude Code, Grok Build, and any OpenAI- or
Anthropic-compatible app) point their base URL at `127.0.0.1:12600`; the gateway
owns upstream API keys, per-client routing, protocol translation between the
three wire protocols, per-request body logging, and usage aggregation. An
optional Wails v3 desktop shell controls the gateway over the loopback
management API only — it never serves `/v1/*`.

One Go module (`ai-gateway`), two publishable binaries: `ai-gateway`
(`cmd/gateway`) and `ai-gateway-desktop` (`cmd/desktop`).

## docs/v1-scheme.md is the contract

`docs/progress.md` (Simplified Chinese) is the current `0.1.0-rc1` version
record and historical acceptance log. Read it before starting work.
`docs/code-map.md` is the
file-by-file map: look up the change you want, then open those paths.
`docs/v1-scheme.md` is the
frozen engineering contract, and it wins over the implementation when they
disagree. Almost every source file cites it (`docs/v1-scheme.md §7.4`
style). Before changing behaviour in `config`, `route`, `ir`, `inbound`,
`outbound`, `secret`, `point`, or the HTTP surface, read the cited section.

Two rules from §17 that matter most in practice:

- If an external contract (a client's config fields, a provider's protocol) no
  longer matches the spec, **stop**, record reproducible evidence, update
  §20 and the affected tests, and only then change code. Never "fix"
  compatibility from memory.
- Never weaken a protocol assertion or leave a stub/fixed-value implementation
  to make a test pass.

The code has moved slightly ahead of the spec in a few places (provider/model
`enabled` availability flags, `models_url`, `listen.host: 0.0.0.0`,
`/api/v1/providers/{id}/availability`, `POST /api/v1/provider-models/discover`,
`capabilities.context_management`). Treat the code as authoritative for those
and the spec as authoritative everywhere else.

## Commands

Go (root module; requires Go 1.26+, toolchain pinned to `go1.26.6` and fetched
via `GOTOOLCHAIN=auto`):

```powershell
go build ./...
go vet ./...
go test ./...
go test ./internal/route -run TestResolve            # single package / test
go test ./internal/server -run TestCrossProtocol -v
go test -race ./...                                  # needs cgo + a C compiler (mingw-w64 gcc)
go run ./cmd/gateway serve
go run ./cmd/gateway version
```

Desktop frontend (`desktop/` has its own stub `go.mod` so the root module's
`./...` does not walk it):

```powershell
npm --prefix desktop ci
npm --prefix desktop run test                 # vitest
npm --prefix desktop run test -- src/api.test.ts
npm --prefix desktop run lint
npm --prefix desktop run build                # vite -> cmd/desktop/assets (committed, embedded)
npm --prefix desktop run test:e2e             # playwright, spins up vite on 127.0.0.1:9245
npm --prefix desktop run test:e2e -- --project=desktop-light
```

Full suite and packaging (`scripts/verify.ps1` is the §16.3 unified command;
steps that cannot run print an explicit `SKIPPED` and are never reported as
passed):

```powershell
.\scripts\verify.ps1
.\scripts\build-desktop.ps1 -Version 0.1.0-test
.\scripts\build-release.ps1 -Version 0.1.0-test -Commit unknown
.\scripts\build-cross.ps1  -Version 0.1.0-test -Commit unknown
```

Note: `npm run build` overwrites `cmd/desktop/assets/`, which is committed and
`go:embed`-ed by `cmd/desktop/main.go`. Frontend changes are only visible in a
built desktop binary after that step.

## Release packaging after code changes

A task that changes anything shipped in `ai-gateway.exe` or
`ai-gateway-desktop.exe` (Go sources, `desktop/` frontend, or embedded
`cmd/desktop/assets/`) is not finished until a new Windows release zip exists.
Do not wait for the user to ask to repackage. Documentation-only edits that
cannot change those binaries do not require a package.

Required sequence (`docs/progress.md` packaging rules):

1. Tests for the changed packages pass.
2. Commit so the working tree is clean. The binary `-Commit` must be that
   real commit, never `unknown` and never a dirty HEAD.
3. Confirm no `ai-gateway` / `ai-gateway-desktop` process is locking `dist/`.
4. Run `.\scripts\build-release.ps1 -Version 0.1.0-rc1 -Commit <full hash>`.
   Overwrite the same `0.1.0-rc1` zip unless the user asked for a new version.
5. Check the staged `ai-gateway.exe version` (version, commit, Go, platform)
   and that the zip contains both binaries, `LICENSE`, `README.md`, and
   `docs/install.md`.

Tell the user they must replace and restart any already-running gateway
before the new package takes effect.

Runtime data root: `%USERPROFILE%\.ai-gateway` (Windows) / `~/.ai-gateway`,
overridable with `AI_GATEWAY_DATA_DIR` — tests rely on that override.

## Architecture

### Dependency directions (§3.1, enforced by convention)

```
cmd -> internal/app
internal/app -> config, secret, route, server, logstore, point, autostart, process, version
server -> inbound, outbound, route, logstore, point, secret, config, autostart
inbound -> ir        outbound -> ir
desktop -> HTTP /api/v1 only (never imports gateway internals, never edits config.yaml)
```

Hard rules: `ir` imports no concrete protocol package; `inbound` never touches
the key store; `outbound` never decides the route; adapters never call each
other — all cross-protocol conversion goes through `ir`. Platform-specific code
lives in build-tagged files (`*_windows.go`, `*_unix.go`, `*_darwin.go`,
`*_other.go`), never as runtime `runtime.GOOS` branching in shared files.

### Request path

`internal/server/handlers.go` registers everything on one `http.ServeMux`:
health checks, `/api/v1/*` management, and the `/v1/*` + `/c/{client}/v1/*`
data plane. `internal/server/dataplane.go:serveDataPlane` is the single
pipeline:

1. Content-Type must be JSON (else 415); body capped at 128 MiB (413).
2. `parseForRouting` extracts only `model` and `stream`.
3. `startTrace` opens the per-request JSONL session and sets `X-Request-Id`.
4. `route.Resolve(client, model, cfg)` — §7.4: route default → empty or
   `gateway-default` → `<provider-id>/<rest>` prefix override (prefix must be a
   configured provider id) → for `generic`, a uniquely registered model selects
   its owning provider → a model listed on the current route provider is kept
   on that provider → otherwise reject with the unmatched-model message
   (callers must send `<provider-id>/<model-id>`). Ambiguous registered
   ownership without the route provider is rejected. A model containing `/`
   must never be rejected merely as an "unknown provider". The outbound
   protocol is `Provider.ModelAdapter(model)`: a non-empty `models[].adapter`
   wins, otherwise the provider default `adapter`.
5. Capability gates before any upstream call: image input unsupported → 422
   with zero upstream contact; `context_management` stripped for providers that
   do not declare it; reasoning dropped with a `reasoning_dropped` warning event.
6. **Same protocol** (`inProto == outProto`): rewrite only model/stream/auth,
   forward upstream status, whitelisted headers, and bytes verbatim.
   **Cross protocol**: parse to `ir.Request`, generate the outbound request,
   pipe the upstream response through `ir.Sequencer` into the inbound
   protocol's encoding.

`inbound/{chat,responses,messages}` keep every top-level field as
`map[string]json.RawMessage`, so same-protocol forwarding is lossless at the
field level (key order and whitespace are not preserved — that is documented and
intentional). `outbound/{openaichat,openairesponses,anthropic}` build
`<base>/v1/chat/completions`, `<base>/v1/responses`, `<base>/v1/messages`
(no duplicated `/v1` if the base already ends with it). A model may set
`adapter: custom` plus `endpoint` to keep a non-`/v1` path. They share the
transport/credential plumbing in `outbound/internal/upstream`.

`ir.Sequencer` is the state machine for the unified event stream: stable tool
call ids, argument deltas concatenated in arrival order, at most one
`response.completed`, and no success event after an error. A broken upstream
stream must end in a protocol error event — never a fabricated completion.

### Streaming and error mapping

No overall `WriteTimeout` on the HTTP server and no total deadline on streams
(long reasoning must not be truncated); only the five-minute
`ResponseHeaderTimeout` bounds the upstream before response headers (→ 504).
Every SSE event is flushed immediately; buffering an entire
upstream response and replaying it as a stream is forbidden. Redirects are never
followed (status + `Location` are passed through) so provider credentials cannot
leak to a second target. Key-store failures map to **500**, never 502;
unreachable upstream → 502; capability limits → 422. Data-plane errors use the
inbound protocol's native error shape; management errors use the
`{"error":{code,message,details,request_id}}` envelope in `apierror.go`.

### Config, secrets, logs, point

- `internal/config` is the single source of truth (`config.yaml`). `Manager`
  serializes reads/writes, keeps the in-process snapshot the data plane reads,
  validates fully before writing, and writes atomically through a temp file in
  the *same* directory. Unknown top-level YAML fields survive read-modify-write
  via the `Extra` inline map. Optional fields use pointers (`*int`, `*bool`) so
  "absent" is distinguishable from an explicit invalid value.
- `internal/secret`: `Put/Get/Delete/Available` with distinct
  `ErrNotFound` / `ErrUnavailable`. Windows = per-user DPAPI ciphertext under
  `<data root>/secrets/`; macOS/Linux have explicit implementations that fail
  loudly. There is **no plaintext fallback anywhere** — not YAML, not env files,
  not desktop storage. Secret bytes from `Get` are zeroed right after use. Keys
  enter only through the provider write path (§6.3 transaction: validate → write
  key → atomic config write → restore old key on failure).
- `internal/logstore` + `internal/server/trace.go`: one append-only JSONL file
  per request under `logs/<local-date>/<request-id>.jsonl`, with `request`,
  `route`, `upstream_request`, `upstream_event`, `client_event`, `warning`,
  `result` events. Authorization / `x-api-key` / Cookie / tokens are never
  recorded — omitted fields are reported as counts
  (`omitted_sensitive_header_count`). Usage is only ever what the upstream
  actually returned; missing usage is flagged incomplete and never estimated.
- `internal/point` (+ `point/{codex,claude,grok}`): transactional client
  pointing. Read original bytes → SHA-256 backup manifest under
  `backups/<client>/<utc>/` → atomic rewrite → verify → roll back everything on
  any failure. Pointed clients use the provider-neutral `gateway-default` model;
  changing a route must **not** rewrite a pointed client's config file and must
  not replace the original restore point. Adapters rewrite **only** the model
  and routing keys, byte-splicing through the leaf packages `point/tomledit`
  (TOML) and `point/jsonedit` (JSON): comments, key order, quoting style and
  every unrelated `[mcp_servers.*]` / `[plugins.*]` / `permissions` / `hooks`
  block keep their original bytes, and a repeated point is byte-identical. Only
  a target inside an inline table, array or array of tables falls back to
  `transformWhole` re-encoding. Never replace a `Transform` with
  unmarshal → mutate map → marshal.

## Testing conventions

- Desensitized protocol fixtures live in `testdata/protocols/{chat,responses,messages}/`
  and client-config fixtures in `testdata/point/`; tests reach them via
  `filepath.Join("..", "..", "testdata", ...)`. Fixtures must contain no real
  keys, cookies, account ids, or personal paths.
- Integration tests use `httptest.Server` fake upstreams
  (`internal/server/dataplane_test.go`, `crossprotocol_test.go`) and assert
  status, key headers, event order, tool call ids, model rewriting, and downgrade
  warnings — not just "it returned 200".
- Golden/fixture updates only happen as part of an explicit behaviour-change
  task; never bulk-overwrite them to silence a failing test.
- `docs/v1-scheme.md` §19 defines the real-client Windows acceptance run that
  mocks cannot substitute. The 2026-08-22 product decision accepted phase 1
  with residual issues; the original evidence remains in `docs/progress.md`
  §12-13 as a historical record and does not create another release plan.

## Repo notes

- `memory/` holds dated task journals and is gitignored; `bin/`, `dist/`, and
  `.tmp-*/` are local build and scratch output.
- Wails is pinned to exactly `v3.0.0-beta.8`. Only a dedicated upgrade task may
  change that version — never bump it opportunistically while doing other work.
