import { Activity, Database, FileClock, Languages, Moon, Network, Power, Save, Sun, TerminalSquare } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useState } from "react";
import { api } from "../api";
import type { Language, MessageKey } from "../i18n";
import type { Config } from "../types";

export type Theme = "light" | "dark" | "system";
type RefreshResource = "status" | "localAccess" | "config" | "providers" | "clients" | "logs" | "usage";
type Translator = (key: MessageKey) => string;

interface NumberSettingProps {
  icon: LucideIcon;
  label: string;
  hint: string;
  value: number;
  min: number;
  max: number;
  error?: string;
  onChange: (value: number) => void;
}

function NumberSetting({ icon: Icon, label, hint, value, min, max, error, onChange }: NumberSettingProps) {
  const errorId = `${label.replace(/\s+/g, "-").toLowerCase()}-error`;
  return <div className="setting-row">
    <div><Icon size={17} /><span><strong>{label}</strong><small>{hint}</small></span></div>
    <div className="setting-control short-control">
      <input className="short-input mono" type="number" min={min} max={max} value={value} onChange={(event) => onChange(Number(event.target.value))} aria-label={label} aria-invalid={Boolean(error)} aria-describedby={error ? errorId : undefined} />
      {error && <small id={errorId} className="field-error" role="alert">{error}</small>}
    </div>
  </div>;
}

