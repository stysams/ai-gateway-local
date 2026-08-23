# ai-gateway 第一期进度与接续

> 状态：第一期已验收（带遗留问题），第二期已完成并发布
> 文档日期：2026-08-23
> 包构建提交：`ec0dc264a03af6409ff8aa16e1a675f9c7546834`（日志隐私治理、Token 用量与三协议能力扩展；后续仅发布记录文档变更不影响包内容）
> 当前发布：`0.1.0-rc1`（`dist/ai-gateway-0.1.0-rc1-windows-amd64.zip`，SHA-256 `C1DC64A4FDE60BA4F9850CF7B7AE2B6F69D1CEE667B0CC512B11D1750ECAAAF8`）
> 第一验收平台：Windows 11

本文给后续 Agent（含 Grok）接续工作。本文记录进度、权威边界、已落地增量和下一步，**不是新的工程合同**。

开始任何实现或改协议之前，必须先读 [v1-scheme.md](v1-scheme.md)。本文与规格冲突时，以规格为准。外部契约变化时，按规格 §17 / §20 先记证据再改规格，禁止凭记忆兼容。

---

## 1. 现在处于哪一步

任务包 A 到 J 已经全部实现。2026-08-22，产品所有者根据当前实际使用效果，决定将第一期标记为**已验收（带遗留问题）**，并解除第二期启动门禁。

第一期原始完成定义在规格 §22：陌生用户在 Windows 11 上装好无头网关和桌面后，能保存钥匙、指向三个客户端、完成流式工具调用、不改客户端文件就切换上游、看正文日志和真实用量、随时关日志、精确还原、关桌面后网关仍运行、登录后自动启动并解开钥匙。

§19 已于 2026-08-22 执行一轮真实验收。原始二十项门禁没有全部通过，但核心数据面、三客户端工具调用、路由、图片、日志、用量、还原和进程行为已经满足当前使用需求。未完成和部分通过项保留原始证据，并转入本文第 13 节遗留清单，最后集中处理；它们不再阻塞第二期。

---

## 2. 权威与阅读顺序

按这个顺序读，不要跳：

1. 本文：进度、禁区、下一步。
2. [code-map.md](code-map.md)：按任务找文件。改哪一类行为，先查那张表。
3. [v1-scheme.md](v1-scheme.md)：冻结合同。改 `config`、`route`、`ir`、`inbound`、`outbound`、`secret`、`point` 或 HTTP 面之前，先读源码引用的小节（`docs/v1-scheme.md §7.4` 这种写法）。
4. [install.md](install.md)：操作员安装、指向、还原。
5. 仓库根目录 `CLAUDE.md`：依赖方向、数据面管线、测试约定、常用命令。
6. 根目录 `README.md`：任务包交付摘要和构建命令。

`memory/` 是本机任务日记，已被 gitignore，不要当成合同，也不要提交。

### 2.1 规格赢的地方

规格与实现不一致时，停下来对照规格。禁止为了让测试通过而削弱协议断言，禁止留下桩实现或固定返回值。

### 2.2 实现已经超前、以代码为准的地方

这些能力已经在代码、测试和管理面落地，规格正文尚未全部改写成同等条款。改这些行为时以代码和现有测试为准，不要为了“对齐旧规格句子”删掉它们：

| 能力 | 位置 |
|---|---|
| `listen.host` 可为 `127.0.0.1` 或 `0.0.0.0`（默认仍是回环） | `internal/config`，桌面设置页 |
| provider / 模型的 `enabled` 可用性开关 | `config.Provider.Enabled`、`ProviderModel.Enabled` |
| `PUT /api/v1/providers/{id}/availability` | `internal/server` |
| provider `models_url` | 配置与供应商表单 |
| `POST /api/v1/provider-models/discover` | 草稿态拉模型，不写配置、不存钥匙 |
| `capabilities.context_management`；未启用则剥离并记 `context_management_dropped` | 数据面能力门 |

规格 §5.2 现已同步 `listen.host` 的允许值和管理 API 的回环访问边界。当前实现允许显式配置 `0.0.0.0` 给局域网数据面客户端，默认继续 `127.0.0.1`。

其余地方规格仍然赢。

---

## 3. 任务包完成情况

