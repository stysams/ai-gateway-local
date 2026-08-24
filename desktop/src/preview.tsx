/* eslint-disable react-refresh/only-export-components */
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Braces,
  Check,
  CheckCircle2,
  ChevronRight,
  Cloud,
  Copy,
  Cpu,
  FileText,
  Filter,
  Gauge,
  Laptop,
  Moon,
  RefreshCw,
  Route,
  Search,
  Server,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  TerminalSquare,
  Users,
  type LucideIcon,
} from "lucide-react";
import { GatewayMark } from "./icons";
import "./preview.css";

type ViewId = "overview" | "api" | "providers" | "routes" | "clients" | "logs" | "usage" | "settings";
type Tone = "success" | "warning" | "muted";

type NavItem = {
  id: ViewId;
  label: string;
  icon: LucideIcon;
  group: "工作区" | "配置" | "观测" | "系统";
};

const navItems: NavItem[] = [
  { id: "overview", label: "总览", icon: Activity, group: "工作区" },
  { id: "api", label: "本地 API", icon: Braces, group: "工作区" },
  { id: "providers", label: "提供商", icon: Server, group: "配置" },
  { id: "routes", label: "路由", icon: Route, group: "配置" },
  { id: "clients", label: "客户端", icon: Users, group: "配置" },
  { id: "logs", label: "日志", icon: FileText, group: "观测" },
  { id: "usage", label: "用量", icon: Gauge, group: "观测" },
  { id: "settings", label: "设置", icon: Settings, group: "系统" },
];

const viewMeta: Record<ViewId, { eyebrow: string; title: string; description: string }> = {
  overview: { eyebrow: "CONTROL PLANE", title: "运行总览", description: "本机网关状态、流量和路由异常" },
  api: { eyebrow: "LOCAL ACCESS", title: "本地 API", description: "连接地址、认证方式与兼容端点" },
  providers: { eyebrow: "UPSTREAMS", title: "提供商", description: "上游连接、模型目录与可用性" },
  routes: { eyebrow: "ROUTING", title: "路由", description: "客户端默认模型与提供商映射" },
  clients: { eyebrow: "CLIENTS", title: "客户端", description: "配置指向状态与本地接入检查" },
  logs: { eyebrow: "OBSERVABILITY", title: "请求日志", description: "最近通过网关转发的请求" },
  usage: { eyebrow: "USAGE", title: "用量", description: "请求量、成功率与令牌消耗" },
  settings: { eyebrow: "PREFERENCES", title: "设置", description: "监听、日志与桌面行为" },
};

const requests = [
  { time: "13:00:08", client: "Codex", provider: "OpenRouter", model: "gpt-5", latency: "841 ms", status: "成功" },
  { time: "12:58:42", client: "Claude", provider: "OpenRouter", model: "claude-sonnet-4", latency: "1.24 s", status: "成功" },
  { time: "12:55:19", client: "Grok", provider: "DeepSeek", model: "deepseek-chat", latency: "612 ms", status: "成功" },
  { time: "12:47:03", client: "Codex", provider: "OpenRouter", model: "gpt-5", latency: "3.08 s", status: "失败" },
];

function Status({ tone = "success", children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`status status-${tone}`}><span className="status-mark" aria-hidden="true" />{children}</span>;
}

function IconButton({ label, children, onClick, active = false }: { label: string; children: ReactNode; onClick?: () => void; active?: boolean }) {
  return <button className={`icon-button${active ? " is-active" : ""}`} type="button" aria-label={label} title={label} onClick={onClick}>{children}</button>;
}

function CopyButton({ value, notify }: { value: string; notify: (message: string) => void }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      notify("已复制到剪贴板");
    } catch {
      notify("当前环境无法访问剪贴板");
    }
  };
  return <IconButton label={`复制 ${value}`} onClick={copy}><Copy size={15} /></IconButton>;
}

function SectionHeading({ title, description, action }: { title: string; description?: string; action?: ReactNode }) {
  return <div className="section-heading"><div><h2>{title}</h2>{description ? <p>{description}</p> : null}</div>{action}</div>;
}

