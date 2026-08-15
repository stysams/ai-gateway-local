import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity, Boxes, Cable, Check, ChevronRight, CircleAlert, Database, FileClock, Gauge,
  Languages, Moon, Network, Plus, Power, RefreshCw, RotateCcw, Save, Server, Settings, Sun,
  TerminalSquare, Trash2, X,
} from "lucide-react";
import { api } from "./api";
import { translator, type Language, type MessageKey } from "./i18n";
import type { ClientID, Config, LogSummary, PointClient, PointStatus, Provider, Status, UsageGroup, UsageReport } from "./types";
import { validateProvider, type ProviderFormValue } from "./validation";

type Page = "overview" | "providers" | "routes" | "clients" | "logs" | "usage" | "settings";
type Theme = "light" | "dark" | "system";
type ToastKind = "error" | "success";
type Toast = { id: number; kind: ToastKind; message: string };
const pointClients: PointClient[] = ["codex", "claude", "grok"];
const allClients: ClientID[] = ["codex", "claude", "grok", "generic"];

const navigation: { id: Page; icon: typeof Activity }[] = [
  { id: "overview", icon: Activity }, { id: "providers", icon: Boxes }, { id: "routes", icon: Network },
  { id: "clients", icon: Cable }, { id: "logs", icon: FileClock }, { id: "usage", icon: Gauge }, { id: "settings", icon: Settings },
];

const emptyProvider: ProviderFormValue = { id: "", name: "", adapter: "openai-chat", base_url: "", default_model: "", api_key: "" };

