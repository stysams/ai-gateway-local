# Install, operate, and restore

## Windows release package

Build the release archive from a Windows PowerShell session at the repository
root:

```powershell
.\scripts\build-release.ps1 -Version 0.1.0 -Commit <commit-id>
```

The archive under `dist/` contains:

- `ai-gateway.exe`: headless CLI and gateway.
- `ai-gateway-desktop.exe`: desktop, tray, and a `serve` entry point used by
  desktop-managed login start.
- README, license, and this installation guide.

Extract the complete directory to a stable current-user location such as
`%LOCALAPPDATA%\Programs\ai-gateway`. Paths containing spaces are supported.
Moving the executable after enabling login start makes `doctor` report the old
registered path; disable login start before moving, then enable it again.

Launch `ai-gateway-desktop.exe`. It starts `ai-gateway-desktop.exe serve` as a
separate process when the gateway is not already running. Closing the main
window hides it; use the tray to reopen it. “Exit desktop” leaves the gateway
running. “Stop gateway” is the explicit action that calls the shutdown API.

## Headless operation

```powershell
.\ai-gateway.exe serve
.\ai-gateway.exe status
.\ai-gateway.exe doctor
.\ai-gateway.exe stop
```

The gateway defaults to `127.0.0.1`. To let other clients on the local network
reach it, open Settings and select listen address `0.0.0.0`, then restart the
gateway. The default management address is `http://127.0.0.1:12600`;
`GET /readyz` must return HTTP 200 before pointing a client.

## Configure provider models

In the desktop Providers view, add or edit a provider and choose **Fetch
models** after entering its identifier, adapter, Base URL, optional custom
models endpoint, and optional API key. The endpoint defaults to `<base_url>/models`.
The gateway reads the upstream model-list endpoint and fills the model ID,
display name, context window, and maximum output-token fields only when the
upstream response publishes those values. Missing values remain unknown; the
gateway does not infer token limits from a model name.

One provider can retain multiple models. Select one as the default and edit any
discovered values before saving, or add a model manually when the upstream does
not expose a model list. These limits are currently management metadata used by
the catalog and interface; they do not yet truncate or reject data-plane
requests.

The Routes view shows a provider/model tree. Disabling a provider or model
removes it from `/v1/models` and prevents requests from resolving to it.

## Login start

Use the desktop Settings switch, the tray checkbox, or the headless CLI:

```powershell
.\ai-gateway.exe autostart on
.\ai-gateway.exe doctor
.\ai-gateway.exe autostart off
```

Windows registers `\ai-gateway` as a current-user `ONLOGON` Task Scheduler
task at limited privilege and verifies its XML command, `serve` argument, and
logon trigger before updating `config.yaml`. It never creates a SYSTEM task or
a machine service. Managed Windows policy may deny task creation with
`0x80070005`; in that case the config remains disabled. An administrator must
grant the current user Task Scheduler registration permission. Do not replace
this with a SYSTEM task because the gateway must use the current user's DPAPI
secret scope.

After enabling, sign out and sign in to perform the required release check:

```powershell
.\ai-gateway.exe status
.\ai-gateway.exe doctor
```

Confirm the gateway started and `readyz` succeeds for every provider that has
a secret. This logoff/login check cannot be replaced by an automated unit
test.

On macOS, login start uses a user LaunchAgent. On Linux, it uses a user-level
systemd unit. Build the desktop natively on those systems because Wails needs
the target WebView/GTK toolchain:

```powershell
.\scripts\build-desktop.ps1 -Version 0.1.0 -Commit <commit-id>
```

From Windows, `scripts/build-cross.ps1` produces tested headless binaries for
Linux and macOS and explicitly reports the native desktop build as skipped.

## Point a client

The management API supports `codex`, `claude`, and `grok`:

```powershell
$base = "http://127.0.0.1:12600"
Invoke-RestMethod "$base/api/v1/clients/codex"
Invoke-RestMethod -Method Post "$base/api/v1/clients/codex/point"
Invoke-RestMethod "$base/api/v1/clients/codex"
```

Replace `codex` with `claude` or `grok`. Point is idempotent. It creates a
timestamped backup under the gateway data root, writes the client file
atomically, updates the Codex placeholder environment variable when required,
and verifies the final state. A failed write triggers rollback; a rollback
failure is reported as `partial_failure` with the backup directory.

Pointed agent CLIs use the provider-neutral `gateway-default` model. The
gateway's `/v1/models` and `/c/{client}/v1/models` endpoints expose every
enabled provider and model, while the configured route selects the upstream
for `gateway-default` requests. Updating a client route changes the next
request without rewriting the client configuration or replacing its restore
backup. Restart an already-running agent CLI session if it caches
configuration at startup.

| Client | Default file | Override |
| --- | --- | --- |
| Codex | `%USERPROFILE%\.codex\config.toml` | none |
| Claude Code | `%USERPROFILE%\.claude\settings.json` | `CLAUDE_CONFIG_DIR` |
| Grok Build | `%USERPROFILE%\.grok\config.toml` | `GROK_HOME` |

Provider API keys remain in the operating-system secret store and are never
copied into client files.

## Detect drift, restore, and uninstall

```powershell
$base = "http://127.0.0.1:12600"
Invoke-RestMethod "$base/api/v1/status"
Invoke-RestMethod "$base/api/v1/doctor"
Invoke-RestMethod -Method Post "$base/api/v1/clients/codex/restore"
```

`point_state` is `pointed`, `not_pointed`, `drifted`, or
`client_not_installed`. Restore verifies the recorded target and SHA-256
digest, then recovers the exact original bytes or removes a file that did not
exist before point. It also restores the prior Codex environment value.

Before removing the installed directory, restore all three clients, run
`autostart off`, stop the gateway, and exit the desktop tray. Removing an
installation without `autostart off` leaves a stale task that `doctor` will
report.

Before release, complete all twenty Windows acceptance steps in
`docs/v1-scheme.md` §19. Automated transaction, protocol, desktop, and build
tests do not replace the installed-client, real-provider, and logoff/login
checks.
