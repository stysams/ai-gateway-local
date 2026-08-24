export type ClientID = "codex" | "claude" | "grok" | "generic";
export type PointClient = Exclude<ClientID, "generic">;

export interface Route { provider: string; model: string }
export interface Status {
  version: string;
  pid: number;
  listen: string;
  active_requests?: number;
  logging_enabled: boolean;
  logging_body_enabled: boolean;
  autostart_enabled: boolean;
  clients: Record<ClientID, { point_state: string }>;
  routes: Record<ClientID, Route>;
}
export interface LocalAccessModel {
  id: string;
  object: "model";
  created: number;
  owned_by: string;
  display_name: string;
}
export interface LocalAccess {
  base_url: string;
  api_key: string;
  auth_required: boolean;
  default_model: string;
  default_route: Route;
  endpoints: {
    models: string;
    chat_completions: string;
    responses: string;
    messages: string;
  };
  models: LocalAccessModel[];
}
export interface Provider {
  id: string;
  name: string;
  adapter: string;
  base_url: string;
  models_url?: string;
  extra_headers?: Record<string, string>;
  disguise_client?: "" | "claude" | "codex" | "pi";
  default_model: string;
  enabled?: boolean;
  models: ProviderModel[];
  has_secret: boolean;
  capabilities: { image_input: boolean; reasoning: boolean; context_management: boolean };
}
export interface ProviderModel {
  id: string;
  name?: string;
  adapter?: string;
  endpoint?: string;
  enabled?: boolean;
}
export interface DiscoveredProviderModel {
  id: string;
  provider_id: string;
  raw_id: string;
  display_name?: string;
  owned_by?: string;
  context_window?: number;
  max_output_tokens?: number;
}
export interface Config {
  version: number;
  listen: { host?: string; port: number };
  logging: { enabled: boolean; body: boolean; redact?: boolean; dir: string; retention_days: number; quota_bytes: number };
  limits?: {
    global: number;
    per_client: number;
    per_provider: number;
    stream_idle_seconds: number;
    request_body_bytes: number;
    request_header_bytes: number;
    client_rate_per_minute: number;
  };
  ui: { language: string; logging_notice_accepted: boolean };
  autostart: { enabled: boolean };
  providers: Record<string, Omit<Provider, "id" | "has_secret"> & { secret_ref?: string }>;
  routes: Record<ClientID, Route>;
}
export interface PointStatus {
  client: PointClient;
  point_state: string;
  target: string;
  backup_available: boolean;
  message?: string;
  backup_dir?: string;
  changed?: boolean;
  remote_compaction?: boolean;
  subagent_model?: string;
  title_model?: string;
}
export interface LogSummary {
  request_id: string;
  started_at: string;
  completed_at?: string;
  client?: string;
  protocol?: string;
  provider?: string;
  model?: string;
  status: string;
  status_code?: number;
  duration_ms?: number;
  usage?: TokenUsage;
}
export interface TokenUsage {
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens?: number;
  cache_creation_input_tokens?: number;
  cache_read_input_tokens?: number;
  cache_input_tokens?: number;
  total_tokens: number;
}
export interface UsageGroup { requests: number; success: number; failed: number; cancelled: number; usage_requests?: number; usage: TokenUsage | null; incomplete: boolean }
export interface UsageReport {
  total: UsageGroup;
  by_provider: Record<string, UsageGroup>;
  by_model: Record<string, UsageGroup>;
  by_client: Record<string, UsageGroup>;
  by_date: Record<string, UsageGroup>;
  by_hour?: Record<string, UsageGroup>;
}