| 包 | 名称 | 状态 | 说明 |
|---|---|---|---|
| A | 仓库引导与无头骨架 | 已完成 | CLI、配置原子写、单实例锁、健康检查、优雅关闭 |
| B | 系统钥匙存储 | 已完成 | Windows DPAPI；macOS / Linux 明确失败，无明文回退 |
| C | 路由与 Chat 同协议转发 | 已完成 | §7.4、SSE 实时 flush、不跟随重定向 |
| D | IR 与三协议转换 | 已完成 | 六个跨协议方向只走 `ir`；断流发协议错误，不伪造完成 |
| E | 图片、reasoning、能力降级 | 已完成 | 不支持图片则 422 且零上游；reasoning 不可表达则删除并记警告 |
| F | 正文日志与用量 | 已完成 | 每请求 JSONL；默认递归脱敏；用量只信上游，缺失标不完整，禁止估算 |
| G | 完整管理 API | 已完成 | 无头即可完成供应商、路由、日志脱敏导出与清理、用量 |
| H | 三客户端 point / restore | 已完成 | 事务备份、漂移、精确还原；自动化测试已覆盖 |
| I | Wails 桌面主流程 | 已完成 | 七个视图，只走 `/api/v1`，不承载 `/v1/*` |
| J | 托盘、登录启动、发布 | 已完成 | 托盘切路由；登录启动代码已写，本机权限验收未过 |

包与包之间的依赖顺序见规格 §18。不要回头重做已完成包，除非验收暴露了可复现缺陷。

---

## 4. A 到 J 之后已经落地的增量

这些不是新的任务包，已经合进当前提交和 `0.1.0-rc1` 包：

1. **供应商是多模型容器。** 可持久化模型目录（id、显示名、上下文窗口、最大输出令牌）。未知令牌上限保持未知，禁止按模型名推测。
2. **客户端路由是启动默认模型，不是唯一可用模型。** 指向后首选槽位写 `gateway-default`。切换路由不得改写已指向客户端的配置文件，也不得替换最初还原点。
3. **已启用模型按 `供应商/模型ID` 出现在客户端里。**
   - 共同入口：`GET /v1/models` 与 `GET /c/{client}/v1/models`。
   - 除 `/c/claude/v1/models` 外，`display_name` 必须等于 `id`（`gateway-default` 或 `<provider-id>/<model-id>`）。
   - Grok Build 额外写入 `[model."ai-gateway:<provider-id>/<model-id>"]`，`name` 同样写成该 id。
   - Codex 写入 `model_catalog_json` → `ai-gateway-catalog.json`（克隆 bundled 模板）。
   - Claude Code 打开网关模型发现。`/c/claude/v1/models` 的 `id` 是可逆
     `claude-gw*` 选择器别名，`display_name` 仍是真实可选 id，使 `/model`
     列出全部已启用模型。
4. **桌面「客户端路由」是单一默认模型选择器**，选项为全部已启用的 `供应商/模型ID`。该选择器是必选项：不得把已禁用供应商或已禁用模型继续显示为当前值。供应商或模型被禁用后，对应客户端的选中默认路由会被清空，必须重新选择并应用。配置里的四条路由不会被写成空值。托盘菜单同一套目录。
5. **Claude Code `messages` 数组里的 `role: system` 已兼容**，与顶层 `system` 合并为内部系统提示。
6. **探测响应**可在桌面以格式化 JSON 查看；失败正文不回传，避免错误响应泄露钥匙。
7. **桌面操作反馈**是右上角可关闭气泡，不是整页横幅。
8. **日志详情保留完整事件，复制和下载使用强制脱敏 JSONL。** 敏感头只记省略计数；写盘默认递归脱敏常见凭据字段，单条删除和批量清理不触碰活动日志。
9. **Codex 远程压缩开关。** 桌面「客户端」页的 Codex 卡片可开关
   `clients.codex.remote_compaction`。开启后指向/同步把
   `[model_providers.ai-gateway].name` 写成 `OpenAI`，并转发
   `POST /v1/responses/compact`。关闭则改回 `ai-gateway`。不得因此新建还原点。
   只有解析到的模型使用 `openai-responses` 时可以转发 compact；其它适配器 422。
10. **Responses 历史回放的 `output_text` 按文本转换。** Codex 把上一轮助手
    回复原样放进下一轮 `input`；跨协议不得因此 422。
11. **Chat 出站工具参数是 JSON 字符串。** Responses `custom_tool_call`
    经 IR 包装后，`openai-chat` 必须把 `function.arguments` 写成字符串，
    不能嵌对象。否则 OpenCode Console Go 会 400。
12. **Chat 出站工具结果必须紧跟 `tool_calls`。** Responses 回放常把
    reasoning 和后续助手正文插在调用与结果之间；丢掉 reasoning 后不得
    留下空助手消息，出站必须先配对再放其余正文。
