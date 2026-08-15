export type ClientID = "codex" | "claude" | "grok" | "generic";
export type PointClient = Exclude<ClientID, "generic">;

export interface Route { provider: string; model: string }
export interface Status {
  version: string;
  pid: number;
  listen: string;
  logging_enabled: boolean;
  autostart_enabled: boolean;
  clients: Record<ClientID, { point_state: string }>;
  routes: Record<ClientID, Route>;
}
export interface Provider {
  id: string;
  name: string;
  adapter: string;
  base_url: string;
  default_model: string;
  has_secret: boolean;
  capabilities: { image_input: boolean; reasoning: boolean };
}
export interface Config {
  version: number;
  listen: { port: number };
  logging: { enabled: boolean; dir: string };
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
export interface TokenUsage { input_tokens: number; output_tokens: number; reasoning_tokens?: number; total_tokens: number }
export interface UsageGroup { requests: number; success: number; failed: number; cancelled: number; usage: TokenUsage | null; incomplete: boolean }
export interface UsageReport {
  total: UsageGroup;
  by_provider: Record<string, UsageGroup>;
  by_model: Record<string, UsageGroup>;
  by_client: Record<string, UsageGroup>;
  by_date: Record<string, UsageGroup>;
}
