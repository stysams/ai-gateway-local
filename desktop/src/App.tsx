import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity, Boxes, Cable, Check, ChevronRight, CircleAlert, Copy, Database, Download,
  Filter, Gauge, Plus, RefreshCw, RotateCcw, Save, Server, Settings, Upload,
  ShieldCheck, Trash2, X,
} from "lucide-react";
import {
  Area, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis,
  type TooltipContentProps,
} from "recharts";
import { ClientsIcon, GatewayMark, LogsIcon, OverviewIcon, ProvidersIcon, RoutesIcon, SettingsIcon, UsageIcon, type AppIcon } from "./icons";
import { api } from "./api";
import { catalogId, enabledCatalog, isCatalogRoute, reconcileClientRoutes } from "./catalog";
import { inferWire, isModelAdapter, joinCompletionURL, modelAdapters, presetPath } from "./endpoint";
import { CLAUDE_CODE_HEADERS, CODEX_HEADERS, mergeHeaderPreset } from "./headerPresets";
import { translator, type Language, type MessageKey } from "./i18n";
import type { ClientID, Config, LocalAccess, LogSummary, PointClient, PointStatus, Provider, ProviderModel, Route, Status, TokenUsage, UsageGroup, UsageReport } from "./types";
import { validateProvider, type DisguiseClient, type ProviderFormValue } from "./validation";
import { SettingsPage, type Theme } from "./pages/SettingsPage";

type Page = "overview" | "localAccess" | "providers" | "routes" | "clients" | "logs" | "usage" | "settings";
type ToastKind = "error" | "success";
type Toast = { id: number; kind: ToastKind; message: string };
type RefreshResource = "status" | "localAccess" | "config" | "providers" | "clients" | "logs" | "usage";
type RunOperation = (operation: () => Promise<unknown>, message?: string, resources?: RefreshResource[]) => Promise<void>;
type UsageQuery = { from?: string; to?: string; provider?: string; model?: string; client?: string; status?: string };
const pointClients: PointClient[] = ["codex", "claude", "grok"];
const allClients: ClientID[] = ["codex", "claude", "grok", "generic"];

const navigation: { id: Page; icon: AppIcon }[] = [
  { id: "overview", icon: OverviewIcon }, { id: "localAccess", icon: GatewayMark }, { id: "providers", icon: ProvidersIcon }, { id: "routes", icon: RoutesIcon },
  { id: "clients", icon: ClientsIcon }, { id: "logs", icon: LogsIcon }, { id: "usage", icon: UsageIcon }, { id: "settings", icon: SettingsIcon },
];

const emptyProvider: ProviderFormValue = { id: "", name: "", adapter: "openai-chat", base_url: "", models_url: "", extra_headers: [], disguise_client: "", default_model: "", models: [], api_key: "" };

function useDialogFocus<T extends HTMLElement>(open: boolean, onClose?: () => void) {
  const dialogRef = useRef<T | null>(null);
  const previousFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;
    previousFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const dialog = dialogRef.current;
    if (!dialog) return;
    const focusableSelector = "button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex=\"-1\"])";
    const getFocusable = () => Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector));
    const focusable = getFocusable();
    (focusable[0] || dialog).focus();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && onClose) {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const current = getFocusable();
      if (!current.length) {
        event.preventDefault();
        return;
      }
      const first = current[0];
      const last = current[current.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      previousFocus.current?.focus();
    };
  }, [onClose, open]);

  return dialogRef;
}

function emptyModel(adapter: string): ProviderModel {
  return { id: "", name: "", adapter };
}

function modelAdapter(model: Pick<ProviderModel, "adapter"> | undefined, fallback: string): string {
  return model?.adapter && isModelAdapter(model.adapter) ? model.adapter : fallback;
}

function wireAdapter(model: Pick<ProviderModel, "adapter" | "endpoint"> | undefined, fallback: string): string {
  const adapter = modelAdapter(model, fallback);
  if (adapter === "custom") return inferWire(model?.endpoint) || (fallback === "custom" ? "openai-chat" : fallback);
  return adapter;
}

function discoveryAdapter(form: Pick<ProviderFormValue, "adapter" | "default_model" | "models">): string {
  const selected = form.models.find((model) => model.id && model.id === form.default_model);
  return wireAdapter(selected, wireAdapter(form.models[0], form.adapter || "openai-chat"));
}

function protocolLabel(adapter: string, t: (key: MessageKey) => string): string {
  return adapter === "custom" ? t("customProtocol") : adapter;
}

function displayedEndpoint(model: ProviderModel, fallback: string): string {
  return modelAdapter(model, fallback) === "custom" ? (model.endpoint || "") : presetPath(wireAdapter(model, fallback));
}

function requestURLPreview(baseURL: string, model: ProviderModel, fallback: string): string {
  const adapter = modelAdapter(model, fallback);
  return joinCompletionURL(baseURL, adapter === "custom" ? (inferWire(model.endpoint) || adapter) : adapter, adapter === "custom" ? model.endpoint : undefined);
}

function providerAdapters(provider: Provider): string {
  const models = provider.models?.length ? provider.models : [{ adapter: provider.adapter }];
  return [...new Set(models.map((model) => modelAdapter(model, provider.adapter)))].join(" / ");
}

function headerRecord(headers: ProviderFormValue["extra_headers"]): Record<string, string> {
  return Object.fromEntries(headers.map((header) => [header.name.trim(), header.value]));
}