13. **Claude Code `/model` 列出全部已启用模型。** `/c/claude/v1/models` 把
    `id` 写成可逆 `claude-gw*` 选择器别名，以通过客户端
    `/(claude|anthropic)/i` 过滤器；`display_name` 仍是真实可选 id。
    `route.Resolve` 先解码再走 §7.4。指向和目录同步还按 OpenCodex
    `ocx claude` 的外形预写 `cache/gateway-models.json`，`baseUrl` 等于
    `ANTHROPIC_BASE_URL`。别名不得写进启动环境变量、Codex/Grok 目录或发给上游。
14. **桌面和管理端都是单实例。** 网关继续用 `gateway.lock`。桌面在创建窗口和托盘之前先拿 Wails 单实例锁；再次启动只激活已有窗口并退出，不再出现第二个托盘图标。桌面派生 `serve` 之前必须探测 `gateway.lock`，锁已被占用时只等待管理面就绪，不得再拉起一个网关进程。`ai-gateway-desktop serve` 仍走无头入口，只受 `gateway.lock` 约束。
15. **Responses 出站助手历史必须用 `output_text`。** Claude Code 把上一轮
    助手正文放进下一轮 `messages`；跨到 `openai-responses` 时不得把该块
    写成 `input_text`，否则上游 400。用户和 developer 仍用 `input_text`。
16. **Claude Code 用户消息里的 `tool_result` 必须转成工具结果。** ToolSearch
    会把 `tool_reference` 和后续文本放在同一条 `role: user` 消息里。入站
    拆成 IR `RoleTool` + 剩余 `RoleUser`，结构化内容保留为 JSON 字符串。
    出站 Responses 必须紧跟 `function_call` 写出 `function_call_output`，
    出站 Chat 必须紧跟 `tool_calls` 写出 `role: tool`。丢掉结果只留
    `Tool loaded.` 会让上游 400。
17. **未归属模型不得透传。** 请求模型没有供应商前缀、也不是任何已启用
    provider 已登记模型时，数据面 400，错误为
    `未匹配当前选择的[<model>],请选择正确的 供应商/模型ID`，零上游接触。
    显式 `<provider-id>/<model-id>` 和当前路由已登记模型仍可解析。
18. **入站 `Anthropic-Beta` 与 `extra_headers` 按令牌并集。** Claude Code
    的 `context-1m-2025-08-07` 不得被供应商预设整段覆盖。其它入站头仍
    禁止转发。
19. **供应商伪装客户端开关。** `disguise_client` 为 `claude` 或 `codex`
    时，只给第三方 `generic` 入站套用已核验身份头。`claude` 在 Messages
    入站上还要补齐 `thinking: {type: adaptive}`（需启用 reasoning）和
    系统文本块的 `cache_control: {type: ephemeral}`。不得改写 tools、
    用户消息或系统文本，不得写入会话 metadata。已指向客户端、探测
    和模型发现不套用。`extra_headers` 覆盖同名伪装头；`Anthropic-Beta`
    按令牌并集。不得把开关写成再往 `extra_headers` 里粘贴一份预设。
20. **禁用的默认路由不得挡住前缀覆盖。** `route.Resolve` 不得在第 3 步
    之前因为当前路由 provider 禁用而拒绝请求。Codex 默认停在已禁用的
    `tudou` 时，`any/claude-opus-5` 或 `agentrouter/claude-opus-5` 必须
    覆盖成功。只有 `gateway-default`、以及已登记在该禁用路由上的未加前缀
    模型，才回报 `provider "<route>" is disabled`。
21. **出站协议绑定到模型。** 同一 provider（同一 `base_url` 与钥匙）的
    不同模型可以分别使用 `openai-chat`、`openai-responses` 或
    `anthropic`。`models[].adapter` 覆盖 provider 默认 adapter。数据面、
    compact 和默认模型探测都按解析到的模型取协议。模型发现和无目录旧
    配置仍用 provider `adapter`。桌面模型目录每一行都有接口协议选择器。
22. **供应商表单不再配置适配器和令牌上限。** 桌面去掉供应商级适配器
    选项，也去掉模型的上下文窗口与最大输出令牌。保存时用默认模型的
    协议回写 provider `adapter`。上下文由客户端与上游协商；Claude 1M
    窗口用模型 ID 后缀 `[1m]`，例如 `claude-opus-5[1m]`。
23. **Messages 出站 `tool_choice` 必须是官方对象。** IR 仍用 `"auto"` /
    `"none"` / `"required"`；出站 Anthropic 写成 `{"type":"auto"}`、
    `{"type":"none"}`、`{"type":"any"}`。AgentRouter / Bedrock 拒绝字符串
    `"auto"`。同协议若收到 `text/html` 流，仍按原文转发，但记
    `upstream_not_event_stream` 并标失败。
