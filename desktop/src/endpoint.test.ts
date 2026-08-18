import { describe, expect, it } from "vitest";
import { inferWire, joinCompletionURL, presetPath, validateCustomEndpoint } from "./endpoint";

describe("endpoint join", () => {
  it("adds /v1 for preset Claude and GPT adapters", () => {
    expect(joinCompletionURL("https://agentrouter.org", "openai-responses")).toBe("https://agentrouter.org/v1/responses");
    expect(joinCompletionURL("https://agentrouter.org", "anthropic")).toBe("https://agentrouter.org/v1/messages");
    expect(joinCompletionURL("https://openrouter.ai/api/v1", "openai-chat")).toBe("https://openrouter.ai/api/v1/chat/completions");
  });

  it("keeps a custom path exactly as written", () => {
    expect(joinCompletionURL("https://api.2dou.net", "custom", "/responses")).toBe("https://api.2dou.net/responses");
    expect(inferWire("/v1/chat/completions")).toBe("openai-chat");
    expect(validateCustomEndpoint("/v1/foo")).toBe("unknown_endpoint");
    expect(presetPath("anthropic")).toBe("/v1/messages");
  });
});
