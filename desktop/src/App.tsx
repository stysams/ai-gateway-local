import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity, Boxes, Cable, Check, ChevronRight, CircleAlert, Copy, Database, FileClock,
  Languages, Moon, Network, Plus, Power, RefreshCw, RotateCcw, Save, Server, Settings, Sun,
  TerminalSquare, Trash2, X,
} from "lucide-react";
import { ClientsIcon, GatewayMark, LogsIcon, OverviewIcon, ProvidersIcon, RoutesIcon, SettingsIcon, UsageIcon, type AppIcon } from "./icons";
import { api } from "./api";
import { catalogId, enabledCatalog, isCatalogRoute, reconcileClientRoutes } from "./catalog";
import { mergeHeaderPreset, presetForAdapter } from "./headerPresets";
import { translator, type Language, type MessageKey } from "./i18n";
import type { ClientID, Config, LocalAccess, LogSummary, PointClient, PointStatus, Provider, ProviderModel, Route, Status, UsageGroup, UsageReport } from "./types";
import { validateProvider, type DisguiseClient, type ProviderFormValue } from "./validation";

type Page = "overview" | "localAccess" | "providers" | "routes" | "clients" | "logs" | "usage" | "settings";
type Theme = "light" | "dark" | "system";
type ToastKind = "error" | "success";
type Toast = { id: number; kind: ToastKind; message: string };
const pointClients: PointClient[] = ["codex", "claude", "grok"];
const allClients: ClientID[] = ["codex", "claude", "grok", "generic"];

const navigation: { id: Page; icon: AppIcon }[] = [
  { id: "overview", icon: OverviewIcon }, { id: "localAccess", icon: GatewayMark }, { id: "providers", icon: ProvidersIcon }, { id: "routes", icon: RoutesIcon },
  { id: "clients", icon: ClientsIcon }, { id: "logs", icon: LogsIcon }, { id: "usage", icon: UsageIcon }, { id: "settings", icon: SettingsIcon },
];