24. **预设 Claude / GPT 端点默认补 `/v1`。** `openai-chat`、
    `openai-responses`、`anthropic` 的请求路径锁定，桌面只读。
    `base_url` 尚未以 `/v1` 结尾时自动补上。不走 `/v1` 的上游把模型
    `adapter` 写成 `custom`，并自己维护 `endpoint`。桌面在输入框下方
    显示实际调用 URL。`provider.adapter` 仍只回写报文协议。
25. **指向只改模型与路由键，其余字节原样保留。** 三个适配器不再整档
    重新序列化客户端配置。`internal/point/tomledit` 与
    `internal/point/jsonedit` 做字节级拼接：已有键只替换值的字节区间，
    缺失键只插入一行，Grok 失效的 `[model."ai-gateway:*"]` 只删该表行区间。
    注释、键顺序、引号风格，以及 `[mcp_servers.*]`、`[plugins.*]`、
    `[projects.*]`、`[profiles.*]`、Claude 的 `permissions` / `hooks` /
    `statusLine` / `enabledMcpjsonServers` 全部不动；CRLF 文件插入的行也用
    CRLF。已指向配置重复 point 是字节级幂等。目标键落在内联表、数组或
    表数组里时报 `ErrUnsupportedShape`，退回整档重新序列化（仍保留未知
    字段语义）。不要为了省事把 `Transform` 改回 `Unmarshal` → 改 map →
    `Marshal`。
26. **结构化输出跨三协议保留。** Chat `response_format.json_schema`、
    Responses `text.format` 与 Messages `output_config.format` 归一化到
    `ir.OutputFormat`；Anthropic 没有名称时跨到 OpenAI 使用稳定名称
    `structured_output`。函数工具的严格模式归一化到 `Tool.Strict`。
27. **日志隐私治理使用写盘脱敏加导出再脱敏。** `logging.redact` 默认开启，
    覆盖顶层和嵌套 JSON、HTTP 头、JSON 字符串与 SSE 数据帧中的常见凭据字段。
    桌面复制和下载始终调用脱敏导出端点；删除活动日志返回 409，批量清理跳过活动日志。

相关证据在规格 §20「2026-08-15 复核：客户端可选模型目录」、
「2026-08-16 复核：Codex 远程压缩触发条件」、
「2026-08-16 复核：Responses 历史回放使用 output_text」、
「2026-08-16 复核：Chat 出站工具参数必须是 JSON 字符串」和
「2026-08-16 复核：Chat 出站工具结果必须紧跟 tool_calls」和
「2026-08-16 复核：Claude Code `/model` 使用可逆选择器别名」和
「2026-08-16 复核：Claude Code `/model` 必须预写 gateway-models.json」和
「2026-08-16 复核：Responses 出站助手历史必须用 output_text」和
「2026-08-16 复核：Claude Code 用户消息里的 tool_result 必须转成 function_call_output」和
「2026-08-17 复核：未归属模型不得透传到当前路由」和
「2026-08-17 复核：Anthropic-Beta 必须与 extra_headers 并集」和
「2026-08-17 复核：第三方请求需要可开关的客户端伪装」和
「2026-08-17 复核：Claude 伪装必须补齐 thinking 与系统 cache_control」和
「2026-08-18 复核：禁用的默认路由不得挡住前缀覆盖」和
「2026-08-18 复核：出站协议绑定到模型」和
「2026-08-18 复核：AgentRouter OpenAI 面必须带 /v1」和
「2026-08-18 复核：Messages 出站 tool_choice 必须是对象」和
「2026-08-18 复核：预设端点默认补 /v1，例外走自定义路径」和
「2026-08-21 复核：整档重新序列化会重排用户的 MCP 与工具配置」。不要重新发明 Codex 目录方案，也不要只靠 Claude 启动发现而不写缓存。

---

## 5. 三个客户端的目录契约（不要改回去）

| 客户端 | 配置里写什么 | 用户怎么选其它模型 | 禁止事项 |
|---|---|---|---|
| Codex | 首选 `model = "gateway-default"`；根键 `model_catalog_json` 指向同目录 `ai-gateway-catalog.json` | `/model`、`codex debug models`、`codex -m <provider-id>/<model-id>`，或 `/c/codex/v1/models` | 目录条目必须从本机 `codex debug models --bundled` 克隆，保留 `base_instructions`，删除 `model_messages`。禁止手写短提示词。找不到模板则拒绝指向。 |
| Claude Code | 四个模型环境变量都是 `gateway-default`；`CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`；`<CLAUDE_CONFIG_DIR>/cache/gateway-models.json` 写入全部已启用模型 | `/model` 读这份缓存（`display_name` 为 `供应商/模型 ID`）；启动时若发现未被关掉也会刷新同一文件；或 `claude --model <provider-id>/<model-id>` | `/c/claude/v1/models` 与缓存里的 `id` 必须是可逆 `claude-gw*` 别名。禁止把别名写进启动环境变量、Codex/Grok 目录或发给上游。不得覆盖用户已有的无关 `env`（包括 `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`）。 |
| Grok Build | `[models] default = "ai-gateway"`；每个已启用模型一条 `ai-gateway:` 前缀表 | 配置目录与内置模型并存 | restore 只删网关写过的条目，必须保留用户自己的模型。 |

