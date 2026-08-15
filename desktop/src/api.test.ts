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

  it("normalizes API errors", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ error: { code: "config_invalid", message: "bad route" } }), { status: 400 }));
    await expect(api.updateRoute("codex", { provider: "", model: "" })).rejects.toEqual(expect.objectContaining<Partial<APIError>>({ status: 400, code: "config_invalid", message: "bad route" }));
  });

  it("updates autostart through its dedicated endpoint", async () => {
    const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ enabled: true, valid: true }));
    await api.setAutostart(true);
    expect(fetcher).toHaveBeenCalledWith("http://127.0.0.1:12600/api/v1/autostart", expect.objectContaining({ method: "PUT", body: "{\"enabled\":true}" }));
  });
});