export function App() {
  const [page, setPage] = useState<Page>("overview");
  const [language, setLanguage] = useState<Language>(() => (localStorage.getItem("language") as Language) || "zh-CN");
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem("theme") as Theme) || "system");
  const [status, setStatus] = useState<Status | null>(null);
  const [localAccess, setLocalAccess] = useState<LocalAccess | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [clients, setClients] = useState<Record<string, PointStatus>>({});
  const [logs, setLogs] = useState<LogSummary[]>([]);
  const [logCursor, setLogCursor] = useState<string>();
  const [usage, setUsage] = useState<UsageReport | null>(null);
  const [busy, setBusy] = useState(true);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextToastId = useRef(0);
  const refreshSequence = useRef(0);
  const usageSequence = useRef(0);
  const usageQuery = useRef<UsageQuery>({});
  const activeNavRef = useRef<HTMLButtonElement | null>(null);
  const t = useMemo(() => translator(language), [language]);

  useEffect(() => {
    const root = document.documentElement;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const applyTheme = () => {
      const dark = theme === "dark" || (theme === "system" && media.matches);
      root.dataset.theme = dark ? "dark" : "light";
    };
    applyTheme();
    root.lang = language;
    localStorage.setItem("theme", theme);
    localStorage.setItem("language", language);
    if (theme !== "system") return;
    media.addEventListener("change", applyTheme);
    return () => media.removeEventListener("change", applyTheme);
  }, [theme, language]);

  useEffect(() => {
    if (matchMedia("(max-width: 600px)").matches) activeNavRef.current?.scrollIntoView({ block: "nearest", inline: "center" });
  }, [page]);

  const pushToast = useCallback((kind: ToastKind, message: string) => {
    const id = nextToastId.current++;
    setToasts((current) => [...current.slice(-3), { id, kind, message }]);
    window.setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), kind === "error" ? 7000 : 4200);
  }, []);

  const refresh = useCallback(async (resources?: RefreshResource[]) => {
    const sequence = ++refreshSequence.current;
    const requested = new Set(resources || ["status", "localAccess", "config", "providers", "clients", "logs", "usage"]);
    setBusy(true);
    try {
      const [nextStatus, nextLocalAccess, nextConfig, nextProviders, nextLogs, nextUsage, ...nextClients] = await Promise.all([
        requested.has("status") ? api.status() : Promise.resolve(null), requested.has("localAccess") ? api.localAccess() : Promise.resolve(null), requested.has("config") ? api.config() : Promise.resolve(null), requested.has("providers") ? api.providers() : Promise.resolve(null), requested.has("logs") ? api.logs() : Promise.resolve(null), requested.has("usage") ? api.usage(usageQuery.current) : Promise.resolve(null), ...(requested.has("clients") ? pointClients.map(api.client) : pointClients.map(() => Promise.resolve(null))),
      ]);
      if (sequence !== refreshSequence.current) return;
      if (nextStatus) setStatus(nextStatus); if (nextLocalAccess) setLocalAccess(nextLocalAccess); if (nextConfig) { setConfig(nextConfig); if (nextConfig.ui.language === "zh-CN" || nextConfig.ui.language === "en-US") setLanguage(nextConfig.ui.language); } if (nextProviders) setProviders(nextProviders); if (nextLogs) { setLogs(nextLogs.items); setLogCursor(nextLogs.next_cursor); } if (nextUsage) setUsage(nextUsage);
      if (nextClients[0]) setClients(Object.fromEntries(nextClients.filter((value): value is PointStatus => value !== null).map((value) => [value.client, value])));
    } catch (reason) { pushToast("error", reason instanceof Error ? reason.message : String(reason)); }
    finally { setBusy(false); }
  }, [pushToast]);

  useEffect(() => {
    queueMicrotask(() => void refresh());
  }, [refresh]);

  const run: RunOperation = async (operation, message, resources) => {
    setToasts([]);
    try { await operation(); if (message) pushToast("success", message); await refresh(resources); }
    catch (reason) { pushToast("error", reason instanceof Error ? reason.message : String(reason)); }
  };

  const queryUsage = useCallback(async (query: UsageQuery) => {
    const sequence = ++usageSequence.current;
    usageQuery.current = query;
    setBusy(true);
    try {
      const nextUsage = await api.usage(query);
      if (sequence === usageSequence.current) setUsage(nextUsage);
    } catch (reason) {
      if (sequence === usageSequence.current) pushToast("error", reason instanceof Error ? reason.message : String(reason));
    } finally {
      if (sequence === usageSequence.current) setBusy(false);
    }
  }, [pushToast]);

  const acceptRisk = async () => {
    if (!config) return;
    await run(() => api.updateConfig({ ...config, ui: { ...config.ui, logging_notice_accepted: true } }), undefined, ["config"]);
  };

  const loadMoreLogs = async () => {
    if (!logCursor) return;
    try {
      const nextLogs = await api.logs(logCursor);
      setLogs((current) => [...current, ...nextLogs.items]);
      setLogCursor(nextLogs.next_cursor);
    } catch (reason) { pushToast("error", reason instanceof Error ? reason.message : String(reason)); }
  };

  return (
    <div className="shell">
      <aside className="sidebar" aria-label="Primary navigation">
        <div className="brand"><span className="brand-mark"><GatewayMark size={18} /></span><span>ai-gateway</span></div>
        <nav>
          {navigation.map(({ id, icon: Icon }) => <button key={id} ref={page === id ? activeNavRef : undefined} className={page === id ? "nav-item active" : "nav-item"} onClick={() => setPage(id)} aria-label={t(id)} title={t(id)} aria-current={page === id ? "page" : undefined}><Icon size={17} /><span>{t(id)}</span></button>)}
        </nav>
        <div className="sidebar-status"><span className={status ? "status-dot ok" : "status-dot"} />{status ? t("connected") : t("loading")}</div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div className="topbar-inner">
            <div><p className="eyebrow">AI GATEWAY / {t(page).toUpperCase()}</p><h1>{t(page)}</h1></div>
            <button className="icon-button" onClick={() => void refresh()} title={t("refresh")} aria-label={t("refresh")} disabled={busy}><RefreshCw size={17} className={busy ? "spin" : ""} /></button>
          </div>
        </header>
        {busy && !status ? <div className="loading"><RefreshCw className="spin" size={18} />{t("loading")}</div> : (
          <div className="content">
            {page === "overview" && <Overview status={status} usage={usage} t={t} />}
            {page === "localAccess" && localAccess && <LocalAccessPage access={localAccess} t={t} notify={pushToast} />}
            {page === "providers" && <Providers providers={providers} t={t} run={run} notify={pushToast} />}
            {page === "routes" && status && <Routes status={status} providers={providers} t={t} run={run} />}
            {page === "clients" && <Clients clients={clients} t={t} run={run} />}
            {page === "logs" && config && <Logs items={logs} hasMore={Boolean(logCursor)} loadMore={loadMoreLogs} enabled={config.logging.enabled} body={config.logging.body !== false} redact={config.logging.redact !== false} t={t} run={run} notify={pushToast} />}
            {page === "usage" && <Usage usage={usage} providers={providers} t={t} onQuery={queryUsage} />}
            {page === "settings" && config && <SettingsPage config={config} language={language} theme={theme} setLanguage={setLanguage} setTheme={setTheme} t={t} run={run} />}
          </div>
        )}
      </main>
      <ToastViewport toasts={toasts} onDismiss={(id) => setToasts((current) => current.filter((toast) => toast.id !== id))} t={t} />
      {config && !config.ui.logging_notice_accepted && <RiskModal t={t} onAccept={() => void acceptRisk()} />}
    </div>
  );
}

function ToastViewport({ toasts, onDismiss, t }: { toasts: Toast[]; onDismiss: (id: number) => void; t: (key: MessageKey) => string }) {
  return <div className="toast-viewport" aria-label={t("systemNotifications")}>
    {toasts.map((toast) => <div className={`toast ${toast.kind}`} key={toast.id} role={toast.kind === "error" ? "alert" : "status"}>
      {toast.kind === "error" ? <CircleAlert size={17} aria-hidden="true" /> : <Check size={17} aria-hidden="true" />}
      <span>{toast.message}</span>
      <button className="toast-close" onClick={() => onDismiss(toast.id)} aria-label={t("close")}><X size={16} /></button>
    </div>)}
  </div>;
}

function SectionHeader({ title, description, action, hideTitle = false }: { title: string; description?: string; action?: React.ReactNode; hideTitle?: boolean }) {
  if (hideTitle && !description && !action) return null;
  return <div className={hideTitle ? "section-header title-hidden" : "section-header"}><div className="section-copy">{!hideTitle && <h2>{title}</h2>}{description && <p>{description}</p>}</div>{action && <div className="section-actions">{action}</div>}</div>;
}

function State({ value }: { value: string }) {
  const good = ["pointed", "success", "ok", "enabled"].includes(value);
  const warn = ["drifted", "interrupted", "incomplete"].includes(value);
  const stateKeys: Record<string, MessageKey> = { pointed: "pointed", not_pointed: "notPointed", drifted: "drifted", client_not_installed: "clientNotInstalled", unknown: "unknown", "key ready": "keyReady", keyless: "keyless", api: "api", ok: "ok", success: "success", failed: "failed", cancelled: "cancelled", interrupted: "interrupted", incomplete: "incomplete", enabled: "enabled", disabled: "disabled" };
  const stateLanguage: Language = document.documentElement.lang === "en-US" ? "en-US" : "zh-CN";
  const label = stateKeys[value] ? translator(stateLanguage)(stateKeys[value]) : value.replaceAll("_", " ");
  return <span className={`state ${good ? "good" : warn ? "warn" : "neutral"}`}><span />{label}</span>;
}

function Overview({ status, usage, t }: { status: Status | null; usage: UsageReport | null; t: (key: MessageKey) => string }) {
  if (!status) return null;
  const tokens = usage?.total.usage?.total_tokens || 0;
  return <>
    <section><SectionHeader title={t("runtime")} description={`${status.listen} · PID ${status.pid} · ${status.version}`} />
      <div className="metric-grid overview-metrics">
        <Metric label={t("status")} value={t("connected")} note="127.0.0.1 only" icon={Server} />
        <Metric label={t("requests")} value={String(usage?.total.requests || 0)} note={usage?.total.incomplete ? t("incomplete") : t("success")} icon={Activity} />
        <Metric label={t("tokens")} value={tokens.toLocaleString()} note={`${usage?.total.success || 0} ${t("success")}`} icon={Database} />
      </div>
      <div className="rows overview-statuses"><div className="data-row"><strong>{t("logging")}</strong><State value={status.logging_enabled ? "enabled" : "disabled"} /></div><div className="data-row"><strong>{t("bodyLogging")}</strong><State value={status.logging_enabled && status.logging_body_enabled !== false ? "enabled" : "disabled"} /></div><div className="data-row"><strong>{t("autostart")}</strong><State value={status.autostart_enabled ? "enabled" : "disabled"} /></div></div>
    </section>
    <section><SectionHeader title={t("clientRoutes")} />
      <div className="rows overview-routes">{allClients.map((client) => <div className="data-row" key={client}><strong className="mono">{client}</strong><span className="mono">{catalogId(status.routes[client])}</span>{client === "generic" ? <State value="api" /> : <State value={status.clients[client].point_state} />}</div>)}</div>
    </section>
  </>;
}