const emptyProvider: ProviderFormValue = { id: "", name: "", adapter: "openai-chat", base_url: "", models_url: "", extra_headers: [], disguise_client: "", default_model: "", models: [], api_key: "" };

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
  const activeNavRef = useRef<HTMLButtonElement | null>(null);
  const t = useMemo(() => translator(language), [language]);

  useEffect(() => {
    const root = document.documentElement;
    const dark = theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
    root.dataset.theme = dark ? "dark" : "light";
    root.lang = language;
    localStorage.setItem("theme", theme);
    localStorage.setItem("language", language);
  }, [theme, language]);

  useEffect(() => {
    if (matchMedia("(max-width: 600px)").matches) activeNavRef.current?.scrollIntoView({ block: "nearest", inline: "center" });
  }, [page]);

  const pushToast = useCallback((kind: ToastKind, message: string) => {
    const id = nextToastId.current++;
    setToasts((current) => [...current.slice(-3), { id, kind, message }]);
    window.setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), kind === "error" ? 7000 : 4200);
  }, []);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      const [nextStatus, nextLocalAccess, nextConfig, nextProviders, nextLogs, nextUsage, ...nextClients] = await Promise.all([
        api.status(), api.localAccess(), api.config(), api.providers(), api.logs(), api.usage(), ...pointClients.map(api.client),
      ]);
      setStatus(nextStatus); setLocalAccess(nextLocalAccess); setConfig(nextConfig); setProviders(nextProviders); setLogs(nextLogs.items); setLogCursor(nextLogs.next_cursor); setUsage(nextUsage);
      setClients(Object.fromEntries(nextClients.map((value) => [value.client, value])));
      if (nextConfig.ui.language === "zh-CN" || nextConfig.ui.language === "en-US") setLanguage(nextConfig.ui.language);
    } catch (reason) { pushToast("error", reason instanceof Error ? reason.message : String(reason)); }
    finally { setBusy(false); }
  }, [pushToast]);

  useEffect(() => {
    queueMicrotask(() => void refresh());
  }, [refresh]);

  const run = async (operation: () => Promise<unknown>, message?: string) => {
    setToasts([]);
    try { await operation(); if (message) pushToast("success", message); await refresh(); }
    catch (reason) { pushToast("error", reason instanceof Error ? reason.message : String(reason)); }
  };

  const acceptRisk = async () => {
    if (!config) return;
    await run(() => api.updateConfig({ ...config, ui: { ...config.ui, logging_notice_accepted: true } }));
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
          {navigation.map(({ id, icon: Icon }) => <button key={id} ref={page === id ? activeNavRef : undefined} className={page === id ? "nav-item active" : "nav-item"} onClick={() => setPage(id)} aria-current={page === id ? "page" : undefined}><Icon size={17} /><span>{t(id)}</span></button>)}
        </nav>
        <div className="sidebar-status"><span className={status ? "status-dot ok" : "status-dot"} />{status ? t("connected") : t("loading")}</div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div className="topbar-inner">
            <div><p className="eyebrow">AI GATEWAY / {page.toUpperCase()}</p><h1>{t(page)}</h1></div>
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
            {page === "logs" && config && <Logs items={logs} hasMore={Boolean(logCursor)} loadMore={loadMoreLogs} enabled={config.logging.enabled} body={config.logging.body !== false} t={t} run={run} notify={pushToast} />}
            {page === "usage" && <Usage usage={usage} t={t} />}
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

function SectionHeader({ title, description, action }: { title: string; description?: string; action?: React.ReactNode }) {
  return <div className="section-header"><div><h2>{title}</h2>{description && <p>{description}</p>}</div>{action}</div>;
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
      <div className="rows"><div className="data-row"><strong>{t("logging")}</strong><State value={status.logging_enabled ? "enabled" : "disabled"} /></div><div className="data-row"><strong>{t("bodyLogging")}</strong><State value={status.logging_enabled && status.logging_body_enabled !== false ? "enabled" : "disabled"} /></div><div className="data-row"><strong>{t("autostart")}</strong><State value={status.autostart_enabled ? "enabled" : "disabled"} /></div></div>
    </section>
    <section><SectionHeader title={t("clientRoutes")} />
      <div className="rows">{allClients.map((client) => <div className="data-row" key={client}><strong className="mono">{client}</strong><span className="mono">{catalogId(status.routes[client])}</span>{client === "generic" ? <State value="api" /> : <State value={status.clients[client].point_state} />}</div>)}</div>
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
      <dl className="access-parameters">
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

function Providers({ providers, t, run, notify }: { providers: Provider[]; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void>; notify: (kind: ToastKind, message: string) => void }) {
  const [open, setOpen] = useState(false); const [editing, setEditing] = useState<string>(); const [form, setForm] = useState(emptyProvider); const [errors, setErrors] = useState<Record<string, string>>({}); const [fetching, setFetching] = useState(false);
  const [probe, setProbe] = useState<{ provider: string; result: { ok: boolean; status: number; latency_ms: number; models?: number; error?: string; response?: string } } | null>(null);
  const edit = (p: Provider) => {
    const models = p.models?.length ? p.models : [{ id: p.default_model, name: "", context_window: 0, max_output_tokens: 0 }];
    const extra_headers = Object.entries(p.extra_headers || {}).sort(([left], [right]) => left.localeCompare(right)).map(([name, value]) => ({ name, value }));
    const disguise_client: DisguiseClient = p.disguise_client === "claude" || p.disguise_client === "codex" ? p.disguise_client : "";
    setEditing(p.id); setForm({ id: p.id, name: p.name, adapter: p.adapter, base_url: p.base_url, models_url: p.models_url || "", extra_headers, disguise_client, default_model: p.default_model, models, api_key: "" }); setErrors({}); setOpen(true);
  };
  const headerPreset = presetForAdapter(form.adapter);
  const updateHeader = (index: number, patch: Partial<ProviderFormValue["extra_headers"][number]>) => setForm((current) => ({ ...current, extra_headers: current.extra_headers.map((header, headerIndex) => headerIndex === index ? { ...header, ...patch } : header) }));
  const removeHeader = (index: number) => setForm((current) => ({ ...current, extra_headers: current.extra_headers.filter((_, headerIndex) => headerIndex !== index) }));
  const updateModel = (index: number, patch: Partial<ProviderModel>) => setForm((current) => ({ ...current, models: current.models.map((model, modelIndex) => modelIndex === index ? { ...model, ...patch } : model) }));
  const removeModel = (index: number) => setForm((current) => {
    const removed = current.models[index]; const models = current.models.filter((_, modelIndex) => modelIndex !== index);
    const default_model = current.default_model === removed.id ? (models[0]?.id || "") : current.default_model;
    return { ...current, models, default_model };
  });
  const addModel = () => setForm((current) => ({ ...current, models: [...current.models, { id: "", name: "", context_window: 0, max_output_tokens: 0 }] }));
  const fetchModels = async () => {
    const discoveryErrors = validateProvider({ ...form, name: "discovery", default_model: "discovery", models: [] }, false);
    const nextErrors = Object.fromEntries(Object.entries(discoveryErrors).filter(([field]) => ["id", "adapter", "base_url", "models_url"].includes(field) || field.startsWith("extra_headers.")));
    if (Object.keys(nextErrors).length) { setErrors(nextErrors); notify("error", t("fetchModelsHint")); return; }
    setFetching(true);
    try {
      const result = await api.discoverProviderModels({ provider_id: form.id, adapter: form.adapter, base_url: form.base_url, models_url: form.models_url || undefined, extra_headers: headerRecord(form.extra_headers), api_key: form.api_key || undefined });
      setForm((current) => {
        const existing = new Map(current.models.filter((model) => model.id.trim()).map((model) => [model.id, model]));
        const fetched = result.data.map((model) => {
          const saved = existing.get(model.raw_id); existing.delete(model.raw_id);
          return { id: model.raw_id, name: saved?.name || model.display_name || "", context_window: saved?.context_window || model.context_window || 0, max_output_tokens: saved?.max_output_tokens || model.max_output_tokens || 0, enabled: saved?.enabled };
        });
        const models = [...fetched, ...existing.values()];
        if (current.default_model && !models.some((model) => model.id === current.default_model)) models.push({ id: current.default_model, name: "", context_window: 0, max_output_tokens: 0 });
        return { ...current, models, default_model: current.default_model || models[0]?.id || "" };
      });
      setErrors({}); notify("success", `${result.data.length}${t("modelsFound")}`);
    } catch (reason) { notify("error", reason instanceof Error ? reason.message : String(reason)); }
    finally { setFetching(false); }
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); const nextErrors = validateProvider(form, Boolean(editing)); setErrors(nextErrors); if (Object.keys(nextErrors).length) return;
    await run(() => api.saveProvider({ ...(editing ? {} : { id: form.id }), name: form.name, adapter: form.adapter, base_url: form.base_url, models_url: form.models_url?.trim() || undefined, extra_headers: headerRecord(form.extra_headers), disguise_client: form.disguise_client, default_model: form.default_model, models: form.models.map((model) => ({ ...model, id: model.id.trim(), name: model.name?.trim() || undefined })), api_key: form.api_key || undefined, capabilities: { image_input: true, reasoning: true, context_management: false } }, editing), t("success"));
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
      <Field label={t("identifier")} error={errors.id}><input value={form.id} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, id: e.target.value })} /></Field>
      <Field label={t("name")} error={errors.name}><input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
      <Field label={t("adapter")} error={errors.adapter}><select value={form.adapter} onChange={(e) => setForm({ ...form, adapter: e.target.value })}><option>openai-chat</option><option>openai-responses</option><option>anthropic</option></select></Field>
      <Field label={t("baseURL")} error={errors.base_url} wide><input type="url" value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} /></Field>
      <Field label={t("modelsURL")} error={errors.models_url} wide><input type="url" value={form.models_url} placeholder={t("modelsURLPlaceholder")} onChange={(e) => setForm({ ...form, models_url: e.target.value })} /></Field>
      <Field label={t("apiKey")} wide><input type="password" autoComplete="new-password" value={form.api_key} onChange={(e) => setForm({ ...form, api_key: e.target.value })} placeholder={editing ? t("keepKey") : "sk-…"} /></Field>
    </div>
    <div className="header-editor disguise-editor"><div className="header-editor-title"><div><h3>{t("disguiseClient")}</h3><p>{t("disguiseClientDescription")}</p></div>
      <label className="field disguise-select"><span>{t("disguiseClient")}</span><select aria-label={t("disguiseClient")} value={form.disguise_client} onChange={(event) => setForm({ ...form, disguise_client: event.target.value as DisguiseClient })}><option value="">{t("disguiseClientOff")}</option><option value="claude">{t("disguiseClientClaude")}</option><option value="codex">{t("disguiseClientCodex")}</option></select></label>
    </div></div>
    <div className="header-editor"><div className="header-editor-title"><div><h3>{t("customHeaders")}</h3><p>{t("customHeadersDescription")}</p></div><div className="header-editor-actions"><button type="button" className="secondary" onClick={() => setForm((current) => ({ ...current, extra_headers: mergeHeaderPreset(current.extra_headers, headerPreset.headers) }))}><Plus size={15} />{t("applyPreset")} {headerPreset.label}</button><button type="button" className="secondary" onClick={() => setForm((current) => ({ ...current, extra_headers: [...current.extra_headers, { name: "", value: "" }] }))}><Plus size={15} />{t("addHeader")}</button></div></div>
      {form.extra_headers.length === 0 ? <div className="header-empty">{t("noCustomHeaders")}</div> : <div className="header-rows" role="table" aria-label={t("customHeaders")}><div className="header-row header-row-head" role="row"><span>{t("headerName")}</span><span>{t("headerValue")}</span><span /></div>{form.extra_headers.map((header, index) => <div className="header-row" role="row" key={`header-row-${index}`}><label><span>{t("headerName")}</span><input className="mono" value={header.name} onChange={(event) => updateHeader(index, { name: event.target.value })} aria-invalid={Boolean(errors[`extra_headers.${index}.name`])} />{errors[`extra_headers.${index}.name`] && <small className="field-error">{errors[`extra_headers.${index}.name`].replaceAll("_", " ")}</small>}</label><label><span>{t("headerValue")}</span><input className="mono" value={header.value} onChange={(event) => updateHeader(index, { value: event.target.value })} aria-invalid={Boolean(errors[`extra_headers.${index}.value`])} />{errors[`extra_headers.${index}.value`] && <small className="field-error">{errors[`extra_headers.${index}.value`].replaceAll("_", " ")}</small>}</label><button type="button" className="icon-button compact danger" onClick={() => removeHeader(index)} title={t("removeHeader")} aria-label={`${t("removeHeader")} ${header.name || index + 1}`}><Trash2 size={15} /></button></div>)}</div>}
    </div><div className="model-catalog"><div className="model-catalog-header"><div><h3>{t("modelCatalog")}</h3><p>{t("modelCatalogDescription")}</p></div><button type="button" className="secondary" onClick={() => void fetchModels()} disabled={fetching}><RefreshCw size={15} className={fetching ? "spin" : ""} />{fetching ? t("fetchingModels") : t("fetchModels")}</button></div>
      {errors.default_model && <small className="field-error catalog-error">{errors.default_model.replaceAll("_", " ")}</small>}
      {form.models.length === 0 ? <div className="model-empty">{t("noModels")}</div> : <div className="model-editor" role="table" aria-label={t("modelCatalog")}><div className="model-editor-head" role="row"><span>{t("defaultModel")}</span><span>{t("modelID")}</span><span>{t("displayName")}</span><span>{t("contextWindow")}</span><span>{t("maxOutputTokens")}</span><span /></div>{form.models.map((model, index) => <div className="model-editor-row" role="row" key={`model-row-${index}`}><label className="default-radio" title={t("defaultModel")}><input type="radio" name="default-model" checked={Boolean(model.id) && form.default_model === model.id} onChange={() => setForm({ ...form, default_model: model.id })} /><span>{t("defaultModel")}</span></label><label><span>{t("modelID")}</span><input className="mono" value={model.id} onChange={(e) => { const value = e.target.value; setForm((current) => { const previous = current.models[index]?.id; const models = current.models.map((item, itemIndex) => itemIndex === index ? { ...item, id: value } : item); return { ...current, models, default_model: current.default_model === previous ? value : current.default_model }; }); }} aria-invalid={Boolean(errors[`models.${index}.id`])} />{errors[`models.${index}.id`] && <small className="field-error">{errors[`models.${index}.id`].replaceAll("_", " ")}</small>}</label><label><span>{t("displayName")}</span><input value={model.name || ""} onChange={(e) => updateModel(index, { name: e.target.value })} /></label><label><span>{t("contextWindow")}</span><input className="mono" type="number" min="0" placeholder={t("upstreamUnknown")} value={model.context_window || ""} onChange={(e) => updateModel(index, { context_window: Number(e.target.value) || 0 })} aria-invalid={Boolean(errors[`models.${index}.context_window`])} /></label><label><span>{t("maxOutputTokens")}</span><input className="mono" type="number" min="0" placeholder={t("upstreamUnknown")} value={model.max_output_tokens || ""} onChange={(e) => updateModel(index, { max_output_tokens: Number(e.target.value) || 0 })} aria-invalid={Boolean(errors[`models.${index}.max_output_tokens`])} /></label><button type="button" className="icon-button compact danger" onClick={() => removeModel(index)} title={t("removeModel")} aria-label={`${t("removeModel")} ${model.id || index + 1}`}><Trash2 size={15} /></button></div>)}</div>}
      <button type="button" className="text-button add-model" onClick={addModel}><Plus size={15} />{t("addModel")}</button></div>
    </form>
  </section>;

  return <section><SectionHeader title={t("providers")} description={`${providers.length}${t("configured")}`} action={<button className="primary" onClick={() => { setOpen(true); setEditing(undefined); setForm(emptyProvider); setErrors({}); }}><Plus size={16} />{t("addProvider")}</button>} />
    {providers.length === 0 ? <Empty text={t("noProviders")} /> : <div className="table-wrap providers-table-wrap"><table className="providers-table"><thead><tr><th>{t("provider")}</th><th>{t("adapter")}</th><th>{t("model")}</th><th>{t("modelCount")}</th><th>{t("status")}</th><th className="actions">{t("actions")}</th></tr></thead><tbody>{providers.map((p) => <tr key={p.id}><td><strong>{p.name}</strong><small>{p.id} · {p.base_url}</small></td><td className="mono" data-label={t("adapter")}>{p.adapter}</td><td className="mono" data-label={t("model")}>{p.default_model}</td><td className="mono" data-label={t("modelCount")}>{p.models?.length || 0}</td><td><State value={p.enabled === false ? "disabled" : p.has_secret ? "key ready" : "keyless"} /></td><td className="actions"><button className="text-button" onClick={() => void probeProvider(p)}>{t("probe")}</button><button className="icon-button compact" onClick={() => edit(p)} title={t("edit")}><Settings size={15} /></button><button className="icon-button compact danger" onClick={() => { if (confirm(t("confirmDelete"))) void run(() => api.deleteProvider(p.id)); }} title={t("remove")}><Trash2 size={15} /></button></td></tr>)}</tbody></table></div>}
    {probe && <div className="modal-backdrop" onMouseDown={() => setProbe(null)}><div className="modal probe-modal" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}><div className="drawer-title"><div><p className="eyebrow">{t("probeResponse")}</p><h2>{probe.provider}</h2></div><button className="icon-button" onClick={() => setProbe(null)} aria-label={t("close")}><X size={17} /></button></div><div className="probe-meta"><State value={probe.result.ok ? "ok" : "failed"} /><span>{probe.result.status || "-"} · {probe.result.latency_ms} ms · {probe.result.models || 0} {t("models")}</span></div>{probe.result.error && <p className="field-error">{probe.result.error}</p>}<pre>{formatProbeResponse(probe.result.response) || t("noProbeResponse")}</pre></div></div>}
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

