import { expect, test } from "@playwright/test";

const status = { version: "0.1.0", pid: 4242, listen: "127.0.0.1:12600", logging_enabled: true, logging_body_enabled: true, autostart_enabled: false, clients: { codex: { point_state: "pointed" }, claude: { point_state: "not_pointed" }, grok: { point_state: "drifted" }, generic: { point_state: "unknown" } }, routes: { codex: { provider: "openrouter", model: "gpt-5" }, claude: { provider: "openrouter", model: "anthropic/claude-sonnet-4" }, grok: { provider: "deepseek", model: "deepseek-chat" }, generic: { provider: "openrouter", model: "gpt-5" } } };
const localAccess = { base_url: "http://127.0.0.1:12600/v1", api_key: "ai-gateway", auth_required: false, default_model: "gateway-default", default_route: status.routes.generic, endpoints: { models: "http://127.0.0.1:12600/v1/models", chat_completions: "http://127.0.0.1:12600/v1/chat/completions", responses: "http://127.0.0.1:12600/v1/responses", messages: "http://127.0.0.1:12600/v1/messages" }, models: [{ id: "gateway-default", object: "model", created: 0, owned_by: "ai-gateway", display_name: "gateway-default" }, { id: "openrouter/gpt-5", object: "model", created: 0, owned_by: "openrouter", display_name: "openrouter/gpt-5" }, { id: "openrouter/anthropic/claude-sonnet-4", object: "model", created: 0, owned_by: "openrouter", display_name: "openrouter/anthropic/claude-sonnet-4" }] };
const providerModels = [
  { id: "gpt-5", name: "GPT-5", context_window: 400000, max_output_tokens: 128000 },
  { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4", context_window: 200000, max_output_tokens: 64000 },
];
const config = { version: 1, listen: { port: 12600 }, logging: { enabled: true, body: true, dir: "logs" }, ui: { language: "en-US", logging_notice_accepted: true }, autostart: { enabled: false }, providers: { openrouter: { name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: providerModels, capabilities: { image_input: true, reasoning: true } } }, routes: status.routes };

test.beforeEach(async ({ page }) => {
  await page.route("http://127.0.0.1:12600/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/status")) return route.fulfill({ json: status });
    if (path.endsWith("/local-access")) return route.fulfill({ json: localAccess });
    if (path.endsWith("/config")) return route.fulfill({ json: config });
    if (path.endsWith("/providers")) return route.fulfill({ json: [{ id: "openrouter", name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: providerModels, has_secret: true, capabilities: { image_input: true, reasoning: true } }, { id: "deepseek", name: "DeepSeek", adapter: "openai-chat", base_url: "https://api.deepseek.com", default_model: "deepseek-chat", models: [{ id: "deepseek-chat", name: "DeepSeek Chat", context_window: 128000, max_output_tokens: 8192 }], has_secret: true, capabilities: { image_input: false, reasoning: true } }] });
    if (path.endsWith("/providers/openrouter/probe")) return route.fulfill({ json: { ok: true, status: 200, latency_ms: 318, models: 2, response: '{"id":"resp_probe","output":[{"type":"message","content":[{"type":"output_text","text":"I am the configured upstream model."}]}]}' } });
    if (path.endsWith("/provider-models/discover")) return route.fulfill({ json: { object: "list", provider: "upstream", data: [{ id: "upstream/model-a", provider_id: "upstream", raw_id: "model-a", display_name: "Model A", context_window: 131072, max_output_tokens: 16384 }, { id: "upstream/model-without-metadata", provider_id: "upstream", raw_id: "model-without-metadata" }] } });
    if (path.endsWith("/logs")) return route.fulfill({ json: { items: [{ request_id: "req_01", started_at: "2026-08-15T05:00:00Z", client: "codex", provider: "openrouter", model: "gpt-5", status: "success", status_code: 200, duration_ms: 841 }] } });
    if (path.endsWith("/usage")) return route.fulfill({ json: { total: { requests: 24, success: 23, failed: 1, cancelled: 0, usage: { input_tokens: 12000, output_tokens: 4200, total_tokens: 16200 }, incomplete: false }, by_provider: { openrouter: { requests: 24, success: 23, failed: 1, cancelled: 0, usage: { input_tokens: 12000, output_tokens: 4200, total_tokens: 16200 }, incomplete: false } }, by_model: {}, by_client: {}, by_date: {} } });
    const match = path.match(/\/clients\/(codex|claude|grok)$/); if (match) { const client = match[1] as "codex" | "claude" | "grok"; return route.fulfill({ json: { client, point_state: status.clients[client].point_state, target: `C:/Users/test/.${client}/config`, backup_available: client !== "claude", ...(client === "codex" ? { remote_compaction: false } : {}) } }); }
    if (path.endsWith("/clients/codex/remote-compaction")) return route.fulfill({ json: { client: "codex", point_state: "pointed", target: "C:/Users/test/.codex/config", backup_available: true, remote_compaction: true } });
    return route.fulfill({ status: 200, json: {} });
  });
});

