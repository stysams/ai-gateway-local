import type { ClientID, Config, LogSummary, PointClient, PointStatus, Provider, Route, Status, UsageReport } from "./types";

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

export const api = {
  status: () => request<Status>("/api/v1/status"),
  config: () => request<Config>("/api/v1/config"),
  updateConfig: (config: Config) => request<Config>("/api/v1/config", { method: "PUT", body: JSON.stringify(config) }),
  providers: () => request<Provider[]>("/api/v1/providers"),
  saveProvider: (value: Record<string, unknown>, editing?: string) => request<Provider>(editing ? `/api/v1/providers/${editing}` : "/api/v1/providers", { method: editing ? "PUT" : "POST", body: JSON.stringify(value) }),
  deleteProvider: (id: string) => request<{ deleted: boolean; warning?: string }>(`/api/v1/providers/${id}`, { method: "DELETE" }),
  probeProvider: (id: string) => request<{ ok: boolean; status: number; latency_ms: number; models?: number; error?: string }>(`/api/v1/providers/${id}/probe`, { method: "POST" }),
  updateRoute: (client: ClientID, route: Route) => request<Route & { client: ClientID }>(`/api/v1/routes/${client}`, { method: "PUT", body: JSON.stringify(route) }),
  client: (client: PointClient) => request<PointStatus>(`/api/v1/clients/${client}`),
  point: (client: PointClient) => request<PointStatus>(`/api/v1/clients/${client}/point`, { method: "POST" }),
  restore: (client: PointClient) => request<PointStatus>(`/api/v1/clients/${client}/restore`, { method: "POST" }),
  logs: () => request<{ items: LogSummary[]; next_cursor?: string }>("/api/v1/logs?limit=50"),
  logDetail: (id: string) => request<{ request_id: string; events: unknown[] }>(`/api/v1/logs/${id}`),
  usage: () => request<UsageReport>("/api/v1/usage"),
  setLogging: (enabled: boolean) => request<{ enabled: boolean }>("/api/v1/logging", { method: "PUT", body: JSON.stringify({ enabled }) }),
  setAutostart: (enabled: boolean) => request<{ enabled: boolean; valid: boolean; executable?: string }>("/api/v1/autostart", { method: "PUT", body: JSON.stringify({ enabled }) }),
};