切换路由只改网关配置。客户端文件里只要还是 `gateway-default`，就不要去改它。

指向和目录同步都是最小改写：只重写上表列出的模型与路由键，客户端配置里其它字节
（注释、键顺序、引号风格，以及 MCP、工具、插件、权限、UI、profile 配置）必须原样
保留。见规格 §12.1 与 §20 的 2026-08-21 条。

Grok 目录增删发生在 point 与设置同步（可用性、目录变化）时，不得新建还原点。若旧 Grok 配置里 `name` 还是友好名称，检查会报漂移；再执行一次指向即可就地刷新，不会替换最初还原点。

---

## 6. 冻结决策（普通任务禁止重开）

| 主题 | 决策 |
|---|---|
| 语言与工具链 | Go `1.26`，开发锁定 `go1.26.6` |
| 桌面 | Wails **精确** `v3.0.0-beta.8`。只有独立升级任务可以改版本。 |
| 前端 | React、TypeScript、Vite、npm；提交 `package-lock.json` |
| 配置 | 只有 `config.yaml`。桌面不得另存一份业务配置。 |
| 钥匙 | 只进系统钥匙库。YAML、客户端文件、管理响应、正文日志都不得出现真实钥匙。`Get` 之后立刻清零。 |
| 数据面 | 桌面进程不服务 `/v1/*`。桌面只打回环 `/api/v1`。 |
| 同协议 | 只改 model / stream / 认证，其余字段按原始 JSON 值转发（键序和空白不保证）。 |
| 跨协议 | 只经 `ir`。adapter 互不调用。`ir` 不导入具体协议包。 |
| 流 | 禁止把整段上游响应攒完再假装成流。每个 SSE 事件立刻 flush。无总超时。 |
| 重定向 | 不跟随。状态和 `Location` 原样交给客户端，避免钥匙打到第二目标。 |
| 平台代码 | 用构建标签文件，禁止在共享文件里写 `runtime.GOOS` 分支。 |
| 依赖方向 | `cmd → app → {config,secret,route,server,...}`；`inbound/outbound → ir`；`inbound` 不碰钥匙库；`outbound` 不决定路由。 |

---

## 7. 下一步（按这个顺序做）

### 7.1 主线：启动第二期

第一期已经按“带遗留问题验收”关闭。第二期按 [optimization-roadmap.md](optimization-roadmap.md) 推进：

1. `G0-02` 已完成：正式基线已在干净提交 `2f7c9344027b394bfec64eb7254c832ba930fde4` 上冻结。
2. `SEC-01`、`PERF-01`、`RES-01`、`RES-02` 和 `SEC-02` 已完成：管理 API 只接受真实 TCP 回环来源，配置只读路径使用原子发布的不可变视图，数据面支持并发限制、流式空闲超时、请求体和请求头上限以及单客户端速率限制；后续按依赖启动 `PERF-02`、`LOG-01`、`OBS-01`。
3. 每个发布级变更继续执行 `REL-01` 的测试、打包和回归要求。

### 7.2 第一期遗留问题

第 12 节中的未完成、部分通过和阻塞项已经转入第 13 节。处理原则：

1. 不删除或美化原始验收证据。
2. 不再阻塞第二期主体任务。
3. 第二期主体优化稳定后集中复测和关闭。
4. 外部契约变化仍按规格 §17 / §20 先记录证据再修改。

### 7.3 继续冻结的边界

- 不要升级 Wails。
- 不要手写短 `base_instructions` 或把官方提示词检入仓库。Codex 目录必须从本机 bundled 模板克隆。
- 不要把选择器别名发给上游，也不要把别名写进 Codex / Grok 目录或四个 Claude 启动环境变量。`/c/claude/v1/models` 的可逆 `claude-gw*` 别名是已冻结的例外。
- 不要在桌面进程里嵌 `/v1/*`。
- 不要把 macOS / Linux 抬到与 Windows 相同的第一期验收等级。
- 不要在没有新证据的情况下重开 §2、§7.3、§7.4、§12、§20 已冻结的结论。