function LocalAccessPage({ access, t, notify }: { access: LocalAccess; t: (key: MessageKey) => string; notify: (kind: ToastKind, message: string) => void }) {
  const copyValue = async (value: string) => {
    try { await navigator.clipboard.writeText(value); notify("success", t("copied")); }
    catch { notify("error", t("copyFailed")); }
  };
  const endpoints = [
    [t("modelDiscovery"), access.endpoints.models],
    ["OpenAI Chat Completions", access.endpoints.chat_completions],
    ["OpenAI Responses", access.endpoints.responses],
    ["Anthropic Messages", access.endpoints.messages],
  ] as const;
  return <>
    <section>
      <SectionHeader title={t("connectionParameters")} description={t("localAccessDescription")} />
      <dl className="access-parameters compact-parameters">
        <AccessValue label={t("baseURL")} value={access.base_url} onCopy={copyValue} t={t} />
        <AccessValue label={t("apiKeyPlaceholder")} value={access.api_key} note={access.auth_required ? t("authenticationRequired") : t("authenticationOptional")} onCopy={copyValue} t={t} />
        <AccessValue label={t("defaultModel")} value={access.default_model} note={`${t("defaultRoute")}: ${catalogId(access.default_route)}`} onCopy={copyValue} t={t} />
      </dl>
    </section>
    <section>
      <SectionHeader title={t("protocolEndpoints")} />
      <div className="table-wrap"><table className="access-table"><thead><tr><th>{t("protocol")}</th><th>{t("endpoint")}</th><th className="actions">{t("actions")}</th></tr></thead><tbody>
        {endpoints.map(([name, endpoint]) => <tr key={name}><td><strong>{name}</strong></td><td className="mono">{endpoint}</td><td className="actions"><button className="icon-button compact" type="button" title={`${t("copy")} ${name}`} aria-label={`${t("copy")} ${name}`} onClick={() => void copyValue(endpoint)}><Copy size={14} /></button></td></tr>)}
      </tbody></table></div>
    </section>
    <section>
      <SectionHeader title={t("enabledModels")} description={`${access.models.length} ${t("models")}`} />
      <div className="table-wrap"><table className="access-table"><thead><tr><th>{t("modelID")}</th><th>{t("owner")}</th><th className="actions">{t("actions")}</th></tr></thead><tbody>
        {access.models.map((model) => <tr key={model.id}><td className="mono"><strong>{model.id}</strong></td><td className="mono">{model.owned_by}</td><td className="actions"><button className="icon-button compact" type="button" title={`${t("copy")} ${model.id}`} aria-label={`${t("copy")} ${model.id}`} onClick={() => void copyValue(model.id)}><Copy size={14} /></button></td></tr>)}
      </tbody></table></div>
    </section>
  </>;
}

function AccessValue({ label, value, note, onCopy, t }: { label: string; value: string; note?: string; onCopy: (value: string) => Promise<void>; t: (key: MessageKey) => string }) {
  return <div><dt>{label}</dt><dd><span><code>{value}</code>{note && <small>{note}</small>}</span><button className="icon-button compact" type="button" title={`${t("copy")} ${label}`} aria-label={`${t("copy")} ${label}`} onClick={() => void onCopy(value)}><Copy size={14} /></button></dd></div>;
}

function Metric({ label, value, note, icon: Icon }: { label: string; value: string; note: string; icon: typeof Activity }) {
  return <div className="metric"><div className="metric-label"><Icon size={16} />{label}</div><strong>{value}</strong><span>{note}</span></div>;
}

