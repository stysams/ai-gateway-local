import { expect, test } from "@playwright/test";

const status = { version: "0.1.0", pid: 4242, listen: "127.0.0.1:12600", logging_enabled: true, autostart_enabled: false, clients: { codex: { point_state: "pointed" }, claude: { point_state: "not_pointed" }, grok: { point_state: "drifted" }, generic: { point_state: "unknown" } }, routes: { codex: { provider: "openrouter", model: "gpt-5" }, claude: { provider: "anthropic", model: "claude-sonnet" }, grok: { provider: "openrouter", model: "grok-4" }, generic: { provider: "ollama", model: "qwen3" } } };
const providerModels = [
  { id: "gpt-5", name: "GPT-5", context_window: 400000, max_output_tokens: 128000 },
  { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4", context_window: 200000, max_output_tokens: 64000 },
];
const config = { version: 1, listen: { port: 12600 }, logging: { enabled: true, dir: "logs" }, ui: { language: "en-US", logging_notice_accepted: true }, autostart: { enabled: false }, providers: { openrouter: { name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: providerModels, capabilities: { image_input: true, reasoning: true } } }, routes: status.routes };

test.beforeEach(async ({ page }) => {
  await page.route("http://127.0.0.1:12600/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/status")) return route.fulfill({ json: status });
    if (path.endsWith("/config")) return route.fulfill({ json: config });
    if (path.endsWith("/providers")) return route.fulfill({ json: [{ id: "openrouter", name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", models: providerModels, has_secret: true, capabilities: { image_input: true, reasoning: true } }, { id: "deepseek", name: "DeepSeek", adapter: "openai-chat", base_url: "https://api.deepseek.com", default_model: "deepseek-chat", models: [{ id: "deepseek-chat", name: "DeepSeek Chat", context_window: 128000, max_output_tokens: 8192 }], has_secret: true, capabilities: { image_input: false, reasoning: true } }] });
    if (path.endsWith("/providers/openrouter/probe")) return route.fulfill({ json: { ok: true, status: 200, latency_ms: 318, models: 2, response: '{"id":"resp_probe","output":[{"type":"message","content":[{"type":"output_text","text":"I am the configured upstream model."}]}]}' } });
    if (path.endsWith("/provider-models/discover")) return route.fulfill({ json: { object: "list", provider: "upstream", data: [{ id: "upstream/model-a", provider_id: "upstream", raw_id: "model-a", display_name: "Model A", context_window: 131072, max_output_tokens: 16384 }, { id: "upstream/model-without-metadata", provider_id: "upstream", raw_id: "model-without-metadata" }] } });
    if (path.endsWith("/logs")) return route.fulfill({ json: { items: [{ request_id: "req_01", started_at: "2026-08-15T05:00:00Z", client: "codex", provider: "openrouter", model: "gpt-5", status: "success", status_code: 200, duration_ms: 841 }] } });
    if (path.endsWith("/usage")) return route.fulfill({ json: { total: { requests: 24, success: 23, failed: 1, cancelled: 0, usage: { input_tokens: 12000, output_tokens: 4200, total_tokens: 16200 }, incomplete: false }, by_provider: { openrouter: { requests: 24, success: 23, failed: 1, cancelled: 0, usage: { input_tokens: 12000, output_tokens: 4200, total_tokens: 16200 }, incomplete: false } }, by_model: {}, by_client: {}, by_date: {} } });
    const match = path.match(/\/clients\/(codex|claude|grok)$/); if (match) { const client = match[1] as "codex" | "claude" | "grok"; return route.fulfill({ json: { client, point_state: status.clients[client].point_state, target: `C:/Users/test/.${client}/config`, backup_available: client !== "claude" } }); }
    return route.fulfill({ status: 200, json: {} });
  });
});

test("provider form exposes an editable upstream model catalog", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Providers" }).click();
  await page.getByRole("button", { name: "Add provider" }).click();
  await page.getByLabel("Identifier").fill("upstream");
  await page.getByLabel("Name").fill("Upstream");
  await page.getByLabel("Base URL").fill("https://example.com/v1");
  await page.getByRole("button", { name: "Fetch models" }).click();
  await expect(page.locator('input[value="model-a"]')).toBeVisible();
  await expect(page.locator('input[value="131072"]')).toBeVisible();
  await expect(page.getByPlaceholder("Not provided upstream").first()).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("provider-model-catalog.png"), fullPage: true });
});

test("client routes list every enabled model as provider/model id", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Routes" }).click();
  await expect(page.getByText("This sets the model the client selects by default at startup", { exact: false })).toBeVisible();
  const modelSelect = page.getByRole("combobox", { name: "codex Default selected model" });
  await expect(modelSelect).toBeVisible();
  await expect(modelSelect.locator("option", { hasText: "openrouter/gpt-5" })).toHaveCount(1);
  await expect(modelSelect.locator("option", { hasText: "openrouter/anthropic/claude-sonnet-4" })).toHaveCount(1);
  await expect(modelSelect.locator("option", { hasText: "deepseek/deepseek-chat" })).toHaveCount(1);
  await expect(page.locator(".tree-model-row .mono", { hasText: "openrouter/gpt-5" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("client-routes.png"), fullPage: true });
});

test("all primary views fit and remain keyboard reachable", async ({ page }, testInfo) => {
  await page.goto("/");
  await expect(page.locator("h1", { hasText: "Overview" })).toBeVisible();
  for (const name of ["Providers", "Routes", "Clients", "Logs", "Usage", "Settings"]) {
    await page.getByRole("button", { name }).focus(); await page.keyboard.press("Enter"); await expect(page.locator("h1", { hasText: name })).toBeVisible();
  }
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("settings.png"), fullPage: true });
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