---

## 8. 仓库怎么走

```text
cmd/gateway          无头入口
cmd/desktop          Wails 壳；go:embed assets/；业务只打 /api/v1
internal/app         进程装配
internal/config      唯一配置真相源
internal/secret      钥匙库
internal/route       §7.4 解析
internal/ir          跨协议唯一中介
internal/inbound/*   入站解析与编码
internal/outbound/*  出站生成与解析
internal/server      管理面 + 数据面；serveDataPlane 是唯一数据面管道
internal/logstore    JSONL 与用量
internal/point       指向事务；clientcatalog、tomledit、jsonedit 是叶子包
internal/autostart   当前用户登录启动
desktop/             React 前端；独立 stub go.mod，根模块 ./... 不会走进去
testdata/            脱敏夹具，禁止真实钥匙、账号、个人路径
scripts/             verify / 桌面构建 / 发布 / 交叉构建
docs/v1-scheme.md    合同
docs/install.md      安装
docs/progress.md     本文
```

数据面路径（`internal/server/dataplane.go`）：

1. Content-Type 必须是 JSON，否则 415；体超过 128 MiB 则 413。
2. 只为路由解析 `model` 和 `stream`。
3. `startTrace` 打开 JSONL，设置 `X-Request-Id`。
4. `route.Resolve`：先解码 Claude 选择器别名 → 路由默认 → 空或 `gateway-default` → 命中已配置 provider 前缀则覆盖 → `generic` 唯一已登记归属 → 当前路由已登记模型 → 否则 400 且零上游。含 `/` 的已登记模型名不得报“未知供应商”。
5. 能力门：不支持图片则 422 且零上游；无 `context_management` 则剥离；reasoning 不可表达则删除并记警告。
6. 同协议只改写后原样转发；跨协议经 `ir.Sequencer` 再编码回入站协议。

钥匙库失败映射 **500**，不是 502。上游不可达是 502。能力限制是 422。

---

## 9. 命令

根模块需要 Go 1.26+，工具链锁定 `go1.26.6`，`GOTOOLCHAIN=auto` 会自动拉取。

```powershell
go build ./...
go vet ./...
go test ./...
go test ./internal/route -run TestResolve
go test ./internal/server -run TestCrossProtocol -v
go test -race ./...                  # 需要 cgo + C 编译器
go run ./cmd/gateway serve
go run ./cmd/gateway version

npm --prefix desktop ci
npm --prefix desktop run test
npm --prefix desktop run lint
npm --prefix desktop run build       # 覆盖 cmd/desktop/assets/，该目录已提交并被 embed
npm --prefix desktop run test:e2e
npm --prefix desktop run test:e2e -- --project=desktop-light

.\scripts\verify.ps1                 # §16.3 统一入口；不能跑的步骤必须打印 SKIPPED
.\scripts\build-desktop.ps1 -Version 0.1.0-test
.\scripts\build-release.ps1 -Version 0.1.0-rc1 -Commit <完整提交哈希>
.\scripts\build-cross.ps1  -Version 0.1.0-rc1 -Commit <完整提交哈希>
```

前端改完必须 `npm --prefix desktop run build`，桌面二进制才能看到新界面。

运行时数据根：Windows 是 `%USERPROFILE%\.ai-gateway`，其它系统是 `~/.ai-gateway`，可用 `AI_GATEWAY_DATA_DIR` 覆盖。测试依赖这个覆盖。

发布约定（本仓库已经这样做）：

1. **改完代码必须重新打包。** 凡任务改动了会进入 `ai-gateway.exe` 或 `ai-gateway-desktop.exe` 的内容（Go 源码、`desktop/` 前端、嵌入的 `cmd/desktop/assets/`），任务在打出新的 Windows 发布 zip 之前不算结束。不要等用户再说「重新打包」。只改文档、日记或规格叙述、且不会改变这两个二进制时，不必打包。
2. 工作区干净后再打包，先提交再打，二进制 `-Commit` 必须是真实提交。
3. 同一候选版本的内容更新可以覆盖同名 `0.1.0-rc1` 压缩包，不要擅自改版本号，除非用户明确要求。
4. 打包前确认没有网关或桌面进程占用 `dist/`。
5. 打完用包内 `ai-gateway.exe version` 核对版本、提交、Go、平台；核对压缩包含两个二进制、`LICENSE`、`README.md`、`docs/install.md`。告知用户必须替换并重启已在运行的网关，新包才会生效。

