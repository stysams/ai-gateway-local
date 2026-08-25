import { describe, expect, it } from "vitest";
import { catalogId, enabledCatalog, isCatalogRoute, reconcileClientRoutes } from "./catalog";

const group = (key_id: string, models: { id: string; enabled?: boolean }[]) => ({ key_id, name: key_id, enabled: true, has_api_key: true, model_count: models.length, default_model: models[0]?.id || "", models });

describe("enabledCatalog", () => {
  it("lists every enabled model as provider/key/model id", () => {
    const catalog = enabledCatalog([
      { id: "openrouter", enabled: true, key_groups: { main: group("main", [{ id: "gpt-5" }, { id: "anthropic/claude-sonnet-4" }, { id: "hidden", enabled: false }]) } },
      { id: "ollama", enabled: false, key_groups: { default: group("default", [{ id: "qwen3" }]) } },
      { id: "deepseek", key_groups: { default: group("default", [{ id: "deepseek-chat" }]) } },
    ]);
    expect(catalog.map((item) => item.id)).toEqual(["openrouter/main/gpt-5", "openrouter/main/anthropic/claude-sonnet-4", "deepseek/default/deepseek-chat"]);
  });

  it("keeps provider, key group, and raw model id in the route wire form", () => {
    expect(catalogId({ provider: "openrouter", key_id: "main", model: "anthropic/claude-sonnet-4" })).toBe("openrouter/main/anthropic/claude-sonnet-4");
    expect(catalogId({ provider: "", key_id: "", model: "" })).toBe("");
  });

  it("treats disabled groups and providers as unavailable", () => {
    const catalog = enabledCatalog([{ id: "openrouter", enabled: true, key_groups: { main: group("main", [{ id: "gpt-5" }]) } }, { id: "ollama", enabled: false, key_groups: { default: group("default", [{ id: "qwen3" }]) } }]);
    expect(isCatalogRoute({ provider: "ollama", key_id: "default", model: "qwen3" }, catalog)).toBe(false);
    expect(isCatalogRoute({ provider: "openrouter", key_id: "main", model: "gpt-5" }, catalog)).toBe(true);
  });

  it("clears saved and draft routes that leave the enabled catalog", () => {
    const catalog = enabledCatalog([{ id: "openrouter", enabled: true, key_groups: { main: group("main", [{ id: "gpt-5" }]) } }, { id: "ollama", enabled: false, key_groups: { default: group("default", [{ id: "qwen3" }]) } }]);
    const route = (provider: string, key_id: string, model: string) => ({ provider, key_id, model });
    const next = reconcileClientRoutes(
      { codex: route("ollama", "default", "qwen3"), claude: route("openrouter", "main", "gpt-5") },
      { codex: route("ollama", "default", "qwen3"), claude: route("ollama", "default", "qwen3"), "claude-desktop": route("ollama", "default", "qwen3"), grok: route("openrouter", "main", "gpt-5"), generic: route("missing", "default", "x") },
      catalog,
    );
    expect(next).toEqual({ codex: { provider: "", key_id: "", model: "" }, claude: route("openrouter", "main", "gpt-5"), "claude-desktop": { provider: "", key_id: "", model: "" }, grok: route("openrouter", "main", "gpt-5"), generic: { provider: "", key_id: "", model: "" } });
  });
});