function Providers({ providers, t, run, notify }: { providers: Provider[]; t: (key: MessageKey) => string; run: RunOperation; notify: (kind: ToastKind, message: string) => void }) {
  const [open, setOpen] = useState(false); const [editing, setEditing] = useState<string>(); const [form, setForm] = useState(emptyProvider); const [errors, setErrors] = useState<Record<string, string>>({}); const [fetching, setFetching] = useState(false);
  const [probe, setProbe] = useState<{ provider: string; result: { ok: boolean; status: number; latency_ms: number; models?: number; error?: string; response?: string } } | null>(null);
  const closeProbe = useCallback(() => setProbe(null), []);
  const probeDialogRef = useDialogFocus<HTMLDivElement>(Boolean(probe), closeProbe);
  const edit = (p: Provider) => {
    const models = (p.models?.length ? p.models : [{ id: p.default_model, name: "" }]).map((model) => ({ id: model.id, name: model.name, adapter: modelAdapter(model, p.adapter), endpoint: model.endpoint, enabled: model.enabled }));
    const extra_headers = Object.entries(p.extra_headers || {}).sort(([left], [right]) => left.localeCompare(right)).map(([name, value]) => ({ name, value }));
    const disguise_client: DisguiseClient = p.disguise_client === "claude" || p.disguise_client === "codex" ? p.disguise_client : "";
    setEditing(p.id); setForm({ id: p.id, name: p.name, adapter: p.adapter || "openai-chat", base_url: p.base_url, models_url: p.models_url || "", extra_headers, disguise_client, default_model: p.default_model, models, api_key: "" }); setErrors({}); setOpen(true);
  };
  const updateHeader = (index: number, patch: Partial<ProviderFormValue["extra_headers"][number]>) => setForm((current) => ({ ...current, extra_headers: current.extra_headers.map((header, headerIndex) => headerIndex === index ? { ...header, ...patch } : header) }));
  const removeHeader = (index: number) => setForm((current) => ({ ...current, extra_headers: current.extra_headers.filter((_, headerIndex) => headerIndex !== index) }));
  const updateModel = (index: number, patch: Partial<ProviderModel>) => setForm((current) => ({ ...current, models: current.models.map((model, modelIndex) => modelIndex === index ? { ...model, ...patch } : model) }));
  const changeModelProtocol = (index: number, next: string) => setForm((current) => ({
    ...current,
    models: current.models.map((model, modelIndex) => {
      if (modelIndex !== index) return model;
      if (next === "custom") {
        const previous = modelAdapter(model, current.adapter);
        return { ...model, adapter: "custom", endpoint: model.endpoint || presetPath(previous === "custom" ? "openai-chat" : previous) };
      }
      return { ...model, adapter: next, endpoint: undefined };
    }),
  }));
  const removeModel = (index: number) => setForm((current) => {
    const removed = current.models[index]; const models = current.models.filter((_, modelIndex) => modelIndex !== index);
    const default_model = current.default_model === removed.id ? (models[0]?.id || "") : current.default_model;
    return { ...current, models, default_model };
  });
  const addModel = () => setForm((current) => ({ ...current, models: [...current.models, emptyModel(current.adapter)] }));
  const fetchModels = async () => {
    const adapter = discoveryAdapter(form);
    const discoveryErrors = validateProvider({ ...form, adapter, name: "discovery", default_model: "discovery", models: [] }, false);
    const nextErrors = Object.fromEntries(Object.entries(discoveryErrors).filter(([field]) => ["id", "base_url", "models_url"].includes(field) || field.startsWith("extra_headers.")));
    if (Object.keys(nextErrors).length) { setErrors(nextErrors); notify("error", t("fetchModelsHint")); return; }
    setFetching(true);
    try {
      const result = await api.discoverProviderModels({ provider_id: form.id, adapter, base_url: form.base_url, models_url: form.models_url || undefined, extra_headers: headerRecord(form.extra_headers), api_key: form.api_key || undefined });
      setForm((current) => {
        const existing = new Map(current.models.filter((model) => model.id.trim()).map((model) => [model.id, model]));
        const fallback = discoveryAdapter(current);
        const fetched = result.data.map((model) => {
          const saved = existing.get(model.raw_id); existing.delete(model.raw_id);
          return { id: model.raw_id, name: saved?.name || model.display_name || "", adapter: modelAdapter(saved, fallback), endpoint: saved?.endpoint, enabled: saved?.enabled };
        });
        const models = [...fetched, ...existing.values()];
        if (current.default_model && !models.some((model) => model.id === current.default_model)) models.push({ ...emptyModel(current.adapter), id: current.default_model });
        return { ...current, models, default_model: current.default_model || models[0]?.id || "" };
      });
      setErrors({}); notify("success", `${result.data.length}${t("modelsFound")}`);
    } catch (reason) { notify("error", reason instanceof Error ? reason.message : String(reason)); }
    finally { setFetching(false); }
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const adapter = discoveryAdapter(form);
    const nextErrors = validateProvider({ ...form, adapter }, Boolean(editing)); setErrors(nextErrors); if (Object.keys(nextErrors).length) return;
    await run(() => api.saveProvider({ ...(editing ? {} : { id: form.id }), name: form.name, adapter, base_url: form.base_url, models_url: form.models_url?.trim() || undefined, extra_headers: headerRecord(form.extra_headers), disguise_client: form.disguise_client, default_model: form.default_model, models: form.models.map((model) => { const nextAdapter = modelAdapter(model, adapter); return { id: model.id.trim(), name: model.name?.trim() || undefined, adapter: nextAdapter, endpoint: nextAdapter === "custom" ? model.endpoint?.trim() || undefined : undefined, enabled: model.enabled }; }), api_key: form.api_key || undefined, capabilities: { image_input: true, reasoning: true, context_management: false } }, editing), t("success"), ["providers", "status", "config"]);
  setOpen(false); setEditing(undefined); setForm(emptyProvider);
  };
  const probeProvider = async (provider: Provider) => {
    try {
      const result = await api.probeProvider(provider.id);
      setProbe({ provider: provider.name, result });
      if (!result.ok) notify("error", result.error || t("probeFailed"));
    } catch (reason) { notify("error", reason instanceof Error ? reason.message : String(reason)); }
  };
  if (open) return <section className="provider-editor-page">
    <SectionHeader title={`${editing ? t("edit") : t("addProvider")}${editing ? ` · ${editing}` : ""}`} description={t("providerEditorDescription")} />
    <form id="provider-form" className="form-panel provider-form" onSubmit={submit} noValidate>
      <div className="form-actions editor-actions"><button type="button" className="secondary" onClick={() => setOpen(false)}>{t("cancel")}</button><button className="primary" type="submit"><Save size={16} />{t("save")}</button></div>
      <div className="form-grid">
      <Field label={t("identifier")} error={errors.id} className="field-id"><input value={form.id} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, id: e.target.value })} /></Field>
      <Field label={t("name")} error={errors.name} className="field-name"><input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
      <Field label={t("baseURL")} error={errors.base_url} className="field-base-url"><input type="url" value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} /></Field>
      <Field label={t("modelsURL")} error={errors.models_url} className="field-models-url"><input type="url" value={form.models_url} placeholder={t("modelsURLPlaceholder")} onChange={(e) => setForm({ ...form, models_url: e.target.value })} /></Field>
      <Field label={t("apiKey")} className="field-api-key"><input type="password" autoComplete="new-password" value={form.api_key} onChange={(e) => setForm({ ...form, api_key: e.target.value })} placeholder={editing ? t("keepKey") : "sk-…"} /></Field>
    </div>
    <div className="header-editor disguise-editor"><div className="header-editor-title"><div><h3>{t("disguiseClient")}</h3><p>{t("disguiseClientDescription")}</p></div>
      <label className="field disguise-select"><span>{t("disguiseClient")}</span><select aria-label={t("disguiseClient")} value={form.disguise_client} onChange={(event) => setForm({ ...form, disguise_client: event.target.value as DisguiseClient })}><option value="">{t("disguiseClientOff")}</option><option value="claude">{t("disguiseClientClaude")}</option><option value="codex">{t("disguiseClientCodex")}</option></select></label>
    </div></div>
    <div className="header-editor"><div className="header-editor-title"><div><h3>{t("customHeaders")}</h3><p>{t("customHeadersDescription")}</p></div><div className="header-editor-actions"><button type="button" className="secondary" onClick={() => setForm((current) => ({ ...current, extra_headers: mergeHeaderPreset(current.extra_headers, CLAUDE_CODE_HEADERS) }))}><Plus size={15} />{t("applyPreset")} Claude Code</button><button type="button" className="secondary" onClick={() => setForm((current) => ({ ...current, extra_headers: mergeHeaderPreset(current.extra_headers, CODEX_HEADERS) }))}><Plus size={15} />{t("applyPreset")} Codex</button><button type="button" className="secondary" onClick={() => setForm((current) => ({ ...current, extra_headers: [...current.extra_headers, { name: "", value: "" }] }))}><Plus size={15} />{t("addHeader")}</button></div></div>
      {form.extra_headers.length === 0 ? <div className="header-empty">{t("noCustomHeaders")}</div> : <div className="header-rows" role="table" aria-label={t("customHeaders")}><div className="header-row header-row-head" role="row"><span>{t("headerName")}</span><span>{t("headerValue")}</span><span /></div>{form.extra_headers.map((header, index) => <div className="header-row" role="row" key={`header-row-${index}`}><label><span>{t("headerName")}</span><input className="mono" value={header.name} onChange={(event) => updateHeader(index, { name: event.target.value })} aria-invalid={Boolean(errors[`extra_headers.${index}.name`])} />{errors[`extra_headers.${index}.name`] && <small className="field-error">{errors[`extra_headers.${index}.name`].replaceAll("_", " ")}</small>}</label><label><span>{t("headerValue")}</span><input className="mono" value={header.value} onChange={(event) => updateHeader(index, { value: event.target.value })} aria-invalid={Boolean(errors[`extra_headers.${index}.value`])} />{errors[`extra_headers.${index}.value`] && <small className="field-error">{errors[`extra_headers.${index}.value`].replaceAll("_", " ")}</small>}</label><button type="button" className="icon-button compact danger" onClick={() => removeHeader(index)} title={t("removeHeader")} aria-label={`${t("removeHeader")} ${header.name || index + 1}`}><Trash2 size={15} /></button></div>)}</div>}
    </div><div className="model-catalog"><div className="model-catalog-header"><div><h3>{t("modelCatalog")}</h3><p>{t("modelCatalogDescription")}</p><p className="catalog-hint">{t("claude1mHint")}</p></div><button type="button" className="secondary" onClick={() => void fetchModels()} disabled={fetching}><RefreshCw size={15} className={fetching ? "spin" : ""} />{fetching ? t("fetchingModels") : t("fetchModels")}</button></div>
      {errors.default_model && <small className="field-error catalog-error">{errors.default_model.replaceAll("_", " ")}</small>}
      {form.models.length === 0 ? <div className="model-empty">{t("noModels")}</div> : <div className="model-editor" role="table" aria-label={t("modelCatalog")}><div className="model-editor-head" role="row"><span>{t("defaultModel")}</span><span>{t("modelID")}</span><span>{t("modelProtocol")}</span><span>{t("requestEndpoint")}</span><span>{t("displayName")}</span><span /></div>{form.models.map((model, index) => <div className="model-editor-row" role="row" key={`model-row-${index}`}><label className="default-radio" title={t("defaultModel")}><input type="radio" name="default-model" checked={Boolean(model.id) && form.default_model === model.id} onChange={() => setForm({ ...form, default_model: model.id })} /><span>{t("defaultModel")}</span></label><label><span>{t("modelID")}</span><input className="mono" value={model.id} onChange={(e) => { const value = e.target.value; setForm((current) => { const previous = current.models[index]?.id; const models = current.models.map((item, itemIndex) => itemIndex === index ? { ...item, id: value } : item); return { ...current, models, default_model: current.default_model === previous ? value : current.default_model }; }); }} aria-invalid={Boolean(errors[`models.${index}.id`])} />{errors[`models.${index}.id`] && <small className="field-error">{errors[`models.${index}.id`].replaceAll("_", " ")}</small>}</label><label><span>{t("modelProtocol")}</span><select className="mono" value={modelAdapter(model, form.adapter)} onChange={(e) => changeModelProtocol(index, e.target.value)} aria-invalid={Boolean(errors[`models.${index}.adapter`])} aria-label={`${t("modelProtocol")} ${model.id || index + 1}`}>{modelAdapters.map((adapter) => <option key={adapter} value={adapter}>{protocolLabel(adapter, t)}</option>)}</select>{errors[`models.${index}.adapter`] && <small className="field-error">{errors[`models.${index}.adapter`].replaceAll("_", " ")}</small>}</label><label className="endpoint-field"><span>{t("requestEndpoint")}</span><input className="mono" value={displayedEndpoint(model, form.adapter)} readOnly={modelAdapter(model, form.adapter) !== "custom"} placeholder="/v1/chat/completions" onChange={(e) => updateModel(index, { endpoint: e.target.value })} aria-invalid={Boolean(errors[`models.${index}.endpoint`])} aria-label={`${t("requestEndpoint")} ${model.id || index + 1}`} />{errors[`models.${index}.endpoint`] && <small className="field-error">{errors[`models.${index}.endpoint`].replaceAll("_", " ")}</small>}<small className="endpoint-preview">{requestURLPreview(form.base_url, model, form.adapter) || t("requestURLPending")}</small></label><label><span>{t("displayName")}</span><input value={model.name || ""} onChange={(e) => updateModel(index, { name: e.target.value })} /></label><button type="button" className="icon-button compact danger" onClick={() => removeModel(index)} title={t("removeModel")} aria-label={`${t("removeModel")} ${model.id || index + 1}`}><Trash2 size={15} /></button></div>)}</div>}
      <button type="button" className="text-button add-model" onClick={addModel}><Plus size={15} />{t("addModel")}</button></div>
    </form>
  </section>;

  return <section><SectionHeader title={t("providers")} hideTitle description={`${providers.length}${t("configured")}`} action={<button className="primary" onClick={() => { setOpen(true); setEditing(undefined); setForm(emptyProvider); setErrors({}); }}><Plus size={16} />{t("addProvider")}</button>} />
    {providers.length === 0 ? <Empty text={t("noProviders")} /> : <div className="table-wrap providers-table-wrap"><table className="providers-table"><thead><tr><th>{t("provider")}</th><th>{t("adapter")}</th><th>{t("model")}</th><th>{t("modelCount")}</th><th>{t("status")}</th><th className="actions">{t("actions")}</th></tr></thead><tbody>{providers.map((p) => <tr key={p.id}><td><strong>{p.name}</strong><small>{p.id} · {p.base_url}</small></td><td className="mono" data-label={t("adapter")}>{providerAdapters(p)}</td><td className="mono" data-label={t("model")}>{p.default_model}</td><td className="mono" data-label={t("modelCount")}>{p.models?.length || 0}</td><td><State value={p.enabled === false ? "disabled" : p.has_secret ? "key ready" : "keyless"} /></td><td className="actions"><div className="action-cluster"><button className="text-button" onClick={() => void probeProvider(p)}>{t("probe")}</button><button className="icon-button compact" onClick={() => edit(p)} title={t("edit")} aria-label={`${t("edit")} ${p.name}`}><Settings size={15} /></button><button className="icon-button compact danger" onClick={() => { if (confirm(t("confirmDelete"))) void run(() => api.deleteProvider(p.id), undefined, ["providers", "status", "config"]); }} title={t("remove")} aria-label={`${t("remove")} ${p.name}`}><Trash2 size={15} /></button></div></td></tr>)}</tbody></table></div>}
    {probe && <div className="modal-backdrop" onMouseDown={closeProbe}><div ref={probeDialogRef} className="modal probe-modal" role="dialog" aria-modal="true" aria-labelledby="probe-title" onMouseDown={(event) => event.stopPropagation()}><div className="drawer-title"><div><p className="eyebrow">{t("probeResponse")}</p><h2 id="probe-title">{probe.provider}</h2></div><button className="icon-button" onClick={closeProbe} aria-label={t("close")}><X size={17} /></button></div><div className="probe-meta"><State value={probe.result.ok ? "ok" : "failed"} /><span>{probe.result.status || "-"} · {probe.result.latency_ms} ms · {probe.result.models || 0} {t("models")}</span></div>{probe.result.error && <p className="field-error">{probe.result.error}</p>}<pre>{formatProbeResponse(probe.result.response) || t("noProbeResponse")}</pre></div></div>}
  </section>;
}