当前包：`dist/ai-gateway-0.1.0-rc1-windows-amd64.zip`  
提交：`ec0dc264a03af6409ff8aa16e1a675f9c7546834`
SHA-256：`C1DC64A4FDE60BA4F9850CF7B7AE2B6F69D1CEE667B0CC512B11D1750ECAAAF8`

---

## 10. 测试约定

- 协议夹具在 `testdata/protocols/{chat,responses,messages}/`，客户端夹具在 `testdata/point/`。测试用 `filepath.Join("..", "..", "testdata", ...)` 引用。
- 夹具禁止真实钥匙、Cookie、账号、个人路径。
- 集成测试用 `httptest.Server` 假上游，断言状态、关键头、事件顺序、工具 id、模型改写、降级警告，不要只断言 200。
- 金样或夹具更新必须属于明确的行为变更任务，禁止为了让测试变绿而批量覆盖。
- 改桌面可见行为时，至少跑 vitest，并在 1440×900 浅色和 390×844 深色下跑 Playwright。没有浏览器工具时写明用了什么替代验证。

---

## 11. 给下一个 Agent 的开工指令

默认任务（用户没有另指定功能时，就做这个）：

> 阅读 `docs/progress.md`、`docs/optimization-roadmap.md` 和 `docs/v1-scheme.md`。第一期已经带遗留问题验收，`G0-02`、`SEC-01` 和 `PERF-01` 已完成；默认按依赖启动 `RES-01`，并继续 `LOG-01`、`OBS-01`。第 13 节遗留问题不阻塞主线，除非用户明确要求处理。不要升级 Wails，不要改变冻结协议来换取测试通过。

若用户指定了功能：先核对本文件第 7.3 节和第 6 节，确认那不是禁区；再读规格对应小节；只改需要改的目录。

改完后用受影响的测试和 `.\scripts\verify.ps1` 验证。不能跑的步骤必须 `SKIPPED`。

---

## 12. §19 验收记录

2026-08-22 已在本机 Windows 11 25H2（构建 26200）使用发布版
`0.1.0-rc1` 执行真实验收。发布版网关进程全程独立运行在
`127.0.0.1:12600`；真实认证信息只通过本机 secret store 使用，未写入本表。
原始严格门禁没有全部通过，原因见第 3、7、8、10、13、17、18 项。2026-08-22 产品所有者根据当前使用效果决定带遗留问题验收；本表保留原始结果，不把未完成项改写成通过。

| 项 | 内容 | 结果 | 证据 |
|---|---|---|---|
| 环境 | Windows / 网关 / 三客户端 / provider 版本 | 通过 | Windows 11 25H2 构建 26200；网关 `0.1.0-rc1`；Codex 0.147.0、Claude Code 2.1.228、Grok 1.0.5；DPAPI secret store 7 个必需 secret 可用 |
| 1 | 只跑 `ai-gateway serve`，不打开桌面 | 通过 | 发布版 `ai-gateway.exe serve` 独立运行；未启动桌面进程 |
| 2 | `healthz` 与 `readyz` 成功 | 通过 | 两个端点均返回 `200` 和 `ok: true` |
| 3 | 添加云 provider；有 Ollama 再加 Ollama | 未完成 | 使用已有云 provider 完成真实请求；本轮未新增 provider，也未安装 Ollama |
| 4 | 分别 point 三个客户端，确认备份 | 通过 | 三个 point 均成功并生成备份；restore 后备份状态清除 |
| 5 | 三个客户端各完成一次流式工具调用 | 通过 | Codex Shell、Claude Bash、Grok terminal 均实际创建并读取临时文件 |
| 6 | 三个请求分别走对应客户端路由 | 通过 | 日志分别确认 Codex→jiandui、Claude→agentrouter、Grok→stysams-grok |
| 7 | 托盘把 Codex 路由切到另一模型 | 未完成 | 已验证同一管理 API 的路由切换；本轮未启动桌面托盘执行真实点击操作 |
| 8 | 再发 Codex 请求，上游已变，配置哈希不变 | 部分通过 | API 路由切换后 Codex 请求确实到新上游，配置哈希不变；因第 7 项未走托盘，托盘链路仍未闭环 |
| 9 | `<已有-provider-id>/<model>` 只覆盖该请求 | 通过 | `agentrouter/gpt-5.6-sol` 请求返回 `200`，下一次默认路由未被永久改写 |
| 10 | 含 `/` 但前缀不是 provider id 的模型名完整转发 | 部分通过 | 临时登记 `vendor/gpt-5.6-sol` 后日志确认完整模型名到达当前 provider；测试模型被上游返回 `503`，未取得成功响应 |
| 11 | 图片发到支持图片的 provider，确认透传 | 通过 | 真实 `1x1 PNG` 请求到图片能力 provider 返回 `200` |
| 12 | 图片发到不支持的 provider，422 且上游零请求 | 通过 | 临时关闭 provider 图片能力后网关返回 `422 unsupported_image`，日志无上游请求；随后恢复配置 |
| 13 | 默认日志有正文，无真实上游认证头 | 部分通过 | 开启正文日志实测正文存在且认证头被省略；当前用户原始配置是关闭日志，未证明默认值本身 |
| 14 | 关日志后新请求不建文件 | 通过 | 关闭日志后请求未新增日志文件或日志字节 |
| 15 | usage 只汇总上游真实 token | 通过 | 真实请求响应 usage 与时间过滤后的 `/api/v1/usage` 聚合值一致，无估算值 |
| 16 | restore 三个客户端，字节和环境变量恢复 | 通过 | 三个客户端 restore 成功；配置文件 SHA-256 与 point 前完全一致，状态均为 `not_pointed` |
| 17 | 打开登录启动，注销再登录 | 阻塞 | `PUT /api/v1/autostart {enabled:true}` 返回 `0x80070005 Access is denied`；未留下计划任务 |
| 18 | 登录后网关启动且能解开 secret | 未完成 | 第 17 项权限失败，不能安全执行注销和重新登录链路 |
| 19 | 占用 12600 后再启动，明确失败且不改端口 | 通过 | 第二个实例退出码 `1`，明确提示已监听，原监听 PID 和端口未变化 |
| 20 | `ai-gateway doctor` 无阻断项 | 通过 | 配置、DPAPI、7 个 secret、日志可写且可解析、客户端还原和无自启动残留均正常 |