function Overview({ navigate }: { navigate: (view: ViewId) => void }) {
  return <div className="page-stack">
    <section className="gateway-summary" aria-labelledby="gateway-health-title">
      <div className="gateway-primary">
        <div className="health-icon"><ShieldCheck size={22} /></div>
        <div>
          <p className="micro-label">GATEWAY HEALTH</p>
          <h2 id="gateway-health-title">网关运行正常</h2>
          <p>监听 <span className="mono">127.0.0.1:12600</span>，两个上游提供商已就绪。</p>
        </div>
      </div>
      <div className="metric"><span>今日请求</span><strong className="mono">24</strong><small>23 成功，1 失败</small></div>
      <div className="metric"><span>令牌总量</span><strong className="mono">16.2k</strong><small>输入 12k · 输出 4.2k</small></div>
      <div className="metric"><span>平均响应</span><strong className="mono">934 ms</strong><small>最近 24 次请求</small></div>
    </section>

    <section className="overview-grid">
      <div className="panel traffic-panel">
        <SectionHeading title="实时流量" description="最近通过本地网关的请求" action={<button className="text-button" type="button" onClick={() => navigate("logs")}>查看全部<ArrowRight size={14} /></button>} />
        <div className="request-table" role="table" aria-label="最近请求">
          <div className="request-row request-head" role="row"><span>时间</span><span>客户端</span><span>上游与模型</span><span>耗时</span><span>状态</span></div>
          {requests.map((item) => <div className="request-row" role="row" key={`${item.time}-${item.client}`}>
            <span className="mono subtle">{item.time}</span>
            <span>{item.client}</span>
            <span><b>{item.provider}</b><small className="mono">{item.model}</small></span>
            <span className="mono">{item.latency}</span>
            <Status tone={item.status === "成功" ? "success" : "warning"}>{item.status}</Status>
          </div>)}
        </div>
      </div>

      <aside className="panel route-watch" aria-labelledby="route-watch-title">
        <SectionHeading title="路由关注" description="需要处理的客户端状态" />
        <div className="watch-item warning-item">
          <div className="watch-icon"><AlertTriangle size={17} /></div>
          <div><b>Grok 配置已漂移</b><p>当前目标与网关记录不一致。</p></div>
          <IconButton label="查看 Grok 路由" onClick={() => navigate("clients")}><ChevronRight size={16} /></IconButton>
        </div>
        <div className="watch-item">
          <div className="watch-icon neutral"><TerminalSquare size={17} /></div>
          <div><b>Claude 尚未指向网关</b><p>可以随时写入本地客户端配置。</p></div>
          <IconButton label="查看 Claude 客户端" onClick={() => navigate("clients")}><ChevronRight size={16} /></IconButton>
        </div>
        <button className="secondary wide" type="button" onClick={() => navigate("routes")}><Route size={15} />检查所有路由</button>
      </aside>
    </section>

    <section className="route-strip" aria-label="当前请求路径">
      <div><span className="micro-label">CURRENT REQUEST PATH</span><strong>Codex 默认路由</strong></div>
      <div className="path-node"><Laptop size={16} /><span>Codex</span></div><ChevronRight className="path-arrow" size={16} />
      <div className="path-node"><GatewayMark size={16} /><span>ai-gateway</span></div><ChevronRight className="path-arrow" size={16} />
      <div className="path-node accent-node"><Cloud size={16} /><span>openrouter / gpt-5</span></div>
    </section>
  </div>;
}

function LocalApi({ notify }: { notify: (message: string) => void }) {
  const entries = [
    ["Base URL", "http://127.0.0.1:12600/v1"],
    ["API Key", "ai-gateway"],
    ["默认模型", "gateway-default"],
  ];
  const endpoints = ["/v1/models", "/v1/chat/completions", "/v1/responses", "/v1/messages"];
  return <div className="page-stack two-column-page">
    <section className="panel span-two">
      <SectionHeading title="连接参数" description="OpenAI 兼容客户端可直接使用以下本地地址" action={<Status>无需认证</Status>} />
      <div className="key-value-grid">{entries.map(([key, value]) => <div className="key-value" key={key}><span>{key}</span><code>{value}</code><CopyButton value={value} notify={notify} /></div>)}</div>
    </section>
    <section className="panel">
      <SectionHeading title="兼容端点" description="当前数据平面已开放的接口" />
      <div className="plain-list">{endpoints.map((endpoint) => <div className="plain-row" key={endpoint}><Braces size={15} /><code>{endpoint}</code><Status>可用</Status></div>)}</div>
    </section>
    <section className="panel">
      <SectionHeading title="已启用模型" description="通过本地模型目录可见" />
      <div className="plain-list"><div className="plain-row"><Cpu size={15} /><code>openrouter/gpt-5</code></div><div className="plain-row"><Cpu size={15} /><code>openrouter/anthropic/claude-sonnet-4</code></div><div className="plain-row"><Cpu size={15} /><code>deepseek/deepseek-chat</code></div></div>
    </section>
  </div>;
}

