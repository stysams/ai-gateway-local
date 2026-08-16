import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

const status = { version: "test", pid: 42, listen: "127.0.0.1:12600", logging_enabled: false, logging_body_enabled: false, autostart_enabled: false, clients: { codex: { point_state: "not_pointed" }, claude: { point_state: "client_not_installed" }, grok: { point_state: "drifted" }, generic: { point_state: "unknown" } }, routes: { codex: { provider: "ollama", model: "qwen3" }, claude: { provider: "ollama", model: "qwen3" }, grok: { provider: "ollama", model: "qwen3" }, generic: { provider: "ollama", model: "qwen3" } } };
const config = { version: 1, listen: { port: 12600 }, logging: { enabled: false, body: false, dir: "logs" }, ui: { language: "en-US", logging_notice_accepted: true }, autostart: { enabled: false }, providers: { ollama: { name: "Ollama", adapter: "openai-chat", base_url: "http://127.0.0.1:11434/v1", default_model: "qwen3", models: [{ id: "qwen3", name: "Qwen 3", context_window: 32768, max_output_tokens: 8192 }], capabilities: { image_input: false, reasoning: false } }, openrouter: { name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: [{ id: "gpt-5", name: "GPT-5", context_window: 400000, max_output_tokens: 128000 }, { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4", context_window: 200000, max_output_tokens: 64000 }], capabilities: { image_input: true, reasoning: true } } }, routes: status.routes };
const providers = [
  { id: "ollama", name: "Ollama", adapter: "openai-chat", base_url: "http://127.0.0.1:11434/v1", default_model: "qwen3", models: [{ id: "qwen3", name: "Qwen 3", context_window: 32768, max_output_tokens: 8192 }], has_secret: false, capabilities: { image_input: false, reasoning: false } },
  { id: "openrouter", name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: [{ id: "gpt-5", name: "GPT-5", context_window: 400000, max_output_tokens: 128000 }, { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4", context_window: 200000, max_output_tokens: 64000 }], has_secret: true, capabilities: { image_input: true, reasoning: true } },
];
const pointStatus = (client: string) => ({ client, point_state: client === "codex" ? "not_pointed" : "client_not_installed", target: `C:/${client}/config`, backup_available: false, ...(client === "codex" ? { remote_compaction: false } : {}) });

beforeEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith("/api/v1/status")) return Response.json(status);
    if (url.endsWith("/api/v1/config")) return Response.json(config);
    if (url.endsWith("/api/v1/providers")) return Response.json(providers);
    if (url.endsWith("/api/v1/provider-models/discover") && init?.method === "POST") return Response.json({ object: "list", provider: "new-provider", data: [{ id: "new-provider/model-a", provider_id: "new-provider", raw_id: "model-a", display_name: "Model A", context_window: 131072, max_output_tokens: 16384 }, { id: "new-provider/model-b", provider_id: "new-provider", raw_id: "model-b" }] });
    if (url.endsWith("/api/v1/providers") && init?.method === "POST") return Response.json(providers[0]);
    if (url.includes("/api/v1/logs?")) return Response.json({ items: [{ request_id: "req-copy", started_at: "2026-08-15T08:00:00Z", client: "codex", provider: "ollama", model: "qwen3", status: "success", status_code: 200, duration_ms: 42 }] });
    if (url.endsWith("/api/v1/logs/req-copy")) return Response.json({ request_id: "req-copy", events: [{ type: "request", headers: { "X-Debug-Trace": ["trace-value"] }, body: { model: "qwen3" } }] });
    if (url.endsWith("/api/v1/usage")) return Response.json({ total: { requests: 0, success: 0, failed: 0, cancelled: 0, usage: null, incomplete: true }, by_provider: {}, by_model: {}, by_client: {}, by_date: {} });
    for (const client of ["codex", "claude", "grok"]) if (url.endsWith(`/api/v1/clients/${client}`)) return Response.json(pointStatus(client));
    if (url.includes("/api/v1/routes/codex") && init?.method === "PUT") return Response.json({ client: "codex", provider: "openrouter", model: "anthropic/claude-sonnet-4" });
    if (url.endsWith("/api/v1/logging") && init?.method === "PUT") return Response.json({ enabled: true, body: true });
    if (url.endsWith("/api/v1/autostart") && init?.method === "PUT") return Response.json({ enabled: true, valid: true });
    if (url.endsWith("/api/v1/clients/codex/point")) return Response.json({ ...pointStatus("codex"), point_state: "pointed", changed: true });
    if (url.endsWith("/api/v1/clients/codex/remote-compaction") && init?.method === "PUT") return Response.json({ ...pointStatus("codex"), remote_compaction: true });
    return Response.json({ error: { code: "not_mocked", message: url } }, { status: 500 });
  });
});

async function ready() { await screen.findByRole("heading", { name: "Overview" }); }

describe("desktop workflow", () => {
  it("navigates by keyboard and switches a route", async () => {
    const user = userEvent.setup(); render(<App />); await ready();
    const routes = screen.getByRole("button", { name: "Routes" }); routes.focus(); await user.keyboard("{Enter}");
    expect(await screen.findByText(/Changes apply to the next request/)).toBeVisible();
    expect(screen.getByText(/not the only model it can use/)).toBeVisible();
    const modelSelect = screen.getByRole("combobox", { name: "codex Default selected model" });
    expect(within(modelSelect).getByRole("option", { name: "ollama/qwen3" })).toBeVisible();
    expect(within(modelSelect).getByRole("option", { name: "openrouter/gpt-5" })).toBeVisible();
    expect(within(modelSelect).getByRole("option", { name: "openrouter/anthropic/claude-sonnet-4" })).toBeVisible();
    await user.selectOptions(modelSelect, "openrouter/anthropic/claude-sonnet-4");
    await user.click(screen.getAllByRole("button", { name: "Apply" })[0]);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/routes/codex"), expect.objectContaining({ method: "PUT", body: "{\"provider\":\"openrouter\",\"model\":\"anthropic/claude-sonnet-4\"}" })));
    const toast = await screen.findByRole("status");
    expect(toast).toHaveTextContent("Success");
    await user.click(within(toast).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("loads upstream model metadata and includes edits in the provider payload", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Providers" })); await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.type(screen.getByLabelText("Identifier"), "new-provider"); await user.type(screen.getByLabelText("Name"), "New Provider"); await user.type(screen.getByLabelText("Base URL"), "https://example.com/v1");
    await user.click(screen.getByRole("button", { name: "Fetch models" }));
    expect(await screen.findByDisplayValue("model-a")).toBeVisible(); expect(screen.getByDisplayValue("131072")).toBeVisible(); expect(screen.getByDisplayValue("16384")).toBeVisible();
    const contextInput = screen.getByDisplayValue("131072"); await user.clear(contextInput); await user.type(contextInput, "200000"); await user.click(screen.getAllByRole("radio", { name: "Default model" })[0]); await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ method: "POST", body: expect.stringContaining('"context_window":200000') })));
  });

  it("requires confirmation before pointing", async () => {
    const user = userEvent.setup(); const confirm = vi.spyOn(window, "confirm").mockReturnValue(false); render(<App />); await ready();
    await user.click(screen.getByRole("button", { name: "Clients" })); await user.click(screen.getAllByRole("button", { name: "Point to gateway" })[0]);
    expect(confirm).toHaveBeenCalled(); expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining("/point"), expect.anything());
  });

  it("toggles Codex remote compaction from the clients page", async () => {
    const user = userEvent.setup(); render(<App />); await ready();
    await user.click(screen.getByRole("button", { name: "Clients" }));
    const toggle = screen.getByRole("checkbox", { name: "Remote compaction" });
    expect(toggle).not.toBeChecked();
    expect(screen.queryByRole("checkbox", { name: "Remote compaction" })).toBe(toggle);
    await user.click(toggle);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/clients/codex/remote-compaction"), expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" })));
  });

  it("shows logging disabled state and can enable it", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    expect(screen.getAllByText("Disabled").length).toBeGreaterThan(0); await user.click(screen.getByRole("checkbox", { name: "Logging" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/logging"), expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" })));
    expect(screen.getByRole("checkbox", { name: "Body" })).toBeDisabled();
  });

  it("copies the complete request log from the detail drawer", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    await user.click(screen.getByRole("button", { name: "Details" }));
    await user.click(await screen.findByRole("button", { name: "Copy request log" }));
    expect(writeText).toHaveBeenCalledOnce();
    expect(JSON.parse(writeText.mock.calls[0][0])).toEqual({ request_id: "req-copy", events: [{ type: "request", headers: { "X-Debug-Trace": ["trace-value"] }, body: { model: "qwen3" } }] });
    expect(screen.getByRole("button", { name: "Copied" })).toBeVisible();
  });

  it("copies a request log directly from its table row", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    await user.click(screen.getByRole("button", { name: "Copy request log req-copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    expect(JSON.parse(writeText.mock.calls[0][0])).toEqual({ request_id: "req-copy", events: [{ type: "request", headers: { "X-Debug-Trace": ["trace-value"] }, body: { model: "qwen3" } }] });
  });

  it("enables current-user login start from settings", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("checkbox", { name: "Start at login" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/autostart"), expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" })));
  });
});
