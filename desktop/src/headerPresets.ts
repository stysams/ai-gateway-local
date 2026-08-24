import type { RequestHeader } from "./validation";

// Keep these values aligned with internal/server/disguise.go. They were
// verified against Claude Code 2.1.228, Codex CLI 0.147.0 and Pi 0.84.2.

export const CLAUDE_CODE_HEADERS: RequestHeader[] = [
  { name: "User-Agent", value: "claude-cli/2.1.228 (external, cli)" },
  { name: "X-App", value: "cli" },
  { name: "Anthropic-Dangerous-Direct-Browser-Access", value: "true" },
  {
    name: "Anthropic-Beta",
    value: "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advanced-tool-use-2025-11-20,effort-2025-11-24",
  },
];

export const CODEX_HEADERS: RequestHeader[] = [
  { name: "User-Agent", value: "codex_cli_rs/0.147.0" },
  { name: "Originator", value: "codex_cli_rs" },
];

export const PI_HEADERS: RequestHeader[] = [
  { name: "User-Agent", value: "Pi Agent/1.0" },
];

export function presetForAdapter(adapter: string): { label: "Claude Code" | "Codex"; headers: RequestHeader[] } {
  return adapter === "anthropic"
    ? { label: "Claude Code", headers: CLAUDE_CODE_HEADERS }
    : { label: "Codex", headers: CODEX_HEADERS };
}

export function mergeHeaderPreset(current: RequestHeader[], preset: RequestHeader[]): RequestHeader[] {
  const presetNames = new Set([...CLAUDE_CODE_HEADERS, ...CODEX_HEADERS, ...PI_HEADERS].map((header) => header.name.toLowerCase()));
  return [...current.filter((header) => !presetNames.has(header.name.trim().toLowerCase())), ...preset.map((header) => ({ ...header }))];
}