test("provider form exposes an editable upstream model catalog", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Providers" }).click();
  await page.getByRole("button", { name: "Add provider" }).click();
  await expect(page.getByRole("combobox", { name: "Disguise client" })).toHaveValue("");
  await page.getByRole("combobox", { name: "Disguise client" }).selectOption("claude");
  await expect(page.getByRole("combobox", { name: "Disguise client" })).toHaveValue("claude");
  await page.getByLabel("Identifier").fill("upstream");
  await page.getByLabel("Name").fill("Upstream");
  await page.getByLabel("Base URL").fill("https://example.com/v1");
  await page.getByRole("button", { name: "Add model manually" }).click();
  const typedModelId = page.getByLabel("Model ID").last();
  await typedModelId.evaluate((el) => el.scrollIntoView({ block: "center", inline: "nearest" }));
  await typedModelId.click({ force: true });
  await page.keyboard.type("claude-opus-5");
  await expect(typedModelId).toHaveValue("claude-opus-5");
  await expect(typedModelId).toBeFocused();
  await page.getByRole("button", { name: "Fetch models" }).click({ force: true });
  await expect(page.locator('input[value="model-a"]')).toBeVisible();
  await expect(page.getByText("For Claude 1M context, append the [1m] suffix to the model ID, for example claude-opus-5[1m].")).toBeVisible();
  await expect(page.getByLabel("Default adapter")).toHaveCount(0);
  await expect(page.getByPlaceholder("Not provided upstream")).toHaveCount(0);
  await expect(page.getByLabel(/^Request endpoint/).last()).toHaveValue("/v1/chat/completions");
  await expect(page.getByLabel(/^Request endpoint/).last()).toHaveAttribute("readonly");
  await expect(page.getByText("https://example.com/v1/chat/completions").first()).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("provider-model-catalog.png"), fullPage: true });
});

test("local API exposes connection values and enabled models", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Local API" }).click();
  await expect(page.getByText("http://127.0.0.1:12600/v1", { exact: true })).toBeVisible();
  await expect(page.getByText("http://127.0.0.1:12600/v1/models", { exact: true })).toBeVisible();
  await expect(page.getByText("openrouter/anthropic/claude-sonnet-4", { exact: true })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("local-api.png"), fullPage: true });
});

test("client routes list every enabled model as provider/model id", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Routes" }).click();
  await expect(page.getByText("This sets the model the client selects by default at startup", { exact: false })).toBeVisible();
  const modelSelect = page.getByRole("combobox", { name: "codex Default selected model" });
  await expect(modelSelect).toBeVisible();
  await expect(modelSelect).toHaveJSProperty("required", true);
  await expect(modelSelect).toHaveValue("openrouter/gpt-5");
  await expect(modelSelect.locator("option", { hasText: "Select a default model" })).toHaveCount(1);
  await expect(modelSelect.locator("option", { hasText: "openrouter/gpt-5" })).toHaveCount(1);
  await expect(modelSelect.locator("option", { hasText: "openrouter/anthropic/claude-sonnet-4" })).toHaveCount(1);
  await expect(modelSelect.locator("option", { hasText: "deepseek/deepseek-chat" })).toHaveCount(1);
  const clientRoutesTop = await page.getByText("Client routes", { exact: true }).evaluate((element) => element.getBoundingClientRect().top);
  const catalogTop = await page.getByText("Provider and model catalog", { exact: true }).evaluate((element) => element.getBoundingClientRect().top);
  expect(clientRoutesTop).toBeLessThan(catalogTop);
  await page.getByRole("button", { name: "OpenRouter Show models" }).click();
  await expect(page.locator(".tree-model-row .mono", { hasText: "openrouter/gpt-5" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("client-routes.png"), fullPage: true });
});

