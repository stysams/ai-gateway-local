import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, api, apiBase } from "./api";

afterEach(() => { vi.restoreAllMocks(); window.history.replaceState({}, "", "/"); });

describe("management API client", () => {
  it("uses the Wails-provided loopback endpoint", () => {
    window.history.replaceState({}, "", "/?api=http://127.0.0.1:13000/");
    expect(apiBase()).toBe("http://127.0.0.1:13000");
  });

  it("sends route updates as JSON", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ client: "codex", provider: "p", model: "m" }), { status: 200, headers: { "Content-Type": "application/json" } }));
    await api.updateRoute("codex", { provider: "p", model: "m" });
    expect(fetcher).toHaveBeenCalledWith("http://127.0.0.1:12600/api/v1/routes/codex", expect.objectContaining({ method: "PUT", body: JSON.stringify({ provider: "p", model: "m" }) }));
  });

  it("loads local access connection data", async () => {
    const value = { base_url: "http://127.0.0.1:12600/v1", models: [] };
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(value));
    await expect(api.localAccess()).resolves.toEqual(value);
    expect(fetcher).toHaveBeenCalledWith("http://127.0.0.1:12600/api/v1/local-access", expect.anything());
  });

  it("normalizes API errors", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ error: { code: "config_invalid", message: "bad route" } }), { status: 400 }));
    await expect(api.updateRoute("codex", { provider: "", model: "" })).rejects.toEqual(expect.objectContaining<Partial<APIError>>({ status: 400, code: "config_invalid", message: "bad route" }));
  });

  it("updates Codex remote compaction through its dedicated endpoint", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ client: "codex", remote_compaction: true }));
    await api.setCodexRemoteCompaction(true);
    expect(fetcher).toHaveBeenCalledWith("http://127.0.0.1:12600/api/v1/clients/codex/remote-compaction", expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" }));
  });

  it("updates autostart through its dedicated endpoint", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ enabled: true, valid: true }));
    await api.setAutostart(true);
    expect(fetcher).toHaveBeenCalledWith("http://127.0.0.1:12600/api/v1/autostart", expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" }));
  });

  it("discovers models from an unsaved provider draft", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ object: "list", provider: "openrouter", data: [] }));
    await api.discoverProviderModels({ provider_id: "openrouter", adapter: "openai-chat", base_url: "https://openrouter.ai/api/v1", api_key: "secret" });
    expect(fetcher).toHaveBeenCalledWith("http://127.0.0.1:12600/api/v1/provider-models/discover", expect.objectContaining({ method: "POST", body: JSON.stringify({ provider_id: "openrouter", adapter: "openai-chat", base_url: "https://openrouter.ai/api/v1", api_key: "secret" }) }));
  });
});
