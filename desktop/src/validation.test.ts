import { describe, expect, it } from "vitest";
import { validateProvider } from "./validation";

describe("validateProvider", () => {
  it("accepts a complete provider", () => {
    expect(validateProvider({ id: "openai-main", name: "OpenAI", adapter: "openai-responses", base_url: "https://api.openai.com/v1", default_model: "gpt-5", api_key: "" })).toEqual({});
  });

  it("reports each invalid field", () => {
    const errors = validateProvider({ id: "Bad ID", name: "", adapter: "other", base_url: "file:///tmp", default_model: "", api_key: "" });
    expect(Object.keys(errors).sort()).toEqual(["adapter", "base_url", "default_model", "id", "name"]);
  });
});