export function App() {
  const [page, setPage] = useState<Page>("overview");
  const [language, setLanguage] = useState<Language>(() => (localStorage.getItem("language") as Language) || "zh-CN");
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem("theme") as Theme) || "system");
  const [status, setStatus] = useState<Status | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [clients, setClients] = useState<Record<string, PointStatus>>({});
  const [logs, setLogs] = useState<LogSummary[]>([]);
  const [usage, setUsage] = useState<UsageReport | null>(null);
  const [busy, setBusy] = useState(true);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextToastId = useRef(0);
  const t = useMemo(() => translator(language), [language]);

  useEffect(() => {
    const root = document.documentElement;
    const dark = theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
    root.dataset.theme = dark ? "dark" : "light";
    root.lang = language;
    localStorage.setItem("theme", theme);
    localStorage.setItem("language", language);
  }, [theme, language]);

  const pushToast = useCallback((kind: ToastKind, message: string) => {
    const id = nextToastId.current++;
    setToasts((current) => [...current.slice(-3), { id, kind, message }]);
    window.setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), kind === "error" ? 7000 : 4200);
  }, []);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      const [nextStatus, nextConfig, nextProviders, nextLogs, nextUsage, ...nextClients] = await Promise.all([
        api.status(), api.config(), api.providers(), api.logs(), api.usage(), ...pointClients.map(api.client),
      ]);
      setStatus(nextStatus); setConfig(nextConfig); setProviders(nextProviders); setLogs(nextLogs.items); setUsage(nextUsage);
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

  return (
    <div className="shell">
      <aside className="sidebar" aria-label="Primary navigation">
        <div className="brand"><span className="brand-mark"><TerminalSquare size={17} /></span><span>ai-gateway</span></div>
        <nav>
          {navigation.map(({ id, icon: Icon }) => <button key={id} className={page === id ? "nav-item active" : "nav-item"} onClick={() => setPage(id)} aria-current={page === id ? "page" : undefined}><Icon size={17} /><span>{t(id)}</span></button>)}
        </nav>
        <div className="sidebar-status"><span className={status ? "status-dot ok" : "status-dot"} />{status ? t("connected") : t("loading")}</div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div><p className="eyebrow">AI GATEWAY / {page.toUpperCase()}</p><h1>{t(page)}</h1></div>
          <button className="icon-button" onClick={() => void refresh()} title={t("refresh")} aria-label={t("refresh")} disabled={busy}><RefreshCw size={17} className={busy ? "spin" : ""} /></button>
        </header>
        {busy && !status ? <div className="loading"><RefreshCw className="spin" size={18} />{t("loading")}</div> : (
          <div className="content">
            {page === "overview" && <Overview status={status} usage={usage} t={t} />}
            {page === "providers" && <Providers providers={providers} t={t} run={run} />}
            {page === "routes" && status && <Routes status={status} providers={providers} t={t} run={run} />}
            {page === "clients" && <Clients clients={clients} t={t} run={run} />}
            {page === "logs" && config && <Logs items={logs} enabled={config.logging.enabled} t={t} run={run} />}
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
      <div className="metric-grid">
        <Metric label={t("status")} value={t("connected")} note="127.0.0.1 only" icon={Server} />
        <Metric label={t("requests")} value={String(usage?.total.requests || 0)} note={usage?.total.incomplete ? t("incomplete") : t("success")} icon={Activity} />
        <Metric label={t("tokens")} value={tokens.toLocaleString()} note={`${usage?.total.success || 0} ${t("success")}`} icon={Database} />
      </div>
      <div className="rows"><div className="data-row"><strong>{t("logging")}</strong><State value={status.logging_enabled ? "enabled" : "disabled"} /></div><div className="data-row"><strong>{t("autostart")}</strong><State value={status.autostart_enabled ? "enabled" : "disabled"} /></div></div>
    </section>
    <section><SectionHeader title={t("clientRoutes")} />
      <div className="rows">{allClients.map((client) => <div className="data-row" key={client}><strong className="mono">{client}</strong><div><span>{status.routes[client].provider}</span><ChevronRight size={14} /><span className="mono">{status.routes[client].model}</span></div>{client === "generic" ? <State value="api" /> : <State value={status.clients[client].point_state} />}</div>)}</div>
    </section>
  </>;
}

function Metric({ label, value, note, icon: Icon }: { label: string; value: string; note: string; icon: typeof Activity }) {
  return <div className="metric"><div className="metric-label"><Icon size={16} />{label}</div><strong>{value}</strong><span>{note}</span></div>;
}

function Providers({ providers, t, run }: { providers: Provider[]; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  const [open, setOpen] = useState(false); const [editing, setEditing] = useState<string>(); const [form, setForm] = useState(emptyProvider); const [errors, setErrors] = useState<Record<string, string>>({});
  const edit = (p: Provider) => { setEditing(p.id); setForm({ id: p.id, name: p.name, adapter: p.adapter, base_url: p.base_url, default_model: p.default_model, api_key: "" }); setOpen(true); };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); const nextErrors = validateProvider(form, Boolean(editing)); setErrors(nextErrors); if (Object.keys(nextErrors).length) return;
    await run(() => api.saveProvider({ ...(editing ? {} : { id: form.id }), name: form.name, adapter: form.adapter, base_url: form.base_url, default_model: form.default_model, api_key: form.api_key || undefined, capabilities: { image_input: true, reasoning: true } }, editing), t("success"));
    setOpen(false); setEditing(undefined); setForm(emptyProvider);
  };
  return <section><SectionHeader title={t("providers")} description={`${providers.length}${t("configured")}`} action={<button className="primary" onClick={() => { setOpen(true); setEditing(undefined); setForm(emptyProvider); }}><Plus size={16} />{t("addProvider")}</button>} />
    {open && <form className="form-panel" onSubmit={submit} noValidate><div className="form-grid">
      <Field label={t("identifier")} error={errors.id}><input value={form.id} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, id: e.target.value })} /></Field>
      <Field label={t("name")} error={errors.name}><input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
      <Field label={t("adapter")} error={errors.adapter}><select value={form.adapter} onChange={(e) => setForm({ ...form, adapter: e.target.value })}><option>openai-chat</option><option>openai-responses</option><option>anthropic</option></select></Field>
      <Field label={t("model")} error={errors.default_model}><input value={form.default_model} onChange={(e) => setForm({ ...form, default_model: e.target.value })} /></Field>
      <Field label={t("baseURL")} error={errors.base_url} wide><input type="url" value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} /></Field>
      <Field label={t("apiKey")} wide><input type="password" autoComplete="new-password" value={form.api_key} onChange={(e) => setForm({ ...form, api_key: e.target.value })} placeholder={editing ? t("keepKey") : "sk-…"} /></Field>
    </div><div className="form-actions"><button type="button" className="secondary" onClick={() => setOpen(false)}>{t("cancel")}</button><button className="primary" type="submit"><Save size={16} />{t("save")}</button></div></form>}
    {providers.length === 0 ? <Empty text={t("noProviders")} /> : <div className="table-wrap"><table><thead><tr><th>{t("provider")}</th><th>{t("adapter")}</th><th>{t("model")}</th><th>{t("status")}</th><th className="actions">{t("actions")}</th></tr></thead><tbody>{providers.map((p) => <tr key={p.id}><td><strong>{p.name}</strong><small>{p.id} · {p.base_url}</small></td><td className="mono">{p.adapter}</td><td className="mono">{p.default_model}</td><td><State value={p.has_secret ? "key ready" : "keyless"} /></td><td className="actions"><button className="text-button" onClick={() => void run(async () => { const result = await api.probeProvider(p.id); if (!result.ok) throw new Error(result.error || "Probe failed"); }, t("success"))}>{t("probe")}</button><button className="icon-button compact" onClick={() => edit(p)} title={t("edit")}><Settings size={15} /></button><button className="icon-button compact danger" onClick={() => { if (confirm(t("confirmDelete"))) void run(() => api.deleteProvider(p.id)); }} title={t("remove")}><Trash2 size={15} /></button></td></tr>)}</tbody></table></div>}
  </section>;
}