function Providers({ notify }: { notify: (message: string) => void }) {
  const providers = [
    { name: "OpenRouter", id: "openrouter", adapter: "OpenAI Responses", base: "https://openrouter.ai/api/v1", model: "gpt-5", latency: "318 ms", models: 2 },
    { name: "DeepSeek", id: "deepseek", adapter: "OpenAI Chat", base: "https://api.deepseek.com", model: "deepseek-chat", latency: "612 ms", models: 1 },
  ];
  return <div className="page-stack">
    <section className="panel">
      <SectionHeading title="上游提供商" description="密钥已保存在本机配置中" action={<button className="primary" type="button" onClick={() => notify("预览中已打开新增提供商流程")}><Server size={15} />新增提供商</button>} />
      <div className="provider-list">{providers.map((provider) => <article className="provider-row" key={provider.id}>
        <div className="provider-mark"><Cloud size={18} /></div>
        <div className="provider-name"><b>{provider.name}</b><code>{provider.id}</code></div>
        <div><span className="field-label">适配器</span><strong>{provider.adapter}</strong></div>
        <div><span className="field-label">默认模型</span><code>{provider.model}</code></div>
        <div><span className="field-label">探测延迟</span><strong className="mono">{provider.latency}</strong></div>
        <div className="provider-state"><Status>{provider.models} 个模型</Status><IconButton label={`配置 ${provider.name}`} onClick={() => notify(`已选择 ${provider.name}`)}><SlidersHorizontal size={16} /></IconButton></div>
      </article>)}</div>
    </section>
    <section className="provider-foot"><ShieldCheck size={17} /><div><b>凭据仅保存在本机</b><p>预览使用项目测试数据，不会向外部服务发起请求。</p></div></section>
  </div>;
}

function RoutesView({ notify }: { notify: (message: string) => void }) {
  const routes = [
    { client: "Codex", provider: "openrouter", model: "gpt-5", state: "已生效" },
    { client: "Claude", provider: "openrouter", model: "anthropic/claude-sonnet-4", state: "待写入" },
    { client: "Grok", provider: "deepseek", model: "deepseek-chat", state: "已漂移" },
    { client: "通用", provider: "openrouter", model: "gpt-5", state: "已生效" },
  ];
  return <div className="page-stack">
    <section className="panel">
      <SectionHeading title="客户端默认路由" description="客户端启动时默认选择的提供商与模型" action={<button className="primary" type="button" onClick={() => notify("路由更改已在预览中应用")}><Check size={15} />应用更改</button>} />
      <div className="route-list">{routes.map((route) => <div className="route-row" key={route.client}>
        <div className="client-chip"><TerminalSquare size={16} /><b>{route.client}</b></div>
        <ArrowRight size={15} className="subtle" />
        <label><span>提供商</span><select defaultValue={route.provider} aria-label={`${route.client} 提供商`}><option value="openrouter">OpenRouter</option><option value="deepseek">DeepSeek</option></select></label>
        <label className="model-select"><span>默认模型</span><select defaultValue={`${route.provider}/${route.model}`} aria-label={`${route.client} 默认模型`}><option value="openrouter/gpt-5">openrouter/gpt-5</option><option value="openrouter/anthropic/claude-sonnet-4">openrouter/anthropic/claude-sonnet-4</option><option value="deepseek/deepseek-chat">deepseek/deepseek-chat</option></select></label>
        <Status tone={route.state === "已生效" ? "success" : "warning"}>{route.state}</Status>
      </div>)}</div>
    </section>
  </div>;
}