test("disabling a provider clears the required client route", async ({ page }, testInfo) => {
  const liveProviders = [
    { id: "openrouter", name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", enabled: true, models: providerModels, has_secret: true, capabilities: { image_input: true, reasoning: true } },
    { id: "deepseek", name: "DeepSeek", adapter: "openai-chat", base_url: "https://api.deepseek.com", default_model: "deepseek-chat", enabled: true, models: [{ id: "deepseek-chat", name: "DeepSeek Chat", context_window: 128000, max_output_tokens: 8192 }], has_secret: true, capabilities: { image_input: false, reasoning: true } },
  ];
  await page.route("http://127.0.0.1:12600/api/v1/providers", async (route) => {
    if (route.request().method() === "GET") return route.fulfill({ json: liveProviders });
    return route.fallback();
  });
  await page.route("http://127.0.0.1:12600/api/v1/providers/openrouter/availability", async (route) => {
    const body = route.request().postDataJSON() as { enabled?: boolean };
    liveProviders[0] = { ...liveProviders[0], enabled: body.enabled !== false };
    return route.fulfill({ json: liveProviders[0] });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Routes" }).click();
  const modelSelect = page.getByRole("combobox", { name: "codex Default selected model" });
  const grokSelect = page.getByRole("combobox", { name: "grok Default selected model" });
  await expect(modelSelect).toHaveValue("openrouter/gpt-5");
  await expect(grokSelect).toHaveValue("deepseek/deepseek-chat");
  await page.getByRole("checkbox", { name: "Provider OpenRouter" }).click();
  await expect(modelSelect).toHaveValue("");
  await expect(page.getByRole("combobox", { name: "claude Default selected model" })).toHaveValue("");
  await expect(page.getByRole("combobox", { name: "generic Default selected model" })).toHaveValue("");
  await expect(grokSelect).toHaveValue("deepseek/deepseek-chat");
  await expect(page.getByText("The current default route is no longer available. Select another model and apply it.").first()).toBeVisible();
  await expect(modelSelect.locator("option", { hasText: "openrouter/gpt-5" })).toHaveCount(0);
  await expect(modelSelect.locator("option", { hasText: "deepseek/deepseek-chat" })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Apply" }).first()).toBeDisabled();
  await modelSelect.selectOption("deepseek/deepseek-chat");
  await expect(modelSelect).toHaveValue("deepseek/deepseek-chat");
  await expect(page.getByRole("button", { name: "Apply" }).first()).toBeEnabled();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("client-routes-cleared.png"), fullPage: true });
});

test("clients page exposes a Codex remote compaction switch", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Clients" }).click();
  const toggle = page.getByRole("checkbox", { name: "Remote compaction" });
  await expect(toggle).toBeVisible();
  await expect(toggle).not.toBeChecked();
  await expect(page.getByText("When on, Codex writes the provider display name OpenAI", { exact: false })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("codex-remote-compaction.png"), fullPage: true });
});

test("request logs retain route context at every breakpoint", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Logs" }).click();
  const firstRequest = page.locator(".log-table tbody tr").first();
  await expect(firstRequest).toContainText("Success");
  await expect(firstRequest).toContainText("openrouter");
  await expect(firstRequest).toContainText("gpt-5");
  await expect(firstRequest).toContainText("codex");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("request-logs.png"), fullPage: true });
});

test("all primary views fit and remain keyboard reachable", async ({ page }, testInfo) => {
  await page.goto("/");
  await expect(page.locator("h1", { hasText: "Overview" })).toBeVisible();
  for (const name of ["Local API", "Providers", "Routes", "Clients", "Logs", "Usage", "Settings"]) {
    await page.getByRole("button", { name }).focus(); await page.keyboard.press("Enter"); await expect(page.locator("h1", { hasText: name })).toBeVisible();
  }
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("settings.png"), fullPage: true });
});

test("desktop groups related controls into shared rows", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop-light", "Desktop density assertions require the desktop viewport.");
  await page.goto("/");

  await page.getByRole("button", { name: "Settings" }).click();
  const limitRows = page.locator(".limits-list .setting-row");
  const limitTops = await limitRows.evaluateAll((rows) => rows.slice(0, 4).map((row) => Math.round(row.getBoundingClientRect().top)));
  expect(new Set(limitTops).size).toBe(1);

  await page.getByRole("button", { name: "Local API" }).click();
  const accessTops = await page.locator(".compact-parameters > div").evaluateAll((rows) => rows.map((row) => Math.round(row.getBoundingClientRect().top)));
  expect(new Set(accessTops).size).toBe(1);

  await page.getByRole("button", { name: "Routes" }).click();
  const routeTops = await page.locator(".route-client-grid .route-row").evaluateAll((rows) => rows.map((row) => Math.round(row.getBoundingClientRect().top)));
  expect(routeTops[0]).toBe(routeTops[1]);
  expect(routeTops[2]).toBe(routeTops[3]);
  expect(routeTops[2]).toBeGreaterThan(routeTops[0]);

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
});

test("probe response is displayed as formatted JSON", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Providers" }).click();
  await page.getByRole("row", { name: /OpenRouter/ }).getByRole("button", { name: "Probe" }).click();
  const response = page.locator(".probe-modal pre");
  await expect(response).toContainText('"output": [');
  await expect(response).toContainText("I am the configured upstream model.");
  expect((await response.textContent())?.split("\n").length).toBeGreaterThan(5);
  await page.screenshot({ path: testInfo.outputPath("probe-response.png"), fullPage: true });
});