function Field({ label, error, wide, children }: { label: string; error?: string; wide?: boolean; children: React.ReactNode }) { return <label className={wide ? "field wide" : "field"}><span>{label}</span>{children}{error && <small className="field-error">{error.replaceAll("_", " ")}</small>}</label>; }

function Routes({ status, providers, t, run }: { status: Status; providers: Provider[]; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  const [draft, setDraft] = useState(status.routes);
  return <section><SectionHeader title={t("routes")} description={t("routesDescription")} />
    <div className="route-list">{allClients.map((client) => <div className="route-row" key={client}><div><strong className="mono">{client}</strong><small>/c/{client}/v1</small></div><label><span>{t("provider")}</span><select value={draft[client].provider} onChange={(e) => setDraft({ ...draft, [client]: { ...draft[client], provider: e.target.value } })}>{providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label><label><span>{t("model")}</span><input value={draft[client].model} onChange={(e) => setDraft({ ...draft, [client]: { ...draft[client], model: e.target.value } })} /></label><button className="secondary" onClick={() => void run(() => api.updateRoute(client, draft[client]), t("success"))}><Check size={16} />{t("apply")}</button></div>)}</div>
  </section>;
}

function Clients({ clients, t, run }: { clients: Record<string, PointStatus>; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  return <section><SectionHeader title={t("clients")} description={t("clientsDescription")} /><div className="client-grid">{pointClients.map((client) => { const value = clients[client]; return <article className="client-item" key={client}><div className="client-title"><div className="client-icon"><TerminalSquare size={18} /></div><div><h3>{client}</h3><State value={value?.point_state || "unknown"} /></div></div><dl><dt>{t("target")}</dt><dd className="mono">{value?.target || "—"}</dd></dl>{value?.message && <p className="muted">{value.message}</p>}<div className="client-actions"><button className="primary" disabled={value?.point_state === "pointed" || value?.point_state === "client_not_installed"} onClick={() => { if (confirm(t("confirmPoint"))) void run(() => api.point(client), t("success")); }}><Cable size={16} />{t("point")}</button><button className="secondary" disabled={!value?.backup_available} onClick={() => { if (confirm(t("confirmRestore"))) void run(() => api.restore(client), t("success")); }}><RotateCcw size={16} />{t("restore")}</button></div></article>; })}</div></section>;
}

function Logs({ items, enabled, t, run }: { items: LogSummary[]; enabled: boolean; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  const [detail, setDetail] = useState<{ id: string; events: unknown[] } | null>(null);
  return <section><SectionHeader title={t("requestLog")} description={enabled ? t("enabled") : t("disabled")} action={<label className="switch"><input type="checkbox" checked={enabled} onChange={(e) => void run(() => api.setLogging(e.target.checked))} /><span /><b>{t("logging")}</b></label>} />
    {!enabled && <div className="empty-state inline"><CircleAlert size={18} /><span>{t("disabled")}</span></div>}
    {items.length === 0 ? <Empty text={t("noLogs")} /> : <div className="table-wrap"><table><thead><tr><th>{t("status")}</th><th>{t("provider")}</th><th>{t("model")}</th><th>{t("clients")}</th><th>{t("requests")}</th><th /></tr></thead><tbody>{items.map((item) => <tr key={item.request_id}><td><State value={item.status} /></td><td>{item.provider || "—"}</td><td className="mono">{item.model || "—"}</td><td className="mono">{item.client || "generic"}</td><td><strong className="mono">{new Date(item.started_at).toLocaleTimeString()}</strong><small>{item.duration_ms || 0} ms · {item.status_code || "—"}</small></td><td className="actions"><button className="text-button" onClick={() => void run(async () => { const value = await api.logDetail(item.request_id); setDetail({ id: value.request_id, events: value.events }); })}>{t("details")}</button></td></tr>)}</tbody></table></div>}
    {detail && <div className="drawer-backdrop" onMouseDown={() => setDetail(null)}><aside className="drawer" onMouseDown={(e) => e.stopPropagation()} aria-label={t("details")}><div className="drawer-title"><div><p className="eyebrow">REQUEST</p><h2 className="mono">{detail.id}</h2></div><button className="icon-button" onClick={() => setDetail(null)}><X size={17} /></button></div><pre>{JSON.stringify(detail.events, null, 2)}</pre></aside></div>}
  </section>;
}

function Usage({ usage, t }: { usage: UsageReport | null; t: (key: MessageKey) => string }) {
  if (!usage) return null;
  return <section><SectionHeader title={t("usage")} description={usage.total.incomplete ? t("incomplete") : undefined} /><div className="metric-grid"><Metric label={t("requests")} value={String(usage.total.requests)} note={`${usage.total.success} ${t("success")}`} icon={Activity} /><Metric label={t("tokens")} value={(usage.total.usage?.total_tokens || 0).toLocaleString()} note={`${usage.total.usage?.input_tokens || 0} in / ${usage.total.usage?.output_tokens || 0} out`} icon={Database} /></div><UsageTable title={t("providers")} groups={usage.by_provider} t={t} /><UsageTable title={t("clients")} groups={usage.by_client} t={t} /></section>;
}

function UsageTable({ title, groups, t }: { title: string; groups: Record<string, UsageGroup>; t: (key: MessageKey) => string }) { return <div className="usage-block"><h3>{title}</h3><div className="table-wrap"><table><thead><tr><th>{title}</th><th className="number">{t("requests")}</th><th className="number">{t("tokens")}</th><th>{t("status")}</th></tr></thead><tbody>{Object.entries(groups).map(([key, group]) => <tr key={key}><td className="mono">{key}</td><td className="number mono">{group.requests}</td><td className="number mono">{group.usage?.total_tokens?.toLocaleString() || "—"}</td><td>{group.incomplete ? <State value="incomplete" /> : <State value="ok" />}</td></tr>)}</tbody></table></div></div>; }

function SettingsPage({ config, language, theme, setLanguage, setTheme, t, run }: { config: Config; language: Language; theme: Theme; setLanguage: (v: Language) => void; setTheme: (v: Theme) => void; t: (key: MessageKey) => string; run: (op: () => Promise<unknown>, message?: string) => Promise<void> }) {
  const [port, setPort] = useState(config.listen.port); const [logDir, setLogDir] = useState(config.logging.dir);
  const save = () => run(() => api.updateConfig({ ...config, listen: { port }, logging: { ...config.logging, dir: logDir }, ui: { ...config.ui, language } }), t("success"));
  return <section><SectionHeader title={t("settings")} /><div className="settings-list"><div className="setting-row"><div><Languages size={17} /><span><strong>{t("language")}</strong><small>UI / config.yaml</small></span></div><select value={language} onChange={(e) => setLanguage(e.target.value as Language)}><option value="zh-CN">简体中文</option><option value="en-US">English</option></select></div><div className="setting-row"><div><Sun size={17} /><span><strong>{t("theme")}</strong><small>{t("localPreference")}</small></span></div><div className="segmented">{(["light", "dark", "system"] as Theme[]).map((value) => <button className={theme === value ? "selected" : ""} key={value} onClick={() => setTheme(value)}>{value === "dark" && <Moon size={14} />}{value === "light" && <Sun size={14} />}{value === "system" && <TerminalSquare size={14} />}{t(value)}</button>)}</div></div><div className="setting-row"><div><Power size={17} /><span><strong>{t("autostart")}</strong><small>{t("currentUserSession")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("autostart")} checked={config.autostart.enabled} onChange={(e) => void run(() => api.setAutostart(e.target.checked), t("success"))} /><span /></label></div><div className="setting-row"><div><Network size={17} /><span><strong>{t("port")}</strong><small>127.0.0.1 · {t("loopbackOnly")}</small></span></div><input className="short-input mono" type="number" min="1024" max="65535" value={port} onChange={(e) => setPort(Number(e.target.value))} /></div><div className="setting-row"><div><FileClock size={17} /><span><strong>{t("logDir")}</strong><small>{t("relativeDataRoot")}</small></span></div><input value={logDir} onChange={(e) => setLogDir(e.target.value)} /></div></div><div className="settings-footer"><button className="primary" onClick={() => void save()} disabled={port < 1024 || port > 65535 || !logDir.trim()}><Save size={16} />{t("saveSettings")}</button></div></section>;
}

function Empty({ text }: { text: string }) { return <div className="empty-state"><Boxes size={20} /><span>{text}</span></div>; }

function RiskModal({ t, onAccept }: { t: (key: MessageKey) => string; onAccept: () => void }) { return <div className="modal-backdrop"><div className="modal" role="dialog" aria-modal="true" aria-labelledby="risk-title"><div className="modal-icon"><CircleAlert size={20} /></div><h2 id="risk-title">{t("riskTitle")}</h2><p>{t("riskBody")}</p><button className="primary wide-button" autoFocus onClick={onAccept}><Check size={16} />{t("accept")}</button></div></div>; }