产品验收结论：第一期已验收（带遗留问题）。原始二十项结果保持不变，未关闭项转入下方遗留清单，不再阻塞第二期。

第二期当前主线已完成 `SEC-01`、`PERF-01`、`RES-01`、`RES-02`、`SEC-02`、`PERF-02`、`PERF-03`、`LOG-01`、`LOG-02`、`LOG-03`、`OBS-01`、`OBS-02`、`UI-01`、`UI-02`、`UI-03`、`TEST-01`、`TEST-02`、`DOC-01`。模型路由索引基准约为 `11 ns/op、0 B/op、0 allocs/op`；数据面已具备并发、空闲超时、请求体、请求头和单客户端速率边界；日志写入复用请求会话文件句柄，默认递归脱敏常见凭据，脱敏导出和手动清理跳过活动日志，摘要查询按文件元数据增量缓存；三协议跨协议转换已经保留 JSON Schema 结构化输出和严格工具定义；`GET /api/v1/metrics` 提供无正文、无密钥的计数、延迟百分位和 provider 熔断状态；上游连续失败会按提供商短暂熔断；桌面端已完成设置页拆分、按资源增量刷新和日志详情渐进加载。统一验证已通过；竞态测试因当前环境没有 C 编译器明确跳过，Windows 候选发布包按真实提交哈希重新生成。

---

## 13. 第一期遗留问题

| 编号 | 来源 | 状态 | 问题 | 后续关闭标准 |
|---|---|---|---|---|
| `PH1-01` | §19 第 3 项 | 延期 | 本轮使用已有云 provider，没有走新增云 provider 流程，也没有安装 Ollama | 在隔离配置中新增一个真实云 provider，验证探测、保存、请求和删除；有 Ollama 环境时再补本地 provider |
| `PH1-02` | §19 第 7、8 项 | 延期 | 管理 API 路由切换已通过，但没有通过桌面托盘真实点击闭环 | 从托盘切换 Codex 路由，确认下一请求上游变化且 Codex 配置文件哈希不变 |
| `PH1-03` | §19 第 10 项 | 延期 | 含 `/` 的非 provider 前缀模型名已确认完整转发，但测试上游返回 `503` | 使用能成功响应的真实模型重跑并取得成功响应证据 |
| `PH1-04` | §19 第 13 项 | 延期 | 已验证开启正文日志后的内容和认证头省略，但没有用全新数据目录证明默认值 | 在全新临时数据目录启动发布版，确认默认日志和正文开关均为开启 |
| `PH1-05` | §19 第 17、18 项 | 延期、环境阻塞 | 当前非管理员会话创建计划任务返回 `0x80070005`，未完成注销登录和 DPAPI 解钥匙链路 | 在具备计划任务权限的 Windows 用户会话中启用登录启动，注销再登录，确认网关启动并读取密钥，最后关闭登录启动 |

这些问题按产品决策在第二期主体优化完成后集中处理。若其中任一问题在实际使用中造成阻断，应立即提升优先级，不等待集中收尾。