function Field({ label, error, wide, children }: { label: string; error?: string; wide?: boolean; children: React.ReactNode }) { return <label className={wide ? "field wide" : "field"}><span>{label}</span>{children}{error && <small className="field-error">{error.replaceAll("_", " ")}</small>}</label>; }

function Routes({ status, providers, t, run }: { status: Status; providers: Provider[]; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  const catalog = useMemo(() => enabledCatalog(providers), [providers]);
  const [override, setOverride] = useState<Partial<Record<ClientID, Route>>>({});
  const draft = useMemo(() => reconcileClientRoutes(override, status.routes, catalog), [override, status.routes, catalog]);
  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(() => new Set());
  const toggleProvider = (provider: Provider, enabled: boolean) => run(() => api.updateProviderAvailability(provider.id, { enabled }), t("success"));
  const toggleModel = (provider: Provider, model: ProviderModel, enabled: boolean) => run(() => api.updateProviderAvailability(provider.id, { models: { [model.id]: enabled } }), t("success"));
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
    void run(() => api.updateRoute(client, next), t("success"));
  };
  return <section><SectionHeader title={t("routes")} description={t("routesDescription")} />
    <div className="route-clients primary-route-block"><h3>{t("clientRoutes")}</h3><p className="muted">{t("clientRoutesDescription")}</p><div className="route-list">{allClients.map((client) => {
      const currentKnown = isCatalogRoute(draft[client], catalog);
      const currentId = currentKnown ? catalogId(draft[client]) : "";
      const savedId = catalogId(status.routes[client]);
      const canApply = currentKnown && currentId !== savedId;
      return <div className="route-row" key={client}><div><strong className="mono">{client}</strong><small>/c/{client}/v1</small></div><label><span>{t("defaultSelectedModel")}</span><select className="mono" required aria-required="true" aria-invalid={!currentKnown} aria-label={`${client} ${t("defaultSelectedModel")}`} value={currentId} onChange={(event) => selectRoute(client, event.target.value)}><option value="" disabled>{t("selectDefaultModel")}</option>{catalog.map((item) => <option key={item.id} value={item.id}>{item.id}</option>)}</select>{!currentKnown && <small className="field-error">{t("routeUnavailable")}</small>}</label><button className="secondary" disabled={!canApply} onClick={() => applyRoute(client)}><Check size={16} />{t("apply")}</button></div>;
    })}</div></div>
    <div className="route-catalog-header"><div><h3>{t("routeCatalog")}</h3><p className="muted">{t("availabilityDescription")}</p></div><span className="mono muted">{providers.length} {t("providers")}</span></div>
    <div className="route-tree" aria-label={t("routeCatalog")}>{providers.map((provider) => { const treeModels = provider.models?.length ? provider.models : [{ id: provider.default_model, name: "", context_window: 0, max_output_tokens: 0 }]; const expanded = expandedProviders.has(provider.id); const enabledCount = treeModels.filter((model) => model.enabled !== false).length; return <div className="tree-provider" key={provider.id}><div className="tree-provider-row"><button className="tree-provider-toggle" onClick={() => toggleExpanded(provider.id)} aria-expanded={expanded} aria-label={`${provider.name} ${expanded ? t("hideModels") : t("showModels")}`}><ChevronRight className={expanded ? "expanded" : ""} size={15} /><span><b>{provider.name}</b><small>{enabledCount}/{treeModels.length} {t("models")}</small></span></button><span className="mono muted">{provider.id}</span><label className="switch"><input type="checkbox" checked={provider.enabled !== false} onChange={(event) => toggleProvider(provider, event.target.checked)} aria-label={`${t("provider")} ${provider.name}`} /><span /><b>{provider.enabled === false ? t("disabled") : t("enabled")}</b></label></div>{expanded && <div className="tree-models">{treeModels.map((model) => <div className="tree-model-row" key={model.id}><span className="tree-branch" aria-hidden="true"><ChevronRight size={14} /></span><span className="mono">{`${provider.id}/${model.id}`}</span><label className="switch"><input type="checkbox" checked={provider.enabled !== false && model.enabled !== false} disabled={provider.enabled === false} onChange={(event) => toggleModel(provider, model, event.target.checked)} aria-label={`${provider.id}/${model.id} ${t("enabled")}`} /><span /><b>{model.enabled === false ? t("disabled") : t("enabled")}</b></label></div>)}</div>}</div>; })}</div>
  </section>;
}