function ClientsView({ notify }: { notify: (message: string) => void }) {
  const clients = [
    { name: "Codex", path: "C:/Users/test/.codex/config", state: "已指向网关", tone: "success" as Tone, action: "恢复" },
    { name: "Claude", path: "C:/Users/test/.claude/config", state: "尚未指向", tone: "warning" as Tone, action: "写入配置" },
    { name: "Grok", path: "C:/Users/test/.grok/config", state: "配置已漂移", tone: "warning" as Tone, action: "重新指向" },
  ];
  return <div className="page-stack client-grid">{clients.map((client) => <article className="panel client-card" key={client.name}>
    <div className="client-card-head"><div className="client-logo"><TerminalSquare size={20} /></div><Status tone={client.tone}>{client.state}</Status></div>
    <h2>{client.name}</h2><p className="mono path-text">{client.path}</p>
    <dl><div><dt>目标地址</dt><dd className="mono">127.0.0.1:12600</dd></div><div><dt>备份</dt><dd>{client.name === "Claude" ? "不可用" : "可恢复"}</dd></div></dl>
    <button className={client.tone === "success" ? "secondary wide" : "primary wide"} type="button" onClick={() => notify(`${client.name}：${client.action}操作已进入预览状态`)}>{client.action}</button>
  </article>)}</div>;
}

function LogsView() {
  return <div className="page-stack">
    <section className="toolbar-row"><div className="search-box"><Search size={15} /><input aria-label="搜索请求日志" placeholder="按请求编号、客户端或模型搜索" /></div><button className="secondary" type="button"><Filter size={15} />筛选</button></section>
    <section className="panel log-panel"><div className="log-table request-table" role="table" aria-label="请求日志">
      <div className="log-row log-head"><span>请求编号</span><span>开始时间</span><span>客户端</span><span>提供商 / 模型</span><span>耗时</span><span>结果</span></div>
      {requests.map((item, index) => <div className="log-row" key={item.time}><code>req_0{index + 1}</code><span className="mono subtle">2026-08-15 {item.time}</span><span>{item.client}</span><span><b>{item.provider}</b><small className="mono">{item.model}</small></span><span className="mono">{item.latency}</span><Status tone={item.status === "成功" ? "success" : "warning"}>{item.status === "成功" ? "200" : "502"}</Status></div>)}
    </div></section>
  </div>;
}

function UsageView() {
  return <div className="page-stack">
    <section className="usage-summary">
      <div><span>请求总数</span><strong className="mono">24</strong><small>测试数据集</small></div>
      <div><span>成功率</span><strong className="mono">95.8%</strong><small>23 次成功</small></div>
      <div><span>输入令牌</span><strong className="mono">12,000</strong><small>占总量 74%</small></div>
      <div><span>输出令牌</span><strong className="mono">4,200</strong><small>占总量 26%</small></div>
    </section>
    <section className="panel">
      <SectionHeading title="提供商用量" description="当前测试数据全部由 OpenRouter 承载" />
      <div className="usage-provider"><div className="provider-mark"><Cloud size={18} /></div><div><b>OpenRouter</b><p className="mono">24 requests</p></div><div className="usage-bar" aria-label="OpenRouter 用量占比 100%"><span /></div><strong className="mono">16.2k tokens</strong><Status>95.8%</Status></div>
    </section>
  </div>;
}

