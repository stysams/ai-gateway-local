import { describe, expect, it } from "vitest";
import { catalogId, enabledCatalog } from "./catalog";

describe("enabledCatalog", () => {
  it("lists every enabled model as provider/model id", () => {
    const catalog = enabledCatalog([
      {
        id: "openrouter",
        enabled: true,
        default_model: "gpt-5",
        models: [
          { id: "gpt-5", name: "GPT-5", context_window: 0, max_output_tokens: 0 },
          { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4", context_window: 0, max_output_tokens: 0 },
          { id: "hidden", name: "Hidden", context_window: 0, max_output_tokens: 0, enabled: false },
        ],
      },
      {
        id: "ollama",
        enabled: false,
        default_model: "qwen3",
        models: [{ id: "qwen3", context_window: 0, max_output_tokens: 0 }],
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
  });
});
