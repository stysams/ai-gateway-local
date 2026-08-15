import { expect, test } from "@playwright/test";

const status = { version: "0.1.0", pid: 4242, listen: "127.0.0.1:12600", logging_enabled: true, autostart_enabled: false, clients: { codex: { point_state: "pointed" }, claude: { point_state: "not_pointed" }, grok: { point_state: "drifted" }, generic: { point_state: "unknown" } }, routes: { codex: { provider: "openrouter", model: "gpt-5" }, claude: { provider: "anthropic", model: "claude-sonnet" }, grok: { provider: "openrouter", model: "grok-4" }, generic: { provider: "ollama", model: "qwen3" } } };
const config = { version: 1, listen: { port: 12600 }, logging: { enabled: true, dir: "logs" }, ui: { language: "en-US", logging_notice_accepted: true }, autostart: { enabled: false }, providers: {}, routes: status.routes };

test.beforeEach(async ({ page }) => {
  await page.route("http://127.0.0.1:12600/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/status")) return route.fulfill({ json: status });
    if (path.endsWith("/config")) return route.fulfill({ json: config });
    if (path.endsWith("/providers")) return route.fulfill({ json: [{ id: "openrouter", name: "OpenRouter", adapter: "openai-responses", base_url: "https://openrouter.ai/api/v1", default_model: "gpt-5", has_secret: true, capabilities: { image_input: true, reasoning: true } }] });
    if (path.endsWith("/logs")) return route.fulfill({ json: { items: [{ request_id: "req_01", started_at: "2026-08-15T05:00:00Z", client: "codex", provider: "openrouter", model: "gpt-5", status: "success", status_code: 200, duration_ms: 841 }] } });
    if (path.endsWith("/usage")) return route.fulfill({ json: { total: { requests: 24, success: 23, failed: 1, cancelled: 0, usage: { input_tokens: 12000, output_tokens: 4200, total_tokens: 16200 }, incomplete: false }, by_provider: { openrouter: { requests: 24, success: 23, failed: 1, cancelled: 0, usage: { input_tokens: 12000, output_tokens: 4200, total_tokens: 16200 }, incomplete: false } }, by_model: {}, by_client: {}, by_date: {} } });
    const match = path.match(/\/clients\/(codex|claude|grok)$/); if (match) { const client = match[1] as "codex" | "claude" | "grok"; return route.fulfill({ json: { client, point_state: status.clients[client].point_state, target: `C:/Users/test/.${client}/config`, backup_available: client !== "claude" } }); }
    return route.fulfill({ status: 200, json: {} });
  });
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
