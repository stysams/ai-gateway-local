import { describe, expect, it } from "vitest";
import { validateProvider } from "./validation";

const provider = (overrides: Record<string, unknown> = {}) => ({ id: "openai-main", name: "OpenAI", base_url: "https://api.openai.com/v1", extra_headers: [], disguise_client: "" as const, key_groups: { main: { key_id: "main", name: "Main", enabled: true, has_api_key: true, api_key: "", model_count: 1, default_model: "gpt-5", endpoint: "/v1/responses", adapter: "openai-responses", models: [{ id: "gpt-5", name: "GPT-5", adapter: "openai-responses" }] } }, ...overrides });

describe("validateProvider", () => {
  it("accepts a complete provider with key groups", () => { expect(validateProvider(provider())).toEqual({}); });

  it("reports invalid provider and key-group fields", () => {
    const errors = validateProvider(provider({ id: "Bad ID", name: "", base_url: "file:///tmp", disguise_client: "gpt", key_groups: {} }));
    expect(Object.keys(errors).sort()).toEqual(["base_url", "disguise_client", "id", "key_groups", "name"]);
  });

  it("validates duplicate models and the default model reference", () => {
    const value = provider({ key_groups: { main: { ...provider().key_groups.main, default_model: "missing", models: [{ id: "m" }, { id: "m" }] } } });
    const errors = validateProvider(value);
    expect(errors["key_groups.main.default_model"]).toBe("default_model_missing");
    expect(errors["key_groups.main.models.1.id"]).toBe("duplicate_model");
  });

  it("accepts model adapters and rejects unsupported adapters", () => {
    expect(validateProvider(provider())).toEqual({});
    const value = provider({ key_groups: { main: { ...provider().key_groups.main, models: [{ id: "gpt-5", adapter: "other" }] } } });
    expect(validateProvider(value)["key_groups.main.models.0.adapter"]).toBe("invalid_adapter");
  });

  it("rejects locked preset endpoint overrides", () => {
    const value = provider({ key_groups: { main: { ...provider().key_groups.main, models: [{ id: "gpt-5", adapter: "openai-responses", endpoint: "/responses" }] } } });
    expect(validateProvider(value)["key_groups.main.models.0.endpoint"]).toBe("preset_endpoint_locked");
  });
});
