import { describe, expect, it } from "vitest";
import { CLAUDE_CODE_HEADERS, CODEX_HEADERS, mergeHeaderPreset, presetForAdapter } from "./headerPresets";

describe("request header presets", () => {
  it("maps protocol adapters to their verified client preset", () => {
    expect(presetForAdapter("anthropic").headers).toBe(CLAUDE_CODE_HEADERS);
    expect(presetForAdapter("openai-responses").headers).toBe(CODEX_HEADERS);
    expect(Object.fromEntries(CODEX_HEADERS.map((header) => [header.name, header.value]))).toEqual({ "User-Agent": "codex_cli_rs/0.147.0", Originator: "codex_cli_rs" });
  });

  it("replaces preset fields case-insensitively and preserves custom fields", () => {
    expect(mergeHeaderPreset([{ name: "user-agent", value: "old" }, { name: "X-App", value: "old-preset" }, { name: "X-Custom", value: "kept" }], CODEX_HEADERS)).toEqual([
      { name: "X-Custom", value: "kept" },
      ...CODEX_HEADERS,
    ]);
  });
});
