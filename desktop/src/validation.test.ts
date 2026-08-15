import { describe, expect, it } from "vitest";
import { validateProvider } from "./validation";

describe("validateProvider", () => {
  it("accepts a complete provider", () => {
    expect(validateProvider({ id: "openai-main", name: "OpenAI", adapter: "openai-responses", base_url: "https://api.openai.com/v1", default_model: "gpt-5", models: [{ id: "gpt-5", name: "GPT-5", context_window: 400000, max_output_tokens: 128000 }], api_key: "" })).toEqual({});
  });

  it("reports each invalid field", () => {
    const errors = validateProvider({ id: "Bad ID", name: "", adapter: "other", base_url: "file:///tmp", default_model: "", models: [], api_key: "" });
    expect(Object.keys(errors).sort()).toEqual(["adapter", "base_url", "default_model", "id", "name"]);
  });

  it("validates model metadata and the default model reference", () => {
    const errors = validateProvider({ id: "openrouter", name: "OpenRouter", adapter: "openai-chat", base_url: "https://openrouter.ai/api/v1", default_model: "missing", models: [{ id: "m", context_window: 1000, max_output_tokens: 2000 }, { id: "m", context_window: 0, max_output_tokens: 0 }], api_key: "" });
    expect(errors.default_model).toBe("default_model_missing");
    expect(errors["models.0.max_output_tokens"]).toBeUndefined();
    expect(errors["models.1.id"]).toBe("duplicate_model");
  });
});
