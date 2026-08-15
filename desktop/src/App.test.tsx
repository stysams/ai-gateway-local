import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

const status = { version: "test", pid: 42, listen: "127.0.0.1:12600", logging_enabled: false, autostart_enabled: false, clients: { codex: { point_state: "not_pointed" }, claude: { point_state: "client_not_installed" }, grok: { point_state: "drifted" }, generic: { point_state: "unknown" } }, routes: { codex: { provider: "ollama", model: "qwen3" }, claude: { provider: "ollama", model: "qwen3" }, grok: { provider: "ollama", model: "qwen3" }, generic: { provider: "ollama", model: "qwen3" } } };
const config = { version: 1, listen: { port: 12600 }, logging: { enabled: false, dir: "logs" }, ui: { language: "en-US", logging_notice_accepted: true }, autostart: { enabled: false }, providers: { ollama: { name: "Ollama", adapter: "openai-chat", base_url: "http://127.0.0.1:11434/v1", default_model: "qwen3", capabilities: { image_input: false, reasoning: false } } }, routes: status.routes };
const providers = [{ id: "ollama", name: "Ollama", adapter: "openai-chat", base_url: "http://127.0.0.1:11434/v1", default_model: "qwen3", has_secret: false, capabilities: { image_input: false, reasoning: false } }];
const pointStatus = (client: string) => ({ client, point_state: client === "codex" ? "not_pointed" : "client_not_installed", target: `C:/${client}/config`, backup_available: false });

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith("/api/v1/status")) return Response.json(status);
    if (url.endsWith("/api/v1/config")) return Response.json(config);
    if (url.endsWith("/api/v1/providers")) return Response.json(providers);
    if (url.includes("/api/v1/logs?")) return Response.json({ items: [] });
    if (url.endsWith("/api/v1/usage")) return Response.json({ total: { requests: 0, success: 0, failed: 0, cancelled: 0, usage: null, incomplete: true }, by_provider: {}, by_model: {}, by_client: {}, by_date: {} });
    for (const client of ["codex", "claude", "grok"]) if (url.endsWith(`/api/v1/clients/${client}`)) return Response.json(pointStatus(client));
    if (url.includes("/api/v1/routes/codex") && init?.method === "PUT") return Response.json({ client: "codex", provider: "ollama", model: "new-model" });
    if (url.endsWith("/api/v1/logging") && init?.method === "PUT") return Response.json({ enabled: true });
    if (url.endsWith("/api/v1/autostart") && init?.method === "PUT") return Response.json({ enabled: true, valid: true });
    if (url.endsWith("/api/v1/clients/codex/point")) return Response.json({ ...pointStatus("codex"), point_state: "pointed", changed: true });
    return Response.json({ error: { code: "not_mocked", message: url } }, { status: 500 });
  });
});

async function ready() { await screen.findByRole("heading", { name: "Overview" }); }

describe("desktop workflow", () => {
  it("navigates by keyboard and switches a route", async () => {
    const user = userEvent.setup(); render(<App />); await ready();
    const routes = screen.getByRole("button", { name: "Routes" }); routes.focus(); await user.keyboard("{Enter}");
    expect(await screen.findByText("Route changes apply to the next request.")).toBeVisible();
    const modelInputs = screen.getAllByDisplayValue("qwen3"); await user.clear(modelInputs[0]); await user.type(modelInputs[0], "new-model");
    await user.click(screen.getAllByRole("button", { name: "Apply" })[0]);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/routes/codex"), expect.objectContaining({ method: "PUT", body: expect.stringContaining("new-model") })));
    const toast = await screen.findByRole("status");
    expect(toast).toHaveTextContent("Success");
    await user.click(within(toast).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("requires confirmation before pointing", async () => {
    const user = userEvent.setup(); const confirm = vi.spyOn(window, "confirm").mockReturnValue(false); render(<App />); await ready();
    await user.click(screen.getByRole("button", { name: "Clients" })); await user.click(screen.getAllByRole("button", { name: "Point to gateway" })[0]);
    expect(confirm).toHaveBeenCalled(); expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining("/point"), expect.anything());
  });

  it("shows logging disabled state and can enable it", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Logs" }));
    expect(screen.getAllByText("Disabled").length).toBeGreaterThan(0); await user.click(screen.getByRole("checkbox"));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/logging"), expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" })));
  });

  it("enables current-user login start from settings", async () => {
    const user = userEvent.setup(); render(<App />); await ready(); await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("checkbox", { name: "Start at login" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/api/v1/autostart"), expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" })));
  });
});
