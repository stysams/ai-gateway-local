import { describe, expect, it } from "vitest";
import { catalogId, enabledCatalog, isCatalogRoute, reconcileClientRoutes } from "./catalog";

describe("enabledCatalog", () => {
  it("lists every enabled model as provider/model id", () => {
    const catalog = enabledCatalog([
      {
        id: "openrouter",
        enabled: true,
        default_model: "gpt-5",
        models: [
          { id: "gpt-5", name: "GPT-5" },
          { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4" },
          { id: "hidden", name: "Hidden", enabled: false },
        ],
      },
      {
        id: "ollama",
        enabled: false,
        default_model: "qwen3",
        models: [{ id: "qwen3" }],
      },
      { id: "deepseek", default_model: "deepseek-chat", models: [] },
    ]);
    expect(catalog.map((item) => item.id)).toEqual([
      "openrouter/gpt-5",
      "openrouter/anthropic/claude-sonnet-4",
      "deepseek/deepseek-chat",
    ]);
  });

  it("keeps the route wire form as provider plus raw model id", () => {
    expect(catalogId({ provider: "openrouter", model: "anthropic/claude-sonnet-4" })).toBe("openrouter/anthropic/claude-sonnet-4");
    expect(catalogId({ provider: "", model: "" })).toBe("");
  });

  it("treats a disabled provider route as unavailable", () => {
    const catalog = enabledCatalog([
      { id: "openrouter", enabled: true, default_model: "gpt-5", models: [{ id: "gpt-5" }] },
      { id: "ollama", enabled: false, default_model: "qwen3", models: [{ id: "qwen3" }] },
    ]);
    expect(isCatalogRoute({ provider: "ollama", model: "qwen3" }, catalog)).toBe(false);
    expect(isCatalogRoute({ provider: "openrouter", model: "gpt-5" }, catalog)).toBe(true);
  });

  it("clears saved and draft routes that leave the enabled catalog", () => {
    const catalog = enabledCatalog([
      { id: "openrouter", enabled: true, default_model: "gpt-5", models: [{ id: "gpt-5" }] },
      { id: "ollama", enabled: false, default_model: "qwen3", models: [{ id: "qwen3" }] },
    ]);
    const next = reconcileClientRoutes(
      { codex: { provider: "ollama", model: "qwen3" }, claude: { provider: "openrouter", model: "gpt-5" } },
      { codex: { provider: "ollama", model: "qwen3" }, claude: { provider: "ollama", model: "qwen3" }, grok: { provider: "openrouter", model: "gpt-5" }, generic: { provider: "missing", model: "x" } },
      catalog,
    );
    expect(next).toEqual({
      codex: { provider: "", model: "" },
      claude: { provider: "openrouter", model: "gpt-5" },
      grok: { provider: "openrouter", model: "gpt-5" },
      generic: { provider: "", model: "" },
    });
  });
});