function formatProbeResponse(value?: string): string {
  if (!value) return "";
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value.trim();
  }
}

function Field({ label, error, wide, className, children }: { label: string; error?: string; wide?: boolean; className?: string; children: React.ReactNode }) { return <label className={["field", wide ? "wide" : "", className || ""].filter(Boolean).join(" ")}><span>{label}</span>{children}{error && <small className="field-error">{error.replaceAll("_", " ")}</small>}</label>; }

function Routes({ status, providers, t, run }: { status: Status; providers: Provider[]; t: (key: MessageKey) => string; run: RunOperation }) {
  const catalog = useMemo(() => enabledCatalog(providers), [providers]);
  const [override, setOverride] = useState<Partial<Record<ClientID, Route>>>({});
  const draft = useMemo(() => reconcileClientRoutes(override, status.routes, catalog), [override, status.routes, catalog]);
  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(() => new Set());
  const toggleProvider = (provider: Provider, enabled: boolean) => run(() => api.updateProviderAvailability(provider.id, { enabled }), t("success"), ["providers", "status"]);
  const toggleModel = (provider: Provider, model: ProviderModel, enabled: boolean) => run(() => api.updateProviderAvailability(provider.id, { models: { [model.id]: enabled } }), t("success"), ["providers", "status"]);
  const toggleExpanded = (id: string) => setExpandedProviders((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  const selectRoute = (client: ClientID, catalogIdValue: string) => {
    const next = catalog.find((item) => item.id === catalogIdValue);
    if (!next) return;
    setOverride((current) => ({ ...current, [client]: { provider: next.provider, model: next.model } }));
  };
  const applyRoute = (client: ClientID) => {
    const next = draft[client];
    if (!isCatalogRoute(next, catalog)) return;
    void run(() => api.updateRoute(client, next), t("success"), ["status", "config"]);
  };
  return <section><SectionHeader title={t("routes")} hideTitle />
      <div className="route-clients primary-route-block"><h3>{t("clientRoutes")}</h3><p className="muted">{t("clientRoutesDescription")}</p><div className="route-list route-client-grid">{allClients.map((client) => {
      const currentKnown = isCatalogRoute(draft[client], catalog);
      const currentId = currentKnown ? catalogId(draft[client]) : "";
      const savedId = catalogId(status.routes[client]);
      const canApply = currentKnown && currentId !== savedId;
      return <div className="route-row" key={client}><div><strong className="mono">{client}</strong><small>/c/{client}/v1</small></div><label><span>{t("defaultSelectedModel")}</span><select className="mono" required aria-required="true" aria-invalid={!currentKnown} aria-label={`${client} ${t("defaultSelectedModel")}`} title={currentId} value={currentId} onChange={(event) => selectRoute(client, event.target.value)}><option value="" disabled>{t("selectDefaultModel")}</option>{catalog.map((item) => <option key={item.id} value={item.id}>{item.id}</option>)}</select>{currentId && <small className="route-selected-value mono">{t("fullValue")}: {currentId}</small>}{!currentKnown && <small className="field-error">{t("routeUnavailable")}</small>}</label><button className="secondary" disabled={!canApply} onClick={() => applyRoute(client)}><Check size={16} />{t("apply")}</button></div>;
    })}</div></div>
    <div className="route-catalog-header"><div><h3>{t("routeCatalog")}</h3><p className="muted">{t("availabilityDescription")}</p></div><span className="mono muted">{providers.length} {t("providers")}</span></div>
    <div className="route-tree" aria-label={t("routeCatalog")}>{providers.map((provider) => { const treeModels = provider.models?.length ? provider.models : [{ id: provider.default_model, name: "" }]; const expanded = expandedProviders.has(provider.id); const enabledCount = treeModels.filter((model) => model.enabled !== false).length; return <div className="tree-provider" key={provider.id}><div className="tree-provider-row"><button className="tree-provider-toggle" onClick={() => toggleExpanded(provider.id)} aria-expanded={expanded} aria-label={`${provider.name} ${expanded ? t("hideModels") : t("showModels")}`}><ChevronRight className={expanded ? "expanded" : ""} size={15} /><span><b>{provider.name}</b><small>{enabledCount}/{treeModels.length} {t("models")}</small></span></button><span className="mono muted">{provider.id}</span><label className="switch"><input type="checkbox" checked={provider.enabled !== false} onChange={(event) => toggleProvider(provider, event.target.checked)} aria-label={`${t("provider")} ${provider.name}`} /><span /><b>{provider.enabled === false ? t("disabled") : t("enabled")}</b></label></div>{expanded && <div className="tree-models">{treeModels.map((model) => <div className="tree-model-row" key={model.id}><span className="tree-branch" aria-hidden="true"><ChevronRight size={14} /></span><span className="mono">{`${provider.id}/${model.id}`}</span><label className="switch"><input type="checkbox" checked={provider.enabled !== false && model.enabled !== false} disabled={provider.enabled === false} onChange={(event) => toggleModel(provider, model, event.target.checked)} aria-label={`${provider.id}/${model.id} ${t("enabled")}`} /><span /><b>{model.enabled === false ? t("disabled") : t("enabled")}</b></label></div>)}</div>}</div>; })}</div>
  </section>;
}

function Clients({ clients, t, run }: { clients: Record<string, PointStatus>; t: (key: MessageKey) => string; run: RunOperation }) {
  return <section><SectionHeader title={t("clients")} hideTitle description={t("clientsDescription")} /><div className="client-list">{pointClients.map((client) => { const value = clients[client]; return <article className="client-item" key={client}><div className="client-main"><div className="client-title"><div className="client-icon"><ClientsIcon size={18} /></div><div><h3>{client}</h3><State value={value?.point_state || "unknown"} /></div></div><dl><dt>{t("target")}</dt><dd className="mono">{value?.target || "—"}</dd></dl><div className="client-actions"><button className="primary" disabled={value?.point_state === "pointed" || value?.point_state === "client_not_installed"} onClick={() => { if (confirm(t("confirmPoint"))) void run(() => api.point(client), t("success"), ["clients", "status"]); }}><Cable size={16} />{t("point")}</button><button className="secondary" disabled={!value?.backup_available} onClick={() => { if (confirm(t("confirmRestore"))) void run(() => api.restore(client), t("success"), ["clients", "status"]); }}><RotateCcw size={16} />{t("restore")}</button></div></div>{value?.message && <p className="client-message muted">{value.message}</p>}{client === "codex" && <details className="client-advanced" open><summary>{t("advancedSettings")}</summary><div className="client-option"><label className="switch"><input type="checkbox" checked={Boolean(value?.remote_compaction)} onChange={(event) => void run(() => api.setCodexRemoteCompaction(event.target.checked), t("success"), ["clients", "status"])} aria-label={t("remoteCompaction")} /><span /><b>{t("remoteCompaction")}</b></label><p className="muted">{t("remoteCompactionHint")}</p></div></details>}</article>; })}</div></section>;
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value; textarea.setAttribute("readonly", ""); textarea.style.position = "fixed"; textarea.style.opacity = "0";
  document.body.appendChild(textarea); textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("Clipboard is unavailable");
}

function Logs({ items, hasMore, loadMore, enabled, body, redact, t, run, notify }: { items: LogSummary[]; hasMore: boolean; loadMore: () => Promise<void>; enabled: boolean; body: boolean; redact: boolean; t: (key: MessageKey) => string; run: RunOperation; notify: (kind: ToastKind, message: string) => void }) {
  const [detail, setDetail] = useState<{ id: string; events: unknown[] } | null>(null);
  const [detailCount, setDetailCount] = useState(100);
  const [copied, setCopied] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const closeDetail = useCallback(() => setDetail(null), []);
  const detailDialogRef = useDialogFocus<HTMLElement>(Boolean(detail), closeDetail);
  const copyLog = async (id: string, events?: unknown[]) => {
    try {
      void events;
      await copyText(await api.logExport(id));
      notify("success", t("copied"));
      return true;
    } catch { notify("error", t("copyFailed")); return false; }
  };
  const copyDetail = async () => {
    if (!detail || !await copyLog(detail.id, detail.events)) return;
    setCopied(true); window.setTimeout(() => setCopied(false), 1800);
  };
  const onLoadMore = async () => {
    setLoadingMore(true);
    try { await loadMore(); } finally { setLoadingMore(false); }
  };
  const downloadLog = async (id: string) => {
    try {
      const value = await api.logExport(id);
      const url = URL.createObjectURL(new Blob([value], { type: "application/x-ndjson" }));
      const link = document.createElement("a");
      link.href = url; link.download = `${id}.redacted.jsonl`; link.click();
      URL.revokeObjectURL(url);
    } catch (reason) { notify("error", reason instanceof Error ? reason.message : String(reason)); }
  };
  const deleteLog = (id: string) => {
    if (!window.confirm(t("confirmDeleteLog"))) return;
    void run(() => api.deleteLog(id), t("logDeleted"), ["logs", "usage"]);
  };
  const clearLogs = () => {
    if (!window.confirm(t("confirmClearLogs"))) return;
    void run(() => api.clearLogs(), t("logsCleared"), ["logs", "usage"]);
  };
  return <section><div className="log-toolbar"><SectionHeader title={t("requestLog")} hideTitle description={`${enabled ? (body ? t("loggingWithBody") : t("loggingMetadataOnly")) : t("disabled")} · ${items.length} ${t(items.length === 1 ? "requestEntry" : "requestEntries")}`} action={<div className="header-switches"><label className="switch"><input type="checkbox" checked={enabled} onChange={(e) => void run(() => api.setLogging(e.target.checked), undefined, ["config", "status", "logs"])} aria-label={t("logging")} /><span /><b>{t("logging")}</b></label><label className="switch"><input type="checkbox" checked={enabled && body} disabled={!enabled} onChange={(e) => void run(() => api.setLoggingBody(e.target.checked), undefined, ["config", "status", "logs"])} aria-label={t("bodyLogging")} /><span /><b>{t("bodyLogging")}</b></label><label className="switch"><input type="checkbox" checked={redact} onChange={(e) => void run(() => api.setLoggingRedact(e.target.checked), undefined, ["config", "logs"])} aria-label={t("logRedaction")} /><span /><b>{t("logRedaction")}</b></label><button className="icon-button" disabled={!items.length} onClick={clearLogs} title={t("clearLogs")} aria-label={t("clearLogs")}><Trash2 size={16} /></button></div>} /></div>
    {!enabled && <div className="empty-state inline"><CircleAlert size={18} /><span>{t("disabled")}</span></div>}
    {items.length === 0 ? <Empty text={t("noLogs")} /> : <><div className="table-wrap log-scroll"><table className="log-table"><thead><tr><th>{t("status")}</th><th>{t("route")}</th><th>{t("clients")}</th><th>{t("requests")}</th><th /></tr></thead><tbody>{items.map((item) => <tr key={item.request_id}><td className="log-status"><State value={item.status} /></td><td className="log-route"><strong>{item.provider || "—"}</strong><small className="mono">{item.model || "—"}</small></td><td className="log-client mono">{item.client || "generic"}</td><td className="log-request"><time className="mono" dateTime={item.started_at} title={item.started_at}>{new Date(item.started_at).toLocaleString(undefined, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><small>{item.duration_ms || 0} ms · {item.status_code || "—"}</small></td><td className="actions log-actions"><div className="action-cluster"><button className="icon-button compact" onClick={() => void copyLog(item.request_id)} title={t("copyRequestLog")} aria-label={`${t("copyRequestLog")} ${item.request_id}`}><Copy size={15} /></button><button className="icon-button compact" onClick={() => void downloadLog(item.request_id)} title={t("exportRedactedLog")} aria-label={`${t("exportRedactedLog")} ${item.request_id}`}><Download size={15} /></button><button className="icon-button compact danger-icon" onClick={() => deleteLog(item.request_id)} title={t("deleteLog")} aria-label={`${t("deleteLog")} ${item.request_id}`}><Trash2 size={15} /></button><button className="icon-button compact" onClick={() => void run(async () => { const value = await api.logDetail(item.request_id); setDetailCount(100); setDetail({ id: value.request_id, events: value.events }); })} title={t("details")} aria-label={t("details")}><ChevronRight size={16} /></button></div></td></tr>)}</tbody></table></div>{hasMore && <div className="load-more"><button className="secondary" disabled={loadingMore} onClick={() => void onLoadMore()}>{loadingMore && <RefreshCw size={15} className="spin" />}{loadingMore ? t("loadingMore") : t("loadMore")}</button></div>}</>}
    {detail && <div className="drawer-backdrop" onMouseDown={closeDetail}><aside ref={detailDialogRef} className="drawer" role="dialog" aria-modal="true" aria-labelledby="log-detail-title" onMouseDown={(e) => e.stopPropagation()}><div className="drawer-title"><div><p className="eyebrow">REQUEST</p><h2 id="log-detail-title" className="mono">{detail.id}</h2></div><div className="drawer-title-actions"><span className="privacy-state"><ShieldCheck size={14} />{t("redactedExport")}</span><button className="secondary copy-button" onClick={() => void copyDetail()}><Copy size={15} />{copied ? t("copied") : t("copyRequestLog")}</button><button className="icon-button" onClick={closeDetail} aria-label={t("close")}><X size={17} /></button></div></div><pre>{JSON.stringify({ request_id: detail.id, events: detail.events.slice(0, detailCount) }, null, 2)}</pre>{detailCount < detail.events.length && <div className="load-more"><button className="secondary" onClick={() => setDetailCount((count) => Math.min(count + 100, detail.events.length))}>{t("loadMoreEvents")}</button></div>}</aside></div>}
  </section>;
}

function Usage({ usage, providers, t, onQuery }: { usage: UsageReport | null; providers: Provider[]; t: (key: MessageKey) => string; onQuery: (query: UsageQuery) => Promise<void> }) {
  const [range, setRange] = useState<"7d" | "30d" | "all">("all");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [client, setClient] = useState("");
  const [status, setStatus] = useState("");
  const models = useMemo(() => [...new Set(providers.flatMap((item) => item.models?.map((entry) => entry.id) || [item.default_model]).filter(Boolean))].sort(), [providers]);
  const active = Boolean(provider || model || client || status || range !== "all");
  const apply = (next: { range?: typeof range; provider?: string; model?: string; client?: string; status?: string }) => {
    const nextRange = next.range ?? range;
    const nextProvider = next.provider ?? provider;
    const nextModel = next.model ?? model;
    const nextClient = next.client ?? client;
    const nextStatus = next.status ?? status;
    setRange(nextRange); setProvider(nextProvider); setModel(nextModel); setClient(nextClient); setStatus(nextStatus);
    const query: UsageQuery = { provider: nextProvider || undefined, model: nextModel || undefined, client: nextClient || undefined, status: nextStatus || undefined };
    if (nextRange !== "all") {
      const from = new Date();
      from.setHours(0, 0, 0, 0);
      from.setDate(from.getDate() - (nextRange === "7d" ? 6 : 29));
      query.from = from.toISOString();
    }
    void onQuery(query);
  };
  const clear = () => apply({ range: "all", provider: "", model: "", client: "", status: "" });
  if (!usage) return null;
  const total = usage.total.usage;
  const hitRate = cacheHitRate(total);
  return <section>
    <SectionHeader title={t("usage")} hideTitle description={usage.total.incomplete ? t("incomplete") : undefined} action={active ? <button className="icon-button" onClick={clear} title={t("clearFilters")} aria-label={t("clearFilters")}><RotateCcw size={16} /></button> : undefined} />
    <div className="usage-toolbar" aria-label={t("usageFilters")}>
      <div className="usage-toolbar-title"><Filter size={15} /><span>{t("filters")}</span></div>
      <label className="usage-filter"><span>{t("timeRange")}</span><select value={range} onChange={(event) => apply({ range: event.target.value as typeof range })}><option value="7d">{t("last7Days")}</option><option value="30d">{t("last30Days")}</option><option value="all">{t("allTime")}</option></select></label>
      <label className="usage-filter"><span>{t("provider")}</span><select value={provider} onChange={(event) => apply({ provider: event.target.value })}><option value="">{t("allProviders")}</option>{providers.map((item) => <option key={item.id} value={item.id}>{item.name || item.id}</option>)}</select></label>
      <label className="usage-filter"><span>{t("model")}</span><select value={model} onChange={(event) => apply({ model: event.target.value })}><option value="">{t("allModels")}</option>{models.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      <label className="usage-filter"><span>{t("clients")}</span><select value={client} onChange={(event) => apply({ client: event.target.value })}><option value="">{t("allClients")}</option>{allClients.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      <label className="usage-filter"><span>{t("status")}</span><select value={status} onChange={(event) => apply({ status: event.target.value })}><option value="">{t("allStatuses")}</option>{["success", "failed", "cancelled", "interrupted"].map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
    </div>
    <div className="metric-grid usage-metrics">
      <Metric label={t("inputMetric")} value={(total?.input_tokens || 0).toLocaleString()} note={t("inputTokens")} icon={Download} />
      <Metric label={t("outputMetric")} value={(total?.output_tokens || 0).toLocaleString()} note={t("outputTokens")} icon={Upload} />
      <Metric label={t("cacheCreation")} value={(total?.cache_creation_input_tokens || 0).toLocaleString()} note={t("tokens")} icon={Database} />
      <Metric label={t("cacheRead")} value={(total?.cache_read_input_tokens || 0).toLocaleString()} note={t("tokens")} icon={RefreshCw} />
      <Metric label={t("cacheHitRate")} value={formatPercent(hitRate)} note={total?.cache_input_tokens ? `${(total.cache_read_input_tokens || 0).toLocaleString()} / ${total.cache_input_tokens.toLocaleString()} ${t("tokens")}` : t("unavailable")} icon={Gauge} />
    </div>
    <UsageTrend byHour={usage.by_hour} byDate={usage.by_date} t={t} />
    <UsageTable title={t("providers")} groups={usage.by_provider} t={t} />
    <UsageTable title={t("model")} groups={usage.by_model} t={t} />
    <UsageTable title={t("clients")} groups={usage.by_client} t={t} />
  </section>;
}

function cacheHitRate(usage: TokenUsage | null | undefined) {
  const denominator = usage?.cache_input_tokens || 0;
  return denominator > 0 ? ((usage?.cache_read_input_tokens || 0) / denominator) * 100 : null;
}

function formatPercent(value: number | null) {
  return value === null ? "—" : `${value.toFixed(1)}%`;
}

type UsageTrendPoint = {
  time: string;
  input: number | null;
  output: number | null;
  cacheCreation: number | null;
  cacheRead: number | null;
  cacheHitRate: number | null;
};

function formatUsageTime(value: string, hourly: boolean) {
  if (!hourly) return value;
  const match = value.match(/^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})/);
  return match ? `${match[1]} ${match[2]}` : value;
}

function formatTokenTick(value: number) {
  const absolute = Math.abs(value);
  if (absolute >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (absolute >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
}

function UsageChartTooltip({ active, label, payload }: TooltipContentProps) {
  if (!active || !payload?.length) return null;
  return <div className="usage-chart-tooltip">
    <time>{label}</time>
    <div>{payload.filter((item) => item.value !== null && item.value !== undefined).map((item) => {
      const rate = item.dataKey === "cacheHitRate";
      return <div className="usage-chart-tooltip-row" key={String(item.dataKey)}>
        <span><i style={{ background: item.color }} />{item.name}</span>
        <strong>{rate ? formatPercent(Number(item.value)) : Number(item.value).toLocaleString()}</strong>
      </div>;
    })}</div>
  </div>;
}

function UsageTrend({ byHour, byDate, t }: { byHour?: Record<string, UsageGroup>; byDate: Record<string, UsageGroup>; t: (key: MessageKey) => string }) {
  const hourly = Boolean(byHour && Object.keys(byHour).length);
  const groups = hourly ? byHour! : byDate;
  const entries = Object.entries(groups).sort(([left], [right]) => left.localeCompare(right));
  if (!entries.length) return <div className="empty-state usage-empty"><Activity size={20} /><span>{t("noUsageData")}</span></div>;
  const data: UsageTrendPoint[] = entries.map(([time, group]) => ({
    time: formatUsageTime(time, hourly),
    input: group.usage?.input_tokens ?? null,
    output: group.usage?.output_tokens ?? null,
    cacheCreation: group.usage?.cache_creation_input_tokens ?? null,
    cacheRead: group.usage?.cache_read_input_tokens ?? null,
    cacheHitRate: cacheHitRate(group.usage),
  }));
  const series = [
    { key: "input", label: t("inputMetric"), color: "var(--chart-input)" },
    { key: "output", label: t("outputMetric"), color: "var(--chart-output)" },
    { key: "cache-creation", label: t("cacheCreation"), color: "var(--chart-creation)" },
    { key: "cache-read", label: t("cacheRead"), color: "var(--chart-read)" },
    { key: "cache-hit-rate", label: t("cacheHitRate"), color: "var(--chart-rate)" },
  ];
  return <div className="usage-block usage-chart-panel" role="img" aria-label={t("cacheTrend")}>
    <div className="usage-block-heading usage-chart-heading">
      <h3>{t("trend")}</h3>
      <div className="usage-chart-legend" aria-label={t("cacheTrend")}>{series.map((item) => <span className={`usage-chart-legend-item ${item.key}`} key={item.key} style={{ color: item.color }}><i />{item.label}</span>)}</div>
    </div>
    <div className="usage-chart-canvas">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={data} margin={{ top: 14, right: 8, bottom: 54, left: 0 }}>
          <CartesianGrid stroke="var(--chart-grid)" strokeDasharray="0" vertical />
          <XAxis dataKey="time" angle={-35} textAnchor="end" height={70} interval="preserveStartEnd" tick={{ fill: "var(--chart-text)", fontSize: 11 }} axisLine={{ stroke: "var(--chart-axis)" }} tickLine={{ stroke: "var(--chart-axis)" }} />
          <YAxis yAxisId="tokens" width={62} tickFormatter={formatTokenTick} tick={{ fill: "var(--chart-text)", fontSize: 11 }} axisLine={{ stroke: "var(--chart-axis)" }} tickLine={{ stroke: "var(--chart-axis)" }} />
          <YAxis yAxisId="rate" orientation="right" domain={[0, 100]} ticks={[0, 20, 40, 60, 80, 100]} width={46} tickFormatter={(value) => `${value}%`} tick={{ fill: "var(--chart-rate)", fontSize: 11 }} axisLine={{ stroke: "var(--chart-axis)" }} tickLine={{ stroke: "var(--chart-axis)" }} />
          <Tooltip content={UsageChartTooltip} cursor={{ stroke: "var(--chart-axis)", strokeDasharray: "3 3" }} />
          <Area yAxisId="tokens" type="monotone" dataKey="input" name={t("inputMetric")} stroke="var(--chart-input)" fill="var(--chart-input)" fillOpacity={0.11} strokeWidth={2.5} dot={{ r: 3, fill: "var(--surface)", strokeWidth: 2 }} activeDot={{ r: 5 }} connectNulls />
          <Area yAxisId="tokens" type="monotone" dataKey="output" name={t("outputMetric")} stroke="var(--chart-output)" fill="var(--chart-output)" fillOpacity={0.07} strokeWidth={2.5} dot={{ r: 3, fill: "var(--surface)", strokeWidth: 2 }} activeDot={{ r: 5 }} connectNulls />
          <Area yAxisId="tokens" type="monotone" dataKey="cacheCreation" name={t("cacheCreation")} stroke="var(--chart-creation)" fill="var(--chart-creation)" fillOpacity={0.07} strokeWidth={2.5} dot={{ r: 3, fill: "var(--surface)", strokeWidth: 2 }} activeDot={{ r: 5 }} connectNulls />
          <Area yAxisId="tokens" type="monotone" dataKey="cacheRead" name={t("cacheRead")} stroke="var(--chart-read)" fill="var(--chart-read)" fillOpacity={0.1} strokeWidth={2.5} dot={{ r: 3, fill: "var(--surface)", strokeWidth: 2 }} activeDot={{ r: 5 }} connectNulls />
          <Line yAxisId="rate" type="monotone" dataKey="cacheHitRate" name={t("cacheHitRate")} stroke="var(--chart-rate)" strokeWidth={2.5} strokeDasharray="7 6" dot={{ r: 3.5, fill: "var(--surface)", stroke: "var(--chart-rate)", strokeWidth: 2 }} activeDot={{ r: 5 }} connectNulls />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  </div>;
}

function UsageTable({ title, groups, t }: { title: string; groups: Record<string, UsageGroup>; t: (key: MessageKey) => string }) {
  const entries = Object.entries(groups).sort(([, left], [, right]) => (right.usage?.total_tokens || 0) - (left.usage?.total_tokens || 0));
  if (!entries.length) return null;
  return <div className="usage-block"><h3>{title}</h3><div className="table-wrap"><table><thead><tr><th>{title}</th><th className="number">{t("requests")}</th><th className="number">{t("inputMetric")}</th><th className="number">{t("outputMetric")}</th><th className="number">{t("cacheCreation")}</th><th className="number">{t("cacheRead")}</th><th className="number">{t("cacheHitRate")}</th><th>{t("status")}</th></tr></thead><tbody>{entries.map(([key, group]) => { const tracked = Boolean(group.usage?.cache_input_tokens); return <tr key={key}><td className="mono">{key}</td><td className="number mono">{group.requests}</td><td className="number mono">{group.usage ? group.usage.input_tokens.toLocaleString() : "—"}</td><td className="number mono">{group.usage ? group.usage.output_tokens.toLocaleString() : "—"}</td><td className="number mono">{tracked ? (group.usage?.cache_creation_input_tokens || 0).toLocaleString() : "—"}</td><td className="number mono">{tracked ? (group.usage?.cache_read_input_tokens || 0).toLocaleString() : "—"}</td><td className="number mono">{formatPercent(cacheHitRate(group.usage))}</td><td>{group.incomplete ? <State value="incomplete" /> : <State value="ok" />}</td></tr>; })}</tbody></table></div></div>;
}

function Empty({ text }: { text: string }) { return <div className="empty-state"><Boxes size={20} /><span>{text}</span></div>; }

function RiskModal({ t, onAccept }: { t: (key: MessageKey) => string; onAccept: () => void }) {
  const dialogRef = useDialogFocus<HTMLDivElement>(true);
  return <div className="modal-backdrop"><div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby="risk-title"><div className="modal-icon"><CircleAlert size={20} /></div><h2 id="risk-title">{t("riskTitle")}</h2><p>{t("riskBody")}</p><button className="primary wide-button" onClick={onAccept}><Check size={16} />{t("accept")}</button></div></div>;
}
