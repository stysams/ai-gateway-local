import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

const status = { version: "test", pid: 42, listen: "127.0.0.1:12600", logging_enabled: false, logging_body_enabled: false, autostart_enabled: false, clients: { codex: { point_state: "not_pointed" }, claude: { point_state: "client_not_installed" }, grok: { point_state: "drifted" }, generic: { point_state: "unknown" } }, routes: { codex: { provider: "ollama", model: "qwen3" }, claude: { provider: "ollama", model: "qwen3" }, grok: { provider: "ollama", model: "qwen3" }, generic: { provider: "ollama", model: "qwen3" } } };
const localAccess = { base_url: "http://127.0.0.1:12600/v1", api_key: "ai-gateway", auth_required: false, default_model: "gateway-default", default_route: status.routes.generic, endpoints: { models: "http://127.0.0.1:12600/v1/models", chat_completions: "http://127.0.0.1:12600/v1/chat/completions", responses: "http://127.0.0.1:12600/v1/responses", messages: "http://127.0.0.1:12600/v1/messages" }, models: [{ id: "gateway-default", object: "model", created: 0, owned_by: "ai-gateway", display_name: "gateway-default" }, { id: "ollama/qwen3", object: "model", created: 0, owned_by: "ollama", display_name: "ollama/qwen3" }] };
const config = { version: 1, listen: { port: 12600 }, logging: { enabled: false, body: false, redact: true, dir: "logs" }, ui: { language: "en-US", logging_notice_accepted: true }, autostart: { enabled: false }, providers: { ollama: { name: "Ollama", adapter: "openai-chat", base_url: "http://127.0.0.1:11434/v1", default_model: "qwen3", models: [{ id: "qwen3", name: "Qwen 3" }], capabilities: { image_input: false, reasoning: false } }, openrouter: { name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: [{ id: "gpt-5", name: "GPT-5" }, { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4" }], capabilities: { image_input: true, reasoning: true } } }, routes: status.routes };
const providers = [
  { id: "ollama", name: "Ollama", adapter: "openai-chat", base_url: "http://127.0.0.1:11434/v1", default_model: "qwen3", enabled: true, models: [{ id: "qwen3", name: "Qwen 3" }], has_secret: false, capabilities: { image_input: false, reasoning: false } },
  { id: "openrouter", name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", enabled: true, models: [{ id: "gpt-5", name: "GPT-5" }, { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4" }], has_secret: true, capabilities: { image_input: true, reasoning: true } },
];
const pointStatus = (client: string) => ({ client, point_state: client === "codex" ? "not_pointed" : "client_not_installed", target: `C:/${client}/config`, backup_available: false, ...(["codex", "claude"].includes(client) ? { subagent_model: "", title_model: "" } : {}), ...(client === "codex" ? { remote_compaction: false } : {}) });

let liveProviders: typeof providers;

beforeEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  liveProviders = providers.map((provider) => ({ ...provider, models: provider.models.map((model) => ({ ...model })) }));
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith("/api/v1/status")) return Response.json(status);
    if (url.endsWith("/api/v1/local-access")) return Response.json(localAccess);
    if (url.endsWith("/api/v1/config")) return Response.json(config);
    if (url.endsWith("/api/v1/providers")) return Response.json(liveProviders);
    const availability = url.match(/\/api\/v1\/providers\/([^/]+)\/availability$/);
    if (availability && init?.method === "PUT") {
      const body = JSON.parse(String(init.body || "{}")) as { enabled?: boolean; models?: Record<string, boolean> };
      liveProviders = liveProviders.map((provider) => {
        if (provider.id !== availability[1]) return provider;
        return {
          ...provider,
          enabled: typeof body.enabled === "boolean" ? body.enabled : provider.enabled,
          models: provider.models.map((model) => body.models && model.id in body.models ? { ...model, enabled: body.models[model.id] } : model),
        };
      });
      return Response.json(liveProviders.find((provider) => provider.id === availability[1]));
    }
    if (url.endsWith("/api/v1/provider-models/discover") && init?.method === "POST") return Response.json({ object: "list", provider: "new-provider", data: [{ id: "new-provider/model-a", provider_id: "new-provider", raw_id: "model-a", display_name: "Model A", context_window: 131072, max_output_tokens: 16384 }, { id: "new-provider/model-b", provider_id: "new-provider", raw_id: "model-b" }] });
    if (url.endsWith("/api/v1/providers") && init?.method === "POST") return Response.json(providers[0]);
    if (url.includes("/api/v1/logs?") && url.includes("cursor=next-page")) return Response.json({ items: [{ request_id: "req-next", started_at: "2026-08-15T07:59:00Z", client: "claude", provider: "openrouter", model: "gpt-5", status: "success", status_code: 200, duration_ms: 84 }] });
    if (url.includes("/api/v1/logs?")) return Response.json({ items: [{ request_id: "req-copy", started_at: "2026-08-15T08:00:00Z", client: "codex", provider: "ollama", model: "qwen3", status: "success", status_code: 200, duration_ms: 42 }], next_cursor: "next-page" });
    if (url.endsWith("/api/v1/logs/req-copy/export")) return new Response('{"request_id":"req-copy","type":"request","headers":{"Authorization":["[REDACTED]"]},"body":{"model":"qwen3"}}\n', { headers: { "Content-Type": "application/x-ndjson" } });
    if (url.endsWith("/api/v1/logs/req-copy")) return Response.json({ request_id: "req-copy", events: [{ type: "request", headers: { "X-Debug-Trace": ["trace-value"] }, body: { model: "qwen3" } }] });
    if (url.includes("/api/v1/usage")) return Response.json({ total: { requests: 12, success: 10, failed: 1, cancelled: 1, usage_requests: 11, usage: { input_tokens: 1200, output_tokens: 480, reasoning_tokens: 80, cache_creation_input_tokens: 60, cache_read_input_tokens: 540, cache_input_tokens: 1800, total_tokens: 1680 }, incomplete: true }, by_provider: { openrouter: { requests: 12, success: 10, failed: 1, cancelled: 1, usage_requests: 11, usage: { input_tokens: 1200, output_tokens: 480, reasoning_tokens: 80, cache_creation_input_tokens: 60, cache_read_input_tokens: 540, cache_input_tokens: 1800, total_tokens: 1680 }, incomplete: true } }, by_model: { "gpt-5": { requests: 12, success: 10, failed: 1, cancelled: 1, usage_requests: 11, usage: { input_tokens: 1200, output_tokens: 480, reasoning_tokens: 80, cache_creation_input_tokens: 60, cache_read_input_tokens: 540, cache_input_tokens: 1800, total_tokens: 1680 }, incomplete: true } }, by_client: { codex: { requests: 12, success: 10, failed: 1, cancelled: 1, usage_requests: 11, usage: { input_tokens: 1200, output_tokens: 480, reasoning_tokens: 80, cache_creation_input_tokens: 60, cache_read_input_tokens: 540, cache_input_tokens: 1800, total_tokens: 1680 }, incomplete: true } }, by_date: { "2026-08-23": { requests: 12, success: 10, failed: 1, cancelled: 1, usage_requests: 11, usage: { input_tokens: 1200, output_tokens: 480, reasoning_tokens: 80, cache_creation_input_tokens: 60, cache_read_input_tokens: 540, cache_input_tokens: 1800, total_tokens: 1680 }, incomplete: true } } });
    for (const client of ["codex", "claude", "grok"]) if (url.endsWith(`/api/v1/clients/${client}`)) return Response.json(pointStatus(client));
    if (url.includes("/api/v1/routes/codex") && init?.method === "PUT") return Response.json({ client: "codex", provider: "openrouter", model: "anthropic/claude-sonnet-4" });
    if (url.endsWith("/api/v1/logging") && init?.method === "PUT") return Response.json({ enabled: true, body: true });
    if (url.endsWith("/api/v1/autostart") && init?.method === "PUT") return Response.json({ enabled: true, valid: true });
    if (url.endsWith("/api/v1/clients/codex/point")) return Response.json({ ...pointStatus("codex"), point_state: "pointed", changed: true });
    if (url.endsWith("/api/v1/clients/codex/remote-compaction") && init?.method === "PUT") return Response.json({ ...pointStatus("codex"), remote_compaction: true });
    if (url.endsWith("/api/v1/clients/claude/helper-models") && init?.method === "PUT") return Response.json({ ...pointStatus("claude"), ...JSON.parse(String(init.body || "{}")) });
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
    const clientRoutes = screen.getByText("Client routes");
    const routeCatalog = screen.getByText("Provider and model catalog");
    expect(clientRoutes.compareDocumentPosition(routeCatalog) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const modelSelect = screen.getByRole("combobox", { name: "codex Default selected model" });
    expect(modelSelect).toBeRequired();
    expect(modelSelect).toHaveValue("ollama/qwen3");
    expect(within(modelSelect).getByRole("option", { name: "Select a default model" })).toBeDisabled();
    expect(within(modelSelect).getByRole("option", { name: "ollama/qwen3" })).toBeVisible();
    expect(within(modelSelect).getByRole("option", { name: "openrouter/gpt-5" })).toBeVisible();
    expect(within(modelSelect).getByRole("option", { name: "openrouter/anthropic/claude-sonnet-4" })).toBeVisible();
    expect(screen.getAllByRole("button", { name: "Apply" })[0]).toBeDisabled();
    await user.selectOptions(modelSelect, "openrouter/anthropic/claude-sonnet-4");
    await user.click(screen.getAllByRole("button", { name: "Apply" })[0]);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/routes/codex"), expect.objectContaining({ method: "PUT", body: "{\"provider\":\"openrouter\",\"model\":\"anthropic/claude-sonnet-4\"}" })));
    const toast = await screen.findByRole("status");
    expect(toast).toHaveTextContent("Success");
    await user.click(within(toast).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("clears required client routes when the selected provider is disabled", async () => {
    const user = userEvent.setup();
    render(<App />);
    await ready();
    await user.click(screen.getByRole("button", { name: "Routes" }));
    const modelSelect = screen.getByRole("combobox", { name: "codex Default selected model" });
    expect(modelSelect).toHaveValue("ollama/qwen3");
    expect(screen.getAllByRole("button", { name: "Apply" })[0]).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "Provider Ollama" }));
    await waitFor(() => expect(modelSelect).toHaveValue(""));
    expect(within(modelSelect).queryByRole("option", { name: "ollama/qwen3" })).not.toBeInTheDocument();
    expect(screen.getAllByText("The current default route is no longer available. Select another model and apply it.").length).toBe(4);
    expect(screen.getAllByRole("button", { name: "Apply" })[0]).toBeDisabled();
    await user.selectOptions(modelSelect, "openrouter/gpt-5");
    expect(modelSelect).toHaveValue("openrouter/gpt-5");
    expect(screen.getAllByRole("button", { name: "Apply" })[0]).toBeEnabled();
  });

  it("selects separate Claude subagent and title models", async () => {
    const user = userEvent.setup();
    render(<App />);
    await ready();
    await user.click(screen.getByRole("button", { name: "Clients" }));
    const subagent = screen.getByRole("combobox", { name: "claude Subagent model" });
    const title = screen.getByRole("combobox", { name: "claude Title generation model" });
    expect(subagent).toHaveValue("");
    expect(within(subagent).getByRole("option", { name: "Follow current route" })).toBeVisible();
    await user.selectOptions(subagent, "openrouter/anthropic/claude-sonnet-4");
    await user.selectOptions(title, "ollama/qwen3");
    const applyButtons = screen.getAllByRole("button", { name: "Apply" });
    await user.click(applyButtons.at(-1)!);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/clients/claude/helper-models"), expect.objectContaining({ method: "PUT", body: "{\"subagent_model\":\"openrouter/anthropic/claude-sonnet-4\",\"title_model\":\"ollama/qwen3\"}" })));
  });

  it("shows local access parameters and copies a model identifier", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Local API" }));
    expect(screen.getByText("http://127.0.0.1:12600/v1")).toBeVisible();
    expect(screen.getByText("http://127.0.0.1:12600/v1/models")).toBeVisible();
    expect(screen.getByText("ollama/qwen3")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Copy ollama/qwen3" }));
    expect(writeText).toHaveBeenCalledWith("ollama/qwen3");
  });

  it("keeps focus while typing a model id", async () => {
    const user = userEvent.setup();
    render(<App />);
    await ready();
    await user.click(screen.getByRole("button", { name: "Providers" }));
    await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.click(screen.getByRole("button", { name: "Add model manually" }));
    const input = screen.getAllByLabelText("Model ID").at(-1);
    expect(input).toBeDefined();
    await user.click(input!);
    await user.keyboard("claude-opus-5");
    expect(input).toHaveValue("claude-opus-5");
    expect(input).toHaveFocus();
  });

  it("loads upstream model metadata and includes edits in the provider payload", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Providers" })); await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.type(screen.getByLabelText("Identifier"), "new-provider"); await user.type(screen.getByLabelText("Name"), "New Provider"); await user.type(screen.getByLabelText("Base URL"), "https://example.com/v1");
    expect(screen.getByText(/append the \[1m\] suffix/)).toBeVisible();
    expect(screen.queryByLabelText("Default adapter")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Context window")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Apply preset Codex" }));
    expect(screen.getByDisplayValue("codex_cli_rs/0.147.0")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Fetch models" }));
    expect(await screen.findByDisplayValue("model-a")).toBeVisible();
    expect(screen.queryByDisplayValue("131072")).not.toBeInTheDocument();
    await user.click(screen.getAllByRole("radio", { name: "Default model" })[0]); await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ method: "POST", body: expect.stringContaining('"id":"model-a"') })));
    expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ body: expect.stringContaining('"Originator":"codex_cli_rs"') }));
    expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ body: expect.stringContaining('"adapter":"openai-chat"') }));
    expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ body: expect.not.stringContaining("context_window") }));
  });

  it("saves a different outbound protocol on one model", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Providers" })); await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.type(screen.getByLabelText("Identifier"), "any"); await user.type(screen.getByLabelText("Name"), "Any"); await user.type(screen.getByLabelText("Base URL"), "https://example.com/v1");
    await user.click(screen.getByRole("button", { name: "Add model manually" }));
    await user.click(screen.getAllByLabelText("Model ID").at(-1)!);
    await user.keyboard("claude-opus");
    await user.click(screen.getAllByRole("radio", { name: "Default model" })[0]);
    await user.selectOptions(screen.getByRole("combobox", { name: "Protocol claude-opus" }), "anthropic");
    expect(screen.getByLabelText("Request endpoint claude-opus")).toHaveValue("/v1/messages");
    expect(screen.getByLabelText("Request endpoint claude-opus")).toHaveAttribute("readOnly");
    expect(screen.getByText("https://example.com/v1/messages")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ method: "POST", body: expect.stringContaining('"adapter":"anthropic"') })));
  });

  it("saves a custom request endpoint without rewriting it to /v1", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Providers" })); await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.type(screen.getByLabelText("Identifier"), "tudou"); await user.type(screen.getByLabelText("Name"), "Tudou"); await user.type(screen.getByLabelText("Base URL"), "https://api.2dou.net");
    await user.click(screen.getByRole("button", { name: "Add model manually" }));
    await user.click(screen.getAllByLabelText("Model ID").at(-1)!);
    await user.keyboard("gpt-5.6-sol");
    await user.click(screen.getAllByRole("radio", { name: "Default model" })[0]);
    await user.selectOptions(screen.getByRole("combobox", { name: "Protocol gpt-5.6-sol" }), "custom");
    const endpoint = screen.getByLabelText("Request endpoint gpt-5.6-sol");
    await user.clear(endpoint);
    await user.type(endpoint, "/responses");
    expect(screen.getByText("https://api.2dou.net/responses")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ method: "POST", body: expect.stringContaining('"endpoint":"/responses"') })));
    expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ body: expect.stringContaining('"adapter":"custom"') }));
    expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ body: expect.stringContaining('"adapter":"openai-responses"') }));
  });

  it("saves disguise_client for third-party requests", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Providers" })); await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.type(screen.getByLabelText("Identifier"), "any"); await user.type(screen.getByLabelText("Name"), "Any"); await user.type(screen.getByLabelText("Base URL"), "https://example.com/v1");
    await user.click(screen.getByRole("button", { name: "Add model manually" }));
    await user.click(screen.getAllByLabelText("Model ID").at(-1)!);
    await user.keyboard("claude-fable-5");
    await user.click(screen.getAllByRole("radio", { name: "Default model" })[0]);
    expect(screen.getByText(/Claude Code disguise also adds thinking and system cache_control/)).toBeVisible();
    await user.selectOptions(screen.getByRole("combobox", { name: "Disguise client" }), "claude");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ method: "POST", body: expect.stringContaining('"disguise_client":"claude"') })));
  });

  it("saves Pi disguise and applies the Pi request-header preset", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Providers" })); await user.click(screen.getByRole("button", { name: "Add provider" }));
    await user.type(screen.getByLabelText("Identifier"), "pi-upstream"); await user.type(screen.getByLabelText("Name"), "Pi Upstream"); await user.type(screen.getByLabelText("Base URL"), "https://example.com/v1");
    await user.click(screen.getByRole("button", { name: "Add model manually" })); await user.type(screen.getAllByLabelText("Model ID").at(-1)!, "gpt-5"); await user.click(screen.getAllByRole("radio", { name: "Default model" })[0]);
    await user.selectOptions(screen.getByRole("combobox", { name: "Disguise client" }), "pi");
    await user.click(screen.getByRole("button", { name: "Apply preset Pi" }));
    expect(screen.getByDisplayValue("Pi Agent/1.0")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ method: "POST", body: expect.stringContaining('"disguise_client":"pi"') })));
    expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/providers$/), expect.objectContaining({ body: expect.stringContaining('"User-Agent":"Pi Agent/1.0"') }));
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

  it("filters token usage by time range, provider, and model", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Usage" }));
    expect(screen.getByText("Token usage trend")).toBeVisible();
    await user.selectOptions(screen.getByRole("combobox", { name: "Time range" }), "7d");
    await user.selectOptions(screen.getByRole("combobox", { name: "Provider" }), "openrouter");
    await user.selectOptions(screen.getByRole("combobox", { name: "Model" }), "gpt-5");
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringMatching(/\/api\/v1\/usage\?.*provider=openrouter.*model=gpt-5/), expect.anything()));
    expect(screen.getAllByText("Cache Hit Rate").length).toBeGreaterThan(0);
    const chart = screen.getByRole("img", { name: "Token and cache trend" });
    expect(within(chart).getByText("Input")).toBeVisible();
    expect(within(chart).getByText("Output")).toBeVisible();
    expect(within(chart).getByText("Cache Creation")).toBeVisible();
    expect(within(chart).getByText("Cache Read")).toBeVisible();
    expect(within(chart).getByText("Cache Hit Rate")).toBeVisible();
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeVisible();
  });

  it("copies the complete request log from the detail drawer", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    await user.click(screen.getByRole("button", { name: "Details" }));
    await user.click(await screen.findByRole("button", { name: "Copy redacted log" }));
    expect(writeText).toHaveBeenCalledOnce();
    expect(writeText.mock.calls[0][0]).toContain('"Authorization":["[REDACTED]"]');
    expect(writeText.mock.calls[0][0]).not.toContain("trace-value");
    expect(screen.getByRole("button", { name: "Copied" })).toBeVisible();
  });

  it("copies a request log directly from its table row", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    await user.click(screen.getByRole("button", { name: "Copy redacted log req-copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    expect(writeText.mock.calls[0][0]).toContain('"Authorization":["[REDACTED]"]');
    expect(writeText.mock.calls[0][0]).not.toContain("trace-value");
  });

  it("loads the next request-log page with the server cursor", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("cursor=next-page"), expect.anything()));
    expect(await screen.findByText("openrouter")).toBeVisible();
    expect(screen.getByText("gpt-5")).toBeVisible();
  });

  it("enables current-user login start from settings", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("checkbox", { name: "Start at login" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/autostart"), expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" })));
  });
});