function Clients({ clients, t, run }: { clients: Record<string, PointStatus>; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  return <section><SectionHeader title={t("clients")} description={t("clientsDescription")} /><div className="client-list">{pointClients.map((client) => { const value = clients[client]; return <article className="client-item" key={client}><div className="client-main"><div className="client-title"><div className="client-icon"><ClientsIcon size={18} /></div><div><h3>{client}</h3><State value={value?.point_state || "unknown"} /></div></div><dl><dt>{t("target")}</dt><dd className="mono">{value?.target || "—"}</dd></dl><div className="client-actions"><button className="primary" disabled={value?.point_state === "pointed" || value?.point_state === "client_not_installed"} onClick={() => { if (confirm(t("confirmPoint"))) void run(() => api.point(client), t("success")); }}><Cable size={16} />{t("point")}</button><button className="secondary" disabled={!value?.backup_available} onClick={() => { if (confirm(t("confirmRestore"))) void run(() => api.restore(client), t("success")); }}><RotateCcw size={16} />{t("restore")}</button></div></div>{value?.message && <p className="client-message muted">{value.message}</p>}{client === "codex" && <details className="client-advanced" open><summary>{t("advancedSettings")}</summary><div className="client-option"><label className="switch"><input type="checkbox" checked={Boolean(value?.remote_compaction)} onChange={(event) => void run(() => api.setCodexRemoteCompaction(event.target.checked), t("success"))} aria-label={t("remoteCompaction")} /><span /><b>{t("remoteCompaction")}</b></label><p className="muted">{t("remoteCompactionHint")}</p></div></details>}</article>; })}</div></section>;
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

function Logs({ items, hasMore, loadMore, enabled, body, t, run, notify }: { items: LogSummary[]; hasMore: boolean; loadMore: () => Promise<void>; enabled: boolean; body: boolean; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void>; notify: (kind: ToastKind, message: string) => void }) {
  const [detail, setDetail] = useState<{ id: string; events: unknown[] } | null>(null);
  const [copied, setCopied] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const copyLog = async (id: string, events?: unknown[]) => {
    try {
      const value = events ? { request_id: id, events } : await api.logDetail(id);
      await copyText(JSON.stringify(value, null, 2));
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
  return <section><div className="log-toolbar"><SectionHeader title={t("requestLog")} description={`${enabled ? (body ? t("loggingWithBody") : t("loggingMetadataOnly")) : t("disabled")} · ${items.length} ${t(items.length === 1 ? "requestEntry" : "requestEntries")}`} action={<div className="header-switches"><label className="switch"><input type="checkbox" checked={enabled} onChange={(e) => void run(() => api.setLogging(e.target.checked))} aria-label={t("logging")} /><span /><b>{t("logging")}</b></label><label className="switch"><input type="checkbox" checked={enabled && body} disabled={!enabled} onChange={(e) => void run(() => api.setLoggingBody(e.target.checked))} aria-label={t("bodyLogging")} /><span /><b>{t("bodyLogging")}</b></label></div>} /></div>
    {!enabled && <div className="empty-state inline"><CircleAlert size={18} /><span>{t("disabled")}</span></div>}
    {items.length === 0 ? <Empty text={t("noLogs")} /> : <><div className="table-wrap log-scroll"><table className="log-table"><thead><tr><th>{t("status")}</th><th>{t("route")}</th><th>{t("clients")}</th><th>{t("requests")}</th><th /></tr></thead><tbody>{items.map((item) => <tr key={item.request_id}><td className="log-status"><State value={item.status} /></td><td className="log-route"><strong>{item.provider || "—"}</strong><small className="mono">{item.model || "—"}</small></td><td className="log-client mono">{item.client || "generic"}</td><td className="log-request"><strong className="mono">{new Date(item.started_at).toLocaleTimeString()}</strong><small>{item.duration_ms || 0} ms · {item.status_code || "—"}</small></td><td className="actions log-actions"><button className="icon-button compact" onClick={() => void copyLog(item.request_id)} title={t("copyRequestLog")} aria-label={`${t("copyRequestLog")} ${item.request_id}`}><Copy size={15} /></button><button className="text-button" onClick={() => void run(async () => { const value = await api.logDetail(item.request_id); setDetail({ id: value.request_id, events: value.events }); })}>{t("details")}</button></td></tr>)}</tbody></table></div>{hasMore && <div className="load-more"><button className="secondary" disabled={loadingMore} onClick={() => void onLoadMore()}>{loadingMore && <RefreshCw size={15} className="spin" />}{loadingMore ? t("loadingMore") : t("loadMore")}</button></div>}</>}
    {detail && <div className="drawer-backdrop" onMouseDown={() => setDetail(null)}><aside className="drawer" onMouseDown={(e) => e.stopPropagation()} aria-label={t("details")}><div className="drawer-title"><div><p className="eyebrow">REQUEST</p><h2 className="mono">{detail.id}</h2></div><div className="drawer-title-actions"><button className="secondary copy-button" onClick={() => void copyDetail()}><Copy size={15} />{copied ? t("copied") : t("copyRequestLog")}</button><button className="icon-button" onClick={() => setDetail(null)} aria-label={t("close")}><X size={17} /></button></div></div><pre>{JSON.stringify({ request_id: detail.id, events: detail.events }, null, 2)}</pre></aside></div>}
  </section>;
}

function Usage({ usage, t }: { usage: UsageReport | null; t: (key: MessageKey) => string }) {
  if (!usage) return null;
  return <section><SectionHeader title={t("usage")} description={usage.total.incomplete ? t("incomplete") : undefined} /><div className="metric-grid usage-metrics"><Metric label={t("requests")} value={String(usage.total.requests)} note={`${usage.total.success} ${t("success")}`} icon={Activity} /><Metric label={t("tokens")} value={(usage.total.usage?.total_tokens || 0).toLocaleString()} note={`${usage.total.usage?.input_tokens || 0} in / ${usage.total.usage?.output_tokens || 0} out`} icon={Database} /></div><UsageTable title={t("providers")} groups={usage.by_provider} t={t} /><UsageTable title={t("clients")} groups={usage.by_client} t={t} /></section>;
}

function UsageTable({ title, groups, t }: { title: string; groups: Record<string, UsageGroup>; t: (key: MessageKey) => string }) { return <div className="usage-block"><h3>{title}</h3><div className="table-wrap"><table><thead><tr><th>{title}</th><th className="number">{t("requests")}</th><th className="number">{t("tokens")}</th><th>{t("status")}</th></tr></thead><tbody>{Object.entries(groups).map(([key, group]) => <tr key={key}><td className="mono">{key}</td><td className="number mono">{group.requests}</td><td className="number mono">{group.usage?.total_tokens?.toLocaleString() || "—"}</td><td>{group.incomplete ? <State value="incomplete" /> : <State value="ok" />}</td></tr>)}</tbody></table></div></div>; }

function SettingsPage({ config, language, theme, setLanguage, setTheme, t, run }: { config: Config; language: Language; theme: Theme; setLanguage: (v: Language) => void; setTheme: (v: Theme) => void; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  const [host, setHost] = useState(config.listen.host || "127.0.0.1"); const [port, setPort] = useState(config.listen.port); const [logDir, setLogDir] = useState(config.logging.dir);
  const save = () => run(() => api.updateConfig({ ...config, listen: { host, port }, logging: { ...config.logging, dir: logDir }, ui: { ...config.ui, language } }), t("success"));
  const dirty = host !== (config.listen.host || "127.0.0.1") || port !== config.listen.port || logDir !== config.logging.dir || language !== config.ui.language;
  return <section><SectionHeader title={t("settings")} />
    <div className="settings-group"><h3>{t("interfaceSettings")}</h3><div className="settings-list"><div className="setting-row"><div><Languages size={17} /><span><strong>{t("language")}</strong><small>UI / config.yaml</small></span></div><select value={language} onChange={(e) => setLanguage(e.target.value as Language)}><option value="zh-CN">简体中文</option><option value="en-US">English</option></select></div><div className="setting-row"><div><Sun size={17} /><span><strong>{t("theme")}</strong><small>{t("localPreference")}</small></span></div><div className="segmented">{(["light", "dark", "system"] as Theme[]).map((value) => <button className={theme === value ? "selected" : ""} key={value} onClick={() => setTheme(value)}>{value === "dark" && <Moon size={14} />}{value === "light" && <Sun size={14} />}{value === "system" && <TerminalSquare size={14} />}{t(value)}</button>)}</div></div></div></div>
    <div className="settings-group"><h3>{t("gatewaySettings")}</h3><div className="settings-list"><div className="setting-row"><div><Power size={17} /><span><strong>{t("autostart")}</strong><small>{t("currentUserSession")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("autostart")} checked={config.autostart.enabled} onChange={(e) => void run(() => api.setAutostart(e.target.checked), t("success"))} /><span /></label></div><div className="setting-row"><div><Network size={17} /><span><strong>{t("listenHost")}</strong><small>{t("listenHostDescription")}</small></span></div><select value={host} onChange={(e) => setHost(e.target.value)}><option value="127.0.0.1">127.0.0.1</option><option value="0.0.0.0">0.0.0.0</option></select></div><div className="setting-row"><div><Network size={17} /><span><strong>{t("port")}</strong><small>{t("listenPortDescription")}</small></span></div><input className="short-input mono" type="number" min="1024" max="65535" value={port} onChange={(e) => setPort(Number(e.target.value))} /></div></div></div>
    <div className="settings-group"><h3>{t("logSettings")}</h3><div className="settings-list"><div className="setting-row"><div><FileClock size={17} /><span><strong>{t("logging")}</strong><small>{t("loggingDescription")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("logging")} checked={config.logging.enabled} onChange={(e) => void run(() => api.setLogging(e.target.checked), t("success"))} /><span /></label></div><div className="setting-row"><div><FileClock size={17} /><span><strong>{t("bodyLogging")}</strong><small>{t("bodyLoggingDescription")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("bodyLogging")} checked={config.logging.enabled && config.logging.body !== false} disabled={!config.logging.enabled} onChange={(e) => void run(() => api.setLoggingBody(e.target.checked), t("success"))} /><span /></label></div><div className="setting-row"><div><FileClock size={17} /><span><strong>{t("logDir")}</strong><small>{t("relativeDataRoot")}</small></span></div><input value={logDir} onChange={(e) => setLogDir(e.target.value)} /></div></div></div>
    {dirty && <div className="settings-footer"><span className="muted">{t("unsavedChanges")}</span><button className="primary" onClick={() => void save()} disabled={port < 1024 || port > 65535 || !logDir.trim()}><Save size={16} />{t("saveSettings")}</button></div>}
  </section>;
}

function Empty({ text }: { text: string }) { return <div className="empty-state"><Boxes size={20} /><span>{text}</span></div>; }

function RiskModal({ t, onAccept }: { t: (key: MessageKey) => string; onAccept: () => void }) { return <div className="modal-backdrop"><div className="modal" role="dialog" aria-modal="true" aria-labelledby="risk-title"><div className="modal-icon"><CircleAlert size={20} /></div><h2 id="risk-title">{t("riskTitle")}</h2><p>{t("riskBody")}</p><button className="primary wide-button" autoFocus onClick={onAccept}><Check size={16} />{t("accept")}</button></div></div>; }