function SettingsView({ dark, setDark, notify }: { dark: boolean; setDark: (dark: boolean) => void; notify: (message: string) => void }) {
  return <div className="page-stack settings-layout">
    <section className="panel">
      <SectionHeading title="网关监听" description="本地数据平面的绑定设置" />
      <div className="setting-row"><div><b>监听端口</b><p>仅本机可访问</p></div><input className="compact-input mono" aria-label="监听端口" defaultValue="12600" /></div>
      <div className="setting-row"><div><b>启动时自动运行</b><p>登录系统后启动网关</p></div><button className="switch" type="button" role="switch" aria-checked="false" onClick={() => notify("自动启动已在预览中开启")}><span /></button></div>
    </section>
    <section className="panel">
      <SectionHeading title="界面" description="仅影响桌面控制台" />
      <div className="setting-row"><div><b>深色主题</b><p>在完整的深色表面体系中显示</p></div><button className={`switch${dark ? " on" : ""}`} type="button" role="switch" aria-checked={dark} onClick={() => setDark(!dark)}><span /></button></div>
      <div className="setting-row"><div><b>界面语言</b><p>桌面预览使用简体中文</p></div><select aria-label="界面语言" defaultValue="zh-CN"><option value="zh-CN">简体中文</option><option value="en-US">English</option></select></div>
    </section>
    <section className="panel span-two">
      <SectionHeading title="日志与隐私" description="请求正文可能包含敏感信息" />
      <div className="setting-row"><div><b>记录请求日志</b><p>保存状态、耗时和令牌统计</p></div><button className="switch on" type="button" role="switch" aria-checked="true"><span /></button></div>
      <div className="setting-row"><div><b>记录请求正文</b><p>仅用于本机排查，请谨慎开启</p></div><button className="switch on" type="button" role="switch" aria-checked="true"><span /></button></div>
    </section>
  </div>;
}

function App() {
  const [view, setView] = useState<ViewId>("overview");
  const [dark, setDark] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [toast, setToast] = useState("");
  const meta = viewMeta[view];

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  }, [dark]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(""), 2200);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const groups = useMemo(() => ["工作区", "配置", "观测", "系统"] as const, []);
  const refresh = () => {
    setRefreshing(true);
    window.setTimeout(() => {
      setRefreshing(false);
      setToast("状态已刷新");
    }, 520);
  };

  let content: ReactNode;
  if (view === "overview") content = <Overview navigate={setView} />;
  else if (view === "api") content = <LocalApi notify={setToast} />;
  else if (view === "providers") content = <Providers notify={setToast} />;
  else if (view === "routes") content = <RoutesView notify={setToast} />;
  else if (view === "clients") content = <ClientsView notify={setToast} />;
  else if (view === "logs") content = <LogsView />;
  else if (view === "usage") content = <UsageView />;
  else content = <SettingsView dark={dark} setDark={setDark} notify={setToast} />;

  return <div className="preview-shell">
    <aside className="preview-sidebar">
      <div className="preview-brand"><div className="brand-symbol"><GatewayMark size={19} /></div><div><b>ai-gateway</b><span>LOCAL CONTROL</span></div></div>
      <nav aria-label="主导航">{groups.map((group) => <div className="nav-group" key={group}><p>{group}</p>{navItems.filter((item) => item.group === group).map((item) => {
        const Icon = item.icon;
        return <button className={`nav-button${view === item.id ? " active" : ""}`} type="button" key={item.id} onClick={() => setView(item.id)} aria-current={view === item.id ? "page" : undefined}><Icon size={16} /><span>{item.label}</span>{item.id === "clients" ? <i aria-label="两个客户端需要处理">2</i> : null}</button>;
      })}</div>)}</nav>
      <div className="sidebar-runtime"><div className="runtime-head"><span className="runtime-pulse" /><b>网关在线</b><span>v0.1.0</span></div><code>127.0.0.1:12600</code><div className="runtime-meta"><span>PID 4242</span><span>本地模式</span></div></div>
    </aside>

    <main className="preview-main">
      <header className="preview-topbar">
        <div><p className="page-eyebrow">{meta.eyebrow}</p><h1>{meta.title}</h1><p>{meta.description}</p></div>
        <div className="top-actions"><Status>本机连接</Status><IconButton label={dark ? "切换到明色主题" : "切换到深色主题"} onClick={() => setDark(!dark)}>{dark ? <Sun size={16} /> : <Moon size={16} />}</IconButton><IconButton label="刷新状态" onClick={refresh} active={refreshing}><RefreshCw className={refreshing ? "spin" : ""} size={16} /></IconButton></div>
      </header>
      <div className="preview-content">{content}<footer><span>桌面端视觉预览</span><span>数据来自项目自动化测试样例</span></footer></div>
    </main>
    <div className={`toast${toast ? " visible" : ""}`} role="status" aria-live="polite"><CheckCircle2 size={16} />{toast}</div>
  </div>;
}

createRoot(document.getElementById("preview-root")!).render(<App />);
