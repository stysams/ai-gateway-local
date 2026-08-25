import type { ClientID, Config, DiscoveredProviderModel, KeyGroup, LocalAccess, LogSummary, PointClient, PointStatus, Provider, Route, Status, UsageReport } from "./types";

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

export function apiBase(): string {
  const value = new URLSearchParams(window.location.search).get("api") || import.meta.env.VITE_API_URL || "http://127.0.0.1:12600";
  return value.replace(/\/$/, "");
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiBase() + path, {
    ...init,
    headers: init?.body ? { "Content-Type": "application/json", ...init.headers } : init?.headers,
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = body?.error;
    throw new APIError(response.status, error?.code || "request_failed", error?.message || response.statusText);
  }
  return body as T;
}

async function requestText(path: string, init?: RequestInit): Promise<string> {
  const response = await fetch(apiBase() + path, init);
  const body = await response.text();
  if (!response.ok) {
    const decoded = (() => { try { return JSON.parse(body); } catch { return {}; } })();
    throw new APIError(response.status, decoded?.error?.code || "request_failed", decoded?.error?.message || response.statusText);
  }
  return body;
}

export const api = {
  status: () => request<Status>("/api/v1/status"),
  localAccess: () => request<LocalAccess>("/api/v1/local-access"),
  config: () => request<Config>("/api/v1/config"),
  updateConfig: (config: Config) => request<Config>("/api/v1/config", { method: "PUT", body: JSON.stringify(config) }),
  providers: () => request<Provider[]>("/api/v1/providers"),
  saveProvider: (value: Record<string, unknown>, editing?: string) => request<Provider>(editing ? `/api/v1/providers/${editing}` : "/api/v1/providers", { method: editing ? "PUT" : "POST", body: JSON.stringify(value) }),
  deleteProvider: (id: string) => request<{ deleted: boolean; warning?: string }>(`/api/v1/providers/${id}`, { method: "DELETE" }),
  probeProvider: (id: string, keyID: string) => request<{ ok: boolean; status: number; latency_ms: number; models?: number; error?: string; response?: string }>(`/api/v1/providers/${id}/probe?key_id=${encodeURIComponent(keyID)}`, { method: "POST" }),
  keyGroups: (providerID: string) => request<KeyGroup[]>(`/api/v1/providers/${providerID}/keys`),
  saveKeyGroup: (providerID: string, keyID: string | undefined, value: Record<string, unknown>) => request<KeyGroup>(keyID ? `/api/v1/providers/${providerID}/keys/${keyID}` : `/api/v1/providers/${providerID}/keys`, { method: keyID ? "PUT" : "POST", body: JSON.stringify(keyID ? value : { ...value, key_id: value.key_id || keyID }) }),
  deleteKeyGroup: (providerID: string, keyID: string) => request<{ deleted: boolean }>(`/api/v1/providers/${providerID}/keys/${keyID}`, { method: "DELETE" }),
  probeKeyGroup: (providerID: string, keyID: string) => request<{ ok: boolean; status: number; latency_ms: number; models?: number; error?: string; response?: string }>(`/api/v1/providers/${providerID}/keys/${keyID}/probe`, { method: "POST" }),
  discoverProviderModels: (value: { provider_id: string; key_id: string; adapter?: string; endpoint?: string; default_model?: string; base_url?: string; models_url?: string; extra_headers?: Record<string, string>; api_key?: string }) =>
    request<{ object: "list"; provider: string; data: DiscoveredProviderModel[] }>("/api/v1/provider-models/discover", { method: "POST", body: JSON.stringify(value) }),
  updateProviderAvailability: (id: string, value: { enabled?: boolean; models?: Record<string, boolean>; key_groups?: Record<string, { enabled?: boolean; models?: Record<string, boolean> }> }) =>
    request<Provider>(`/api/v1/providers/${id}/availability`, { method: "PUT", body: JSON.stringify(value) }),
  updateRoute: (client: ClientID, route: Route) => request<Route & { client: ClientID }>(`/api/v1/routes/${client}`, { method: "PUT", body: JSON.stringify(route) }),
  client: (client: PointClient) => request<PointStatus>(`/api/v1/clients/${client}`),
  point: (client: PointClient) => request<PointStatus>(`/api/v1/clients/${client}/point`, { method: "POST" }),
  setCodexRemoteCompaction: (enabled: boolean) => request<PointStatus>("/api/v1/clients/codex/remote-compaction", { method: "PUT", body: JSON.stringify({ enabled }) }),
  setClientHelperModels: (client: "codex" | "claude", subagentModel: string, titleModel: string) => request<PointStatus>(`/api/v1/clients/${client}/helper-models`, { method: "PUT", body: JSON.stringify({ subagent_model: subagentModel, title_model: titleModel }) }),
  restore: (client: PointClient) => request<PointStatus>(`/api/v1/clients/${client}/restore`, { method: "POST" }),
  logs: (cursor?: string) => {
    const query = new URLSearchParams({ limit: "20" });
    if (cursor) query.set("cursor", cursor);
    return request<{ items: LogSummary[]; next_cursor?: string }>(`/api/v1/logs?${query}`);
  },
  logDetail: (id: string) => request<{ request_id: string; events: unknown[] }>(`/api/v1/logs/${id}`),
  logExport: (id: string) => requestText(`/api/v1/logs/${id}/export`),
  deleteLog: (id: string) => request<{ deleted: boolean; request_id: string }>(`/api/v1/logs/${id}`, { method: "DELETE" }),
  clearLogs: () => request<{ removed: number }>("/api/v1/logs", { method: "DELETE" }),
  usage: (filters?: { from?: string; to?: string; provider?: string; model?: string; client?: string; status?: string }) => {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(filters || {})) if (value) query.set(key, value);
    const suffix = query.toString();
    return request<UsageReport>(`/api/v1/usage${suffix ? `?${suffix}` : ""}`);
  },
  setLogging: (enabled: boolean) => request<{ enabled: boolean; body: boolean }>("/api/v1/logging", { method: "PUT", body: JSON.stringify({ enabled }) }),
  setLoggingBody: (body: boolean) => request<{ enabled: boolean; body: boolean }>("/api/v1/logging", { method: "PUT", body: JSON.stringify({ body }) }),
  setLoggingRedact: (redact: boolean) => request<{ enabled: boolean; body: boolean; redact: boolean }>("/api/v1/logging", { method: "PUT", body: JSON.stringify({ redact }) }),
  setAutostart: (enabled: boolean) => request<{ enabled: boolean; valid: boolean; executable?: string }>("/api/v1/autostart", { method: "PUT", body: JSON.stringify({ enabled }) }),
};