export function SettingsPage({ config, language, theme, setLanguage, setTheme, t, run }: { config: Config; language: Language; theme: Theme; setLanguage: (v: Language) => void; setTheme: (v: Theme) => void; t: Translator; run: (op: () => Promise<unknown>, message?: string, resources?: RefreshResource[]) => Promise<void> }) {
  const limits = config.limits || { global: 0, per_client: 0, per_provider: 0, stream_idle_seconds: 300, request_body_bytes: 0, request_header_bytes: 0, client_rate_per_minute: 0 };
  const [host, setHost] = useState(config.listen.host || "127.0.0.1");
  const [port, setPort] = useState(config.listen.port);
  const [logDir, setLogDir] = useState(config.logging.dir);
  const [retentionDays, setRetentionDays] = useState(config.logging.retention_days || 0);
  const [quotaBytes, setQuotaBytes] = useState(config.logging.quota_bytes || 0);
  const [globalLimit, setGlobalLimit] = useState(limits.global);
  const [clientLimit, setClientLimit] = useState(limits.per_client);
  const [providerLimit, setProviderLimit] = useState(limits.per_provider);
  const [streamIdleSeconds, setStreamIdleSeconds] = useState(limits.stream_idle_seconds);
  const [requestBodyBytes, setRequestBodyBytes] = useState(limits.request_body_bytes);
  const [requestHeaderBytes, setRequestHeaderBytes] = useState(limits.request_header_bytes);
  const [clientRatePerMinute, setClientRatePerMinute] = useState(limits.client_rate_per_minute);

  const rangeError = (value: number, min: number, max: number) => value < min || value > max ? t("validationRange") : undefined;
  const portError = port < 1024 || port > 65535 ? t("validationPort") : undefined;
  const logDirError = !logDir.trim() ? t("validationRequired") : undefined;
  const globalLimitError = rangeError(globalLimit, 0, 1024);
  const clientLimitError = rangeError(clientLimit, 0, 1024);
  const providerLimitError = rangeError(providerLimit, 0, 1024);
  const streamIdleError = rangeError(streamIdleSeconds, 1, 86400);
  const requestBodyError = rangeError(requestBodyBytes, 0, 134217728);
  const requestHeaderError = rangeError(requestHeaderBytes, 0, 1048576);
  const clientRateError = rangeError(clientRatePerMinute, 0, 100000);
  const retentionError = rangeError(retentionDays, 0, 3650);
  const quotaError = rangeError(quotaBytes, 0, 1099511627776);
  const invalid = Boolean(portError || logDirError || globalLimitError || clientLimitError || providerLimitError || streamIdleError || requestBodyError || requestHeaderError || clientRateError || retentionError || quotaError);
  const dirty = host !== (config.listen.host || "127.0.0.1") || port !== config.listen.port || logDir !== config.logging.dir || retentionDays !== (config.logging.retention_days || 0) || quotaBytes !== (config.logging.quota_bytes || 0) || language !== config.ui.language || globalLimit !== limits.global || clientLimit !== limits.per_client || providerLimit !== limits.per_provider || streamIdleSeconds !== limits.stream_idle_seconds || requestBodyBytes !== limits.request_body_bytes || requestHeaderBytes !== limits.request_header_bytes || clientRatePerMinute !== limits.client_rate_per_minute;

  const save = () => {
    if (invalid) return;
    const exposesNetwork = host === "0.0.0.0" && (config.listen.host || "127.0.0.1") !== "0.0.0.0";
    if (exposesNetwork && !window.confirm(t("confirmListenExposure"))) return;
    return run(() => api.updateConfig({ ...config, limits: { global: globalLimit, per_client: clientLimit, per_provider: providerLimit, stream_idle_seconds: streamIdleSeconds, request_body_bytes: requestBodyBytes, request_header_bytes: requestHeaderBytes, client_rate_per_minute: clientRatePerMinute }, listen: { host, port }, logging: { ...config.logging, dir: logDir, retention_days: retentionDays, quota_bytes: quotaBytes }, ui: { ...config.ui, language } }), t("success"), ["config", "status", "logs"]);
  };

  return <section>
    <div className="settings-group">
      <h3>{t("interfaceSettings")}</h3>
      <div className="settings-list interface-list">
        <div className="setting-row"><div><Languages size={17} /><span><strong>{t("language")}</strong><small>UI / config.yaml</small></span></div><select aria-label={t("language")} value={language} onChange={(event) => setLanguage(event.target.value as Language)}><option value="zh-CN">简体中文</option><option value="en-US">English</option></select></div>
        <div className="setting-row"><div><Sun size={17} /><span><strong>{t("theme")}</strong><small>{t("localPreference")}</small></span></div><div className="segmented">{(["light", "dark", "system"] as Theme[]).map((value) => <button type="button" aria-pressed={theme === value} className={theme === value ? "selected" : ""} key={value} onClick={() => setTheme(value)}>{value === "dark" && <Moon size={14} />}{value === "light" && <Sun size={14} />}{value === "system" && <TerminalSquare size={14} />}{t(value)}</button>)}</div></div>
      </div>
    </div>
    <div className="settings-group">
      <h3>{t("gatewaySettings")}</h3>
      <div className="settings-list gateway-list">
        <div className="setting-row"><div><Power size={17} /><span><strong>{t("autostart")}</strong><small>{t("currentUserSession")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("autostart")} checked={config.autostart.enabled} onChange={(event) => void run(() => api.setAutostart(event.target.checked), t("success"), ["status"])} /><span /></label></div>
        <div className="setting-row"><div><Network size={17} /><span><strong>{t("listenHost")}</strong><small>{t("listenHostDescription")}</small></span></div><div className="setting-control"><select aria-label={t("listenHost")} aria-describedby={host === "0.0.0.0" ? "listen-exposure-warning" : undefined} value={host} onChange={(event) => setHost(event.target.value)}><option value="127.0.0.1">127.0.0.1</option><option value="0.0.0.0">0.0.0.0</option></select>{host === "0.0.0.0" && <small id="listen-exposure-warning" className="setting-warning" role="note">{t("listenExposureWarning")}</small>}</div></div>
        <NumberSetting icon={Network} label={t("port")} hint={t("listenPortDescription")} value={port} min={1024} max={65535} error={portError} onChange={setPort} />
      </div>
    </div>
    <div className="settings-group">
      <h3>{t("concurrencyLimits")}</h3>
      <div className="settings-list limits-list">
        <NumberSetting icon={Activity} label={t("globalConcurrency")} hint={t("concurrencyLimitHint")} value={globalLimit} min={0} max={1024} error={globalLimitError} onChange={setGlobalLimit} />
        <NumberSetting icon={Activity} label={t("clientConcurrency")} hint={t("concurrencyLimitHint")} value={clientLimit} min={0} max={1024} error={clientLimitError} onChange={setClientLimit} />
        <NumberSetting icon={Activity} label={t("providerConcurrency")} hint={t("concurrencyLimitHint")} value={providerLimit} min={0} max={1024} error={providerLimitError} onChange={setProviderLimit} />
        <NumberSetting icon={Activity} label={t("streamIdleTimeout")} hint={t("streamIdleTimeoutHint")} value={streamIdleSeconds} min={1} max={86400} error={streamIdleError} onChange={setStreamIdleSeconds} />
        <NumberSetting icon={Database} label={t("requestBodyLimit")} hint={t("requestBodyLimitHint")} value={requestBodyBytes} min={0} max={134217728} error={requestBodyError} onChange={setRequestBodyBytes} />
        <NumberSetting icon={Network} label={t("requestHeaderLimit")} hint={t("requestHeaderLimitHint")} value={requestHeaderBytes} min={0} max={1048576} error={requestHeaderError} onChange={setRequestHeaderBytes} />
        <NumberSetting icon={Activity} label={t("clientRateLimit")} hint={t("clientRateLimitHint")} value={clientRatePerMinute} min={0} max={100000} error={clientRateError} onChange={setClientRatePerMinute} />
      </div>
    </div>
    <div className="settings-group">
      <h3>{t("logSettings")}</h3>
      <div className="settings-list logging-list">
        <div className="setting-row"><div><FileClock size={17} /><span><strong>{t("logging")}</strong><small>{t("loggingDescription")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("logging")} checked={config.logging.enabled} onChange={(event) => void run(() => api.setLogging(event.target.checked), t("success"), ["config", "status", "logs"])} /><span /></label></div>
        <div className="setting-row"><div><FileClock size={17} /><span><strong>{t("bodyLogging")}</strong><small>{t("bodyLoggingDescription")}</small></span></div><label className="switch"><input type="checkbox" aria-label={t("bodyLogging")} checked={config.logging.enabled && config.logging.body !== false} disabled={!config.logging.enabled} onChange={(event) => void run(() => api.setLoggingBody(event.target.checked), t("success"), ["config", "status", "logs"])} /><span /></label></div>
        <div className="setting-row log-directory-row"><div><FileClock size={17} /><span><strong>{t("logDir")}</strong><small>{t("relativeDataRoot")}</small></span></div><div className="setting-control"><input aria-label={t("logDir")} value={logDir} onChange={(event) => setLogDir(event.target.value)} aria-invalid={Boolean(logDirError)} aria-describedby={logDirError ? "log-directory-error" : undefined} />{logDirError && <small id="log-directory-error" className="field-error" role="alert">{logDirError}</small>}</div></div>
        <NumberSetting icon={FileClock} label={t("logRetention")} hint={t("logRetentionHint")} value={retentionDays} min={0} max={3650} error={retentionError} onChange={setRetentionDays} />
        <NumberSetting icon={Database} label={t("logQuota")} hint={t("logQuotaHint")} value={quotaBytes} min={0} max={1099511627776} error={quotaError} onChange={setQuotaBytes} />
      </div>
    </div>
    {dirty && <div className="settings-footer"><span className="muted">{t("unsavedChanges")}</span><button className="primary" onClick={() => void save()} disabled={invalid}><Save size={16} />{t("saveSettings")}</button></div>}
  </section>;
}
