export const wireAdapters = ["openai-chat", "openai-responses", "anthropic"] as const;
export const modelAdapters = [...wireAdapters, "custom"] as const;

export type WireAdapter = (typeof wireAdapters)[number];
export type ModelAdapter = (typeof modelAdapters)[number];

export function isWireAdapter(value: string): value is WireAdapter {
  return (wireAdapters as readonly string[]).includes(value);
}

export function isModelAdapter(value: string): value is ModelAdapter {
  return (modelAdapters as readonly string[]).includes(value);
}

export function presetPath(adapter: string): string {
  switch (adapter) {
    case "openai-chat":
      return "/v1/chat/completions";
    case "openai-responses":
      return "/v1/responses";
    case "anthropic":
      return "/v1/messages";
    default:
      return "";
  }
}

export function wirePath(adapter: string): string {
  switch (adapter) {
    case "openai-chat":
      return "/chat/completions";
    case "openai-responses":
      return "/responses";
    case "anthropic":
      return "/messages";
    default:
      return "";
  }
}

export function inferWire(customEndpoint?: string): WireAdapter | "" {
  const path = (customEndpoint || "").trim().replace(/\/+$/, "");
  if (path.endsWith("/chat/completions")) return "openai-chat";
  if (path.endsWith("/responses")) return "openai-responses";
  if (path.endsWith("/messages")) return "anthropic";
  return "";
}

export function validateCustomEndpoint(customEndpoint?: string): string {
  const path = (customEndpoint || "").trim();
  if (!path) return "required";
  if (!path.startsWith("/") || path.startsWith("//")) return "invalid_endpoint";
  if (/[\s?#]/.test(path)) return "invalid_endpoint";
  if (!inferWire(path)) return "unknown_endpoint";
  return "";
}

export function joinCompletionURL(baseURL: string, adapter: string, customEndpoint?: string): string {
  try {
    const parsed = new URL(baseURL);
    if (!["http:", "https:"].includes(parsed.protocol)) return "";
  } catch {
    return "";
  }
  const base = baseURL.replace(/\/+$/, "");
  const custom = (customEndpoint || "").trim();
  if (custom) {
    return base + (custom.startsWith("/") ? custom : `/${custom}`);
  }
  const suffix = wirePath(adapter);
  if (!suffix) return "";
  return base.endsWith("/v1") ? base + suffix : `${base}/v1${suffix}`;
}
