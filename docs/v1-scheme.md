# ai-gateway v1 实施规格

> 状态：可开发  
> 规格版本：1.0  
> 外部契约核验日期：2026-08-14  
> 第一验收平台：Windows 11  
> 许可证：MIT

本文不是愿景说明，而是 ai-gateway 第一期的工程合同。Agent 可以直接按本文领取任务、创建目录、实现代码和提交测试。

本文使用以下规范词：

- **必须**：第一期验收条件，不得省略。
- **禁止**：第一期不得实现，或不得采用的实现方式。
- **应当**：默认实现方式；只有出现可验证的技术阻断时才能调整。
- **可以**：不影响兼容性和验收的实现自由。

若实现与本文冲突，以本文为准。若外部客户端的当前版本已经改变配置字段或协议，必须先记录复现证据、更新本文的“外部契约”与对应测试，再修改实现；禁止仅凭记忆兼容。

---

## 1. 产品边界

### 1.1 一句话定义

ai-gateway 是一个当前用户会话内运行的本机单用户代理网关。Codex、Claude Code、Grok Build 和兼容 OpenAI 或 Anthropic 协议的本机应用主动把 API 地址指向 `127.0.0.1:12600`，网关统一负责上游钥匙、客户端级路由、协议转换、请求日志和用量汇总。

可选的 Wails 桌面程序只负责控制网关，不承载 `/v1/*` 数据面。

### 1.2 第一阶段必须成立

- 无头网关可以独立安装、启动、停止、诊断和处理请求。
- Codex、Claude Code、Grok Build 各完成一次真实的流式工具调用回合。
- OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 三种入站协议均可使用。
- 三个一等客户端分别拥有自己的当前路由。
- 切换当前路由时，不再修改客户端配置文件。
- 上游钥匙不出现在 `config.yaml`、客户端配置文件、管理 API 响应或正文日志中。
- 指向客户端之前必须备份；还原必须恢复指向前的精确文件内容和相关环境变量状态。
- 网关只监听回环地址；端口冲突时失败，不自动换端口。
- 配置人类可读；许可证为 MIT；没有遥测。
- 桌面关闭后，网关仍可继续运行。

### 1.3 第一阶段明确不做

- 自己实现 Agent 对话界面。
- 多用户、多租户、账号池、负载均衡或请求级故障转移。
- 监听非回环地址、远程访问或管理 API 身份认证。
- 自动同步 MCP、Skills、提示词、会话或客户端供应商目录。
- 修改 Codex 内置模型目录，或劫持客户端二进制。
- 文生图、视觉 sidecar、语音、文件托管。
- 自动端口漂移。
- 自动更新和遥测。
- 日志轮转、日志脱敏、日志大小上限。
- 把 `/v1/*` 服务嵌入 Wails 窗口进程。
- 在 macOS 或 Linux 上承诺与 Windows 相同的第一阶段验收等级。

---

## 2. 已冻结的工程决策

以下决策已经冻结。Agent 不得在普通实现任务中重新选型。

| 主题 | 决策 |
|---|---|
| 后端语言 | Go |
| Go 基线 | `go 1.26`，开发工具链锁定 `go1.26.6` |
| 仓库形式 | 一个 Go module，两个可发布二进制 |
| 无头二进制 | `ai-gateway` |
| 桌面二进制 | `ai-gateway-desktop` |
| 桌面技术栈 | Wails v3、React、TypeScript、Vite |
| Wails 基线 | 精确锁定 `v3.0.0-beta.8`；禁止使用 `master`、nightly 或浮动版本 |
| Node.js 基线 | Node.js `24.19.0` LTS |
| 前端包管理器 | npm，必须提交 `package-lock.json` |
| 默认监听 | `127.0.0.1:12600` |
| 配置格式 | YAML，单一真相源 |
| 管理 API | 与数据面同端口，前缀 `/api/v1` |
| 日志格式 | 每个请求一个追加式 JSONL 文件 |
| 桌面视觉 | 项目内的 Vercel / Geist 视觉约束；简体中文和英文 |
| 第一验收平台 | Windows 11 当前用户会话 |

Wails v3 在核验日期仍为 beta。该风险通过“数据面先行、桌面后置、精确版本锁定、不得边开发边升级”控制。只有独立升级任务可以修改 Wails 版本。若开始任务包 I 时 `v3.0.0-beta.8` 已经无法从官方 Go module 获取，Agent 必须停止桌面初始化、记录命令和错误输出并更新本节，禁止自行换用其他版本。

---

## 3. 交付物和仓库结构

Agent 从空仓库按以下结构创建代码。目录职责也是依赖边界。

```text
ai-gateway/
  cmd/
    gateway/
      main.go
    desktop/
      main.go
  internal/
    app/                  进程装配和生命周期
    config/               YAML 模型、默认值、校验、原子写
    secret/               跨平台钥匙接口和系统实现
    ir/                   协议无关请求、内容块和响应事件
    inbound/
      chat/               OpenAI Chat Completions 入站
      responses/          OpenAI Responses 入站
      messages/           Anthropic Messages 入站
    outbound/
      openaichat/         OpenAI Chat Completions 上游
      openairesponses/    OpenAI Responses 上游
      anthropic/          Anthropic Messages 上游
    route/                客户端路由和请求级模型解析
    server/               HTTP、SSE、管理 API、错误映射
    logstore/             JSONL 正文日志和用量聚合
    point/
      codex/              Codex 指向、检查和还原
      claude/             Claude Code 指向、检查和还原
      grok/               Grok Build 指向、检查和还原
    autostart/            当前用户登录启动
    process/              单实例、PID、启动和停止
    version/              构建版本信息
  desktop/
    package.json
    package-lock.json
    src/
      api/                管理 API 客户端
      components/
      pages/
      i18n/
      styles/
  testdata/
    protocols/
      chat/
      responses/
      messages/
    point/
      codex/
      claude/
      grok/
  docs/
    v1-scheme.md
    test-matrix.md
    install.md
  scripts/
    verify.ps1
  go.mod
  go.sum
  README.md
  LICENSE
```

### 3.1 依赖方向

允许的主要依赖方向：

```text
cmd -> internal/app
internal/app -> config, secret, route, server, logstore, point, autostart, process
server -> inbound, outbound, route, logstore
inbound -> ir
outbound -> ir
point -> config 中的路径工具，但不得依赖 server
desktop -> HTTP /api/v1，不直接导入网关内部包
```

禁止事项：

- `ir` 不得导入任何具体协议包。
- `inbound` 不得直接访问系统钥匙。
- `outbound` 不得自行决定当前路由。
- `desktop` 不得直接修改 `config.yaml` 或三个客户端的配置文件。
- 平台特定代码必须通过构建标签或平台文件隔离，不得在公共文件中散落运行时平台判断。

---

## 4. 运行时目录与文件

数据根目录：

- Windows：`%USERPROFILE%\.ai-gateway`
- macOS 和 Linux：`~/.ai-gateway`

第一阶段固定布局：

```text
~/.ai-gateway/
  config.yaml
  gateway.lock
  gateway.pid.json
  secrets/                    Windows DPAPI 密文文件
  logs/
    2026-08-14/
      <request-id>.jsonl
  backups/
    codex/<UTC timestamp>/
      manifest.json
      config.toml
    claude/<UTC timestamp>/
      manifest.json
      settings.json
    grok/<UTC timestamp>/
      manifest.json
      config.toml
```

约束：

- 所有目录在第一次需要时创建。
- 私有文件应当使用当前平台能提供的最严格当前用户权限。
- `gateway.lock` 用于单实例互斥。
- `gateway.pid.json` 仅是诊断元数据，不得单独作为进程仍存活的依据。
- 临时文件必须创建在目标文件同目录，以保证改名原子性。

---

## 5. 配置合同

配置文件是唯一持久化业务配置。桌面和 CLI 不得维护第二份配置。

### 5.1 完整示例

```yaml
version: 1

listen:
  port: 12600

logging:
  enabled: true
  dir: logs

ui:
  language: zh-CN
  logging_notice_accepted: false

autostart:
  enabled: false

providers:
  openrouter:
    name: OpenRouter
    adapter: openai-chat
    base_url: https://openrouter.ai/api/v1
    default_model: anthropic/claude-sonnet-4
    models:
      - id: anthropic/claude-sonnet-4
        name: Claude Sonnet 4
        context_window: 200000
        max_output_tokens: 64000
    secret_ref: provider.openrouter
    capabilities:
      image_input: true
      reasoning: true
  ollama:
    name: Ollama
    adapter: openai-chat
    base_url: http://127.0.0.1:11434/v1
    default_model: qwen3
    capabilities:
      image_input: false
      reasoning: false

routes:
  codex:
    provider: openrouter
    model: anthropic/claude-sonnet-4
  claude:
    provider: openrouter
    model: anthropic/claude-sonnet-4
  grok:
    provider: openrouter
    model: anthropic/claude-sonnet-4
  generic:
    provider: ollama
    model: qwen3
```

### 5.2 字段规则

#### 顶层

- `version`：必须等于 `1`。
- 未知顶层字段：读取时保留，写回时不得主动删除。
- 缺失的可选字段使用默认值，但首次读取不得仅因补默认值而重写文件。

#### `listen`

- `port`：整数，范围 `1024..65535`，默认 `12600`。
- hostname 不进入配置，始终是 `127.0.0.1`。

#### `logging`

- `enabled`：默认 `true`。
- `dir`：默认 `logs`；相对路径相对数据根目录解析。
- 第一阶段不接受日志级别、轮转或脱敏字段。

#### `providers`

- provider id 必须匹配 `^[a-z][a-z0-9_-]{0,31}$`。
- `name`：非空显示名称。
- `adapter`：只允许 `openai-chat`、`openai-responses`、`anthropic`。
- `base_url`：必须是绝对 HTTP 或 HTTPS URL，不得包含查询字符串或片段。
- `default_model`：非空。
- `models`：可选模型目录；每项包含非空且在同一 provider 内唯一的 `id`，以及可选 `name`、`context_window` 和 `max_output_tokens`。
- `context_window` 和 `max_output_tokens`：分别是非负整数；`0` 表示上游未提供且用户尚未填写，不得按模型名称推测。协议不假定两个字段之间存在固定大小关系。
- 当 `models` 非空时，`default_model` 必须引用目录中的模型。旧配置没有 `models` 时继续兼容。
- `secret_ref`：可选；需要认证的供应商必须设置。
- `capabilities.image_input`：布尔值，默认 `false`。
- `capabilities.reasoning`：布尔值，默认 `false`。
- `capabilities.context_management`：布尔值，默认 `false`。为兼容未实现 Anthropic 上下文管理扩展的第三方供应商，未启用时网关会移除请求中的 `context_management` 并记录 `context_management_dropped` 警告。
- 删除仍被路由引用的 provider 必须返回冲突错误，不得级联修改路由。

#### `routes`

- 固定包含 `codex`、`claude`、`grok`、`generic`。
- `provider` 必须引用已存在的 provider id。
- `model` 必须非空。

### 5.3 配置写入

写入流程必须是：

1. 在内存中应用修改。
2. 完整校验新配置。
3. 写入目标目录内的唯一临时文件。
4. 刷新文件内容。
5. 原子替换 `config.yaml`。
6. 必要时刷新父目录元数据。
7. 只有替换成功后，才更新进程内配置快照。

并发写入必须由进程内互斥锁串行化。

### 5.4 配置错误

配置不存在时，`serve` 必须生成默认配置后继续启动。

配置存在但无法解析或校验失败时：

- `serve` 必须退出。
- 不得覆盖原文件。
- stderr 必须包含文件路径和可定位字段。
- `doctor` 必须报告同一错误。

### 5.5 Provider 填表预设

预设只负责在桌面中预填 `adapter` 和 `base_url`，不是运行时特殊分支。用户保存后得到普通 provider 配置，之后所有请求必须走统一 adapter。

| 预设 | adapter | base_url |
|---|---|---|
| OpenRouter | `openai-chat` | `https://openrouter.ai/api/v1` |
| DeepSeek | `openai-chat` | `https://api.deepseek.com` |
| Ollama | `openai-chat` | `http://127.0.0.1:11434/v1` |
| OpenAI | `openai-responses` | `https://api.openai.com/v1` |
| Anthropic | `anthropic` | `https://api.anthropic.com` |
| xAI | `openai-responses` | `https://api.x.ai/v1` |

预设不得填入 API key、默认模型或能力开关。模型和能力与用户实际选择有关，必须由用户确认或通过 provider probe 得到可验证结果。

---

## 6. 钥匙合同

### 6.1 接口

`internal/secret` 必须暴露等价于以下语义的接口：

```go
type Store interface {
    Put(ctx context.Context, ref string, value []byte) error
    Get(ctx context.Context, ref string) ([]byte, error)
    Delete(ctx context.Context, ref string) error
    Available(ctx context.Context) error
}
```

规则：

- `ref` 使用配置中的 `secret_ref`。
- `Get` 返回的新字节切片由调用方负责尽快清零。
- 错误必须区分“不存在”和“系统存储不可用”。
- API key 只能通过写接口进入，任何读接口都不得回传明文。

### 6.2 平台实现

- Windows：使用当前用户 DPAPI 加密，密文写入 `~/.ai-gateway/secrets/`。
- macOS：使用当前用户 Keychain。
- Linux：使用当前用户 Secret Service。

若当前平台的系统存储不可用：

- 需要钥匙的 provider 操作必须失败。
- 网关若存在需要钥匙的已配置 provider，启动必须失败并给出修复说明。
- 禁止退回明文 YAML、环境变量文件或桌面本地存储。

### 6.3 Provider 更新事务

新增或修改 provider 且请求中包含新钥匙时：

1. 校验 provider 字段。
2. 写入系统钥匙存储。
3. 原子写配置。
4. 若配置写入失败，恢复旧钥匙；若无法恢复，返回明确的部分失败错误并由 `doctor` 报告。

删除 provider 时，先确认没有路由引用，再删除配置，最后删除钥匙。钥匙删除失败不得恢复已删除 provider，但必须返回警告并由 `doctor` 报告孤儿钥匙引用。

---

## 7. 客户端、路径和模型路由

### 7.1 客户端标识

内部只允许四个客户端标识：

```text
codex
claude
grok
generic
```

### 7.2 入站路径

| 调用方 | 写入客户端的 base URL | 网关路径 |
|---|---|---|
| Codex | `http://127.0.0.1:12600/c/codex/v1` | `/c/codex/v1/responses` |
| Claude Code | `http://127.0.0.1:12600/c/claude` | `/c/claude/v1/messages` |
| Grok Build | `http://127.0.0.1:12600/c/grok/v1` | `/c/grok/v1/responses`，并兼容另外两种协议 |
| 通用应用 | `http://127.0.0.1:12600/v1` | `/v1/chat/completions`、`/v1/responses`、`/v1/messages` |

所有协议端点也必须支持：

```text
/c/{client}/v1/chat/completions
/c/{client}/v1/responses
/c/{client}/v1/messages
/c/{client}/v1/models
```

其中 `{client}` 只能是固定标识。无前缀 `/v1/*` 等价于 `/c/generic/v1/*`。

### 7.3 保留模型名与首选模型

`gateway-default` 是网关保留的模型名，含义是“使用该客户端当前路由中的默认模型”。

客户端路由的语义是**该客户端的启动首选模型**，不是该客户端唯一可用的模型。
进入 agent 之后，用户仍然可以选择任意已启用的 `<provider-id>/<model-id>`：
完整目录由 `/c/{client}/v1/models` 提供（§7.5），这是三个一等客户端唯一共同
可用的入口。请求携带的模型名按 §7.4 解析，与该客户端的路由无关。

三个一等客户端执行 point 后，写入的首选模型必须是 `gateway-default`，或当前路由
对应的 `<provider-id>/<model-id>`。两种形式都必须能被 §7.4 正确解析。

客户端配置文件中落地完整目录只在客户端原生支持时进行。依据 §20 的 2026-08-15
复核结论，第一阶段只有 Grok Build 落地完整目录；Codex 与 Claude Code 只写首选
模型，因为在这两个客户端里塞入完整目录会替换客户端自身的系统提示词，或被客户端
的 id 过滤规则丢弃大部分条目。

管理面切换客户端路由时只影响下一次请求。切换路由不得替换最初 point 创建的恢复点。
若某客户端配置中写的是 `gateway-default`，切换路由不得改写该客户端配置文件。

### 7.4 路由算法

输入为客户端、请求中的模型字符串和配置快照。必须按以下顺序解析：

1. 读取该客户端的当前路由，得到 `route.provider` 和 `route.model`。
2. 若请求模型为空或等于 `gateway-default`，使用当前路由的 provider 和 model。
3. 否则，若请求模型形如 `<prefix>/<rest>`，且 `<prefix>` 正好命中一个已配置 provider id，则本次请求使用该 provider，模型为 `<rest>`。
4. 否则使用当前路由的 provider，并把请求模型完整地作为上游模型名。

第四步不得因为模型名包含 `/` 而报“未知供应商”。例如当前路由指向 OpenRouter 时，`anthropic/claude-sonnet-4` 是合法的上游模型名。

请求级 provider 覆盖只在前缀命中已配置 provider 时生效。客户端或调用方若需要确定性地覆盖，必须使用已存在的 provider id。

### 7.5 模型列表

`GET /v1/models` 和客户端前缀版本必须至少返回：

- `gateway-default`。
- 每个 provider 持久化模型目录中的模型，id 为 `<provider-id>/<model-id>`；旧配置没有目录时至少返回 `<provider-id>/<default-model>`。

若管理面成功从某个 provider 拉取模型列表，返回值中的模型 id 必须加 `<provider-id>/` 前缀。不同 provider 的同名模型不得互相覆盖。

模型列表拉取失败不得影响数据面启动。

每项必须带 `display_name`，取值必须等于该项的 `id`（`gateway-default` 或
`<provider-id>/<model-id>`）。客户端选择器按 `供应商/模型 ID` 展示全部已启用
模型。模型目录中的 `name` 只用于网关管理面，不得改写客户端选择器标签。
Claude Code 的网关模型发现读取 `display_name`，Grok Build 的选择器 `name`
字段同样写成该 id（证据见 §20）。

被禁用的 provider 和被禁用的模型不得出现在列表中。

---

## 8. 协议内部模型

### 8.1 请求 IR

`internal/ir` 必须能够无损表达第一阶段承诺的公共语义：

```go
type Request struct {
    ID              string
    Client          ClientID
    InboundProtocol Protocol
    Model           string
    Stream          bool
    System          []Block
    Messages        []Message
    Tools           []Tool
    ToolChoice      json.RawMessage
    Reasoning       ReasoningConfig
    Extensions      map[string]json.RawMessage
}

type Message struct {
    Role    Role
    Content []Block
}

type Block struct {
    Type       BlockType
    Text       string
    Image      *Image
    Reasoning  *Reasoning
    ToolCall   *ToolCall
    ToolResult *ToolResult
}
```

必须支持的块类型：

- 文本。
- 图片 URL。
- 图片 base64。
- reasoning 或 thinking。
- 工具调用，包含稳定 id、名称和 JSON 参数。
- 工具结果，包含对应工具调用 id、文本或结构化内容、错误状态。

IR 不是公开 API，不要求长期向后兼容；但同一切片内所有 adapter 必须只通过 IR 做跨协议转换。

### 8.2 响应事件 IR

流式和非流式响应统一为事件序列：

```text
response.started
reasoning.delta
reasoning.completed
text.delta
text.completed
tool_call.started
tool_call.arguments.delta
tool_call.completed
usage
response.completed
error
```

约束：

- 工具调用 id 在整个转换链中保持稳定。
- 参数增量必须按到达顺序拼接。
- `response.completed` 只出现一次。
- 上游错误后不得再发成功完成事件。
- 客户端断开连接必须取消上游请求。

### 8.3 同协议与跨协议

同协议请求：

- 必须解析路由所需字段。
- 必须重写上游模型和认证。
- 应当保留未知 JSON 字段。
- 可以采用原协议结构直通，减少语义损失。

跨协议请求：

- 必须先转为 IR，再由出站 adapter 生成上游请求。
- 禁止把 Chat Completions 结构直接当作内部标准。

### 8.4 能力降级

- 请求含图片而 provider 的 `capabilities.image_input` 为 `false` 时，网关必须在调用上游之前返回 422。
- 请求含 reasoning，而目标协议或 provider 无法表达时，可以丢弃，但必须在该请求日志中写入 `reasoning_dropped` 警告。
- 工具定义或工具结果无法可靠转换时必须返回 422，禁止静默删除。
- 未识别的供应商扩展字段在跨协议时可以丢弃，但必须记录字段名，不得记录钥匙。

---

## 9. HTTP 和 SSE 行为

### 9.1 数据面端点

必须实现：

```text
POST /v1/chat/completions
POST /v1/responses
POST /v1/messages
POST /c/{client}/v1/chat/completions
POST /c/{client}/v1/responses
POST /c/{client}/v1/messages
GET  /v1/models
GET  /c/{client}/v1/models
GET  /healthz
GET  /readyz
```

### 9.2 健康检查

`GET /healthz`：

- 只说明进程和 HTTP 循环仍存活。
- 成功返回 200 和 `{"status":"ok"}`。
- 不访问上游。

`GET /readyz`：

- 校验配置可用、系统钥匙存储可用、必需 secret 存在。
- 成功返回 200。
- 未就绪返回 503 和错误列表。
- 不对所有上游发真实模型请求。

### 9.3 请求限制

- 只接受 JSON 请求体。
- 最大请求体为 128 MiB；超过返回 413。
- 无法解析的 JSON 返回 400。
- 未知协议路径返回 404。
- 客户端前缀非法返回 404。
- 第一阶段不对回环请求鉴权；传入的占位 API key 被忽略。

### 9.4 SSE

- 流式响应必须在每个可发送事件后 flush。
- 禁止把完整上游响应缓存完再伪装成流式返回。
- 不设置会截断长时间推理的整体 `WriteTimeout`。
- 上游 SSE 断流且协议没有可安全恢复的位置时，直接结束并返回协议对应错误；第一阶段不自动续传。
- 非流式请求可以消费同一事件管线后聚合为最终响应。

### 9.5 错误模型

管理 API 统一错误：

```json
{
  "error": {
    "code": "config_invalid",
    "message": "providers.openrouter.base_url must be an absolute URL",
    "details": {
      "field": "providers.openrouter.base_url"
    },
    "request_id": "req_..."
  }
}
```

数据面错误必须使用入站协议的原生错误外形，并保留合理的 HTTP 状态码：

| 情况 | 状态码 |
|---|---:|
| JSON 或字段格式错误 | 400 |
| provider 或 route 不存在 | 400 |
| 请求体过大 | 413 |
| 图片、工具或协议能力不支持 | 422 |
| 上游认证失败 | 401 或上游状态码 |
| 上游限流 | 429 |
| 上游超时 | 504 |
| 上游不可达 | 502 |
| 内部错误 | 500 |

上游返回非成功状态时：

- 同协议应当尽可能保留状态码与错误体。
- 跨协议必须映射成入站协议错误。
- 日志中记录上游状态码和原始错误正文。

---

## 10. 上游 adapter

固定三类：

- `openai-chat`：`POST <base_url>/chat/completions`
- `openai-responses`：`POST <base_url>/responses`
- `anthropic`：`POST <base_url>/v1/messages`

URL 拼接必须避免重复 `/v1` 或重复斜杠。`base_url` 语义以配置预设为准，不得通过字符串猜测供应商。

### 10.1 认证

- OpenAI 类 adapter 默认发送 `Authorization: Bearer <secret>`。
- Anthropic adapter 默认发送 `x-api-key: <secret>` 和实现所需的稳定 `anthropic-version`。
- 本地无钥匙 provider 不发送认证头。
- 入站客户端的 Authorization、x-api-key 或占位 key 不得转发上游。

若未来需要自定义头，必须新增明确配置合同；第一阶段禁止把任意客户端头透传给上游。

### 10.2 HTTP 客户端

- 每个 provider 可以复用连接池。
- 必须设置连接、TLS 握手和响应头超时。
- 不得给整个流式响应设置固定总时长。
- 必须遵循请求 context 取消。
- 第一阶段不得自动切换 provider。

---

## 11. 管理 API 合同

所有端点位于 `/api/v1`，只监听回环，不鉴权。

### 11.1 状态与生命周期

```text
GET  /api/v1/status
POST /api/v1/shutdown
GET  /api/v1/doctor
```

`GET /status` 至少返回：

```json
{
  "version": "0.1.0-dev",
  "pid": 1234,
  "listen": "127.0.0.1:12600",
  "logging_enabled": true,
  "autostart_enabled": false,
  "clients": {
    "codex": {"point_state": "pointed"},
    "claude": {"point_state": "drifted"},
    "grok": {"point_state": "not_pointed"}
  },
  "routes": {
    "codex": {
      "provider": "openrouter",
      "model": "anthropic/claude-sonnet-4"
    }
  }
}
```

`point_state` 只允许：

- `pointed`
- `not_pointed`
- `drifted`
- `client_not_installed`
- `unknown`

`POST /shutdown` 在返回 202 后发起优雅关闭：

1. 停止接受新请求。
2. 给进行中请求最多 30 秒完成。
3. 取消剩余请求。
4. 关闭日志文件和锁。
5. 退出进程。

### 11.2 配置

```text
GET /api/v1/config
PUT /api/v1/config
```

- GET 不返回任何 secret 明文。
- PUT 必须进行完整校验和原子写。
- PUT 不接受 secret 明文；钥匙通过 provider 接口写入。

### 11.3 Provider

```text
GET    /api/v1/providers
POST   /api/v1/providers
GET    /api/v1/providers/{id}
PUT    /api/v1/providers/{id}
DELETE /api/v1/providers/{id}
POST   /api/v1/providers/{id}/probe
GET    /api/v1/providers/{id}/models
POST   /api/v1/provider-models/discover
```

创建和更新请求可以包含写入后即丢弃的 `api_key` 字段。响应只返回：

```json
{
  "id": "openrouter",
  "name": "OpenRouter",
  "adapter": "openai-chat",
  "base_url": "https://openrouter.ai/api/v1",
  "default_model": "anthropic/claude-sonnet-4",
  "models": [
    {
      "id": "anthropic/claude-sonnet-4",
      "name": "Claude Sonnet 4",
      "context_window": 200000,
      "max_output_tokens": 64000
    }
  ],
  "has_secret": true,
  "capabilities": {
    "image_input": true,
    "reasoning": true
  }
}
```

`probe` 必须进行最小真实上游请求或模型列表请求，并返回耗时、状态和可读错误；不得自动保存任何配置修改。

`POST /api/v1/provider-models/discover` 接收尚未保存的 `provider_id`、
`adapter`、`base_url` 和可选 `api_key`，供管理端在创建 provider 时获取模型。
编辑已有 provider 且 `api_key` 为空时使用已保存密钥。发现结果不得保存 provider、
密钥或污染数据面模型缓存。上游明确发布的 `context_length`、
`max_completion_tokens` 等元数据映射到模型目录；缺失字段返回零值，禁止推测。
用户保存时可以覆盖这些值。上下文和输出令牌数目前仅是管理元数据，不代表数据面
已实施请求裁剪或上限校验。

### 11.4 路由

```text
PUT /api/v1/routes/{client}
```

请求：

```json
{
  "provider": "openrouter",
  "model": "anthropic/claude-sonnet-4"
}
```

成功后下一次请求立即使用新配置。不得修改客户端文件。

### 11.5 指向和还原

```text
GET  /api/v1/clients/{client}
POST /api/v1/clients/{client}/point
POST /api/v1/clients/{client}/restore
```

`point` 和 `restore` 的详细事务见第 12 节。

### 11.6 日志和用量

```text
GET /api/v1/logs
GET /api/v1/logs/{request_id}
GET /api/v1/usage
PUT /api/v1/logging
```

最低查询参数：

- `from`、`to`：RFC 3339 时间。
- `client`
- `provider`
- `status`
- `limit`：默认 100，最大 500。
- `cursor`

`GET /logs` 返回摘要，不默认返回完整正文。`GET /logs/{request_id}` 返回完整 JSONL 解析结果。

### 11.7 登录启动

```text
PUT /api/v1/autostart
```

请求：

```json
{"enabled": true}
```

---

## 12. 三个客户端的 point / restore

### 12.1 通用事务

point 必须：

1. 定位用户级客户端配置。
2. 读取原始字节；不存在时记录 `original_exists: false`。
3. 解析并验证当前格式。
4. 创建新的 UTC 备份目录。
5. 写入原始文件和 `manifest.json`。
6. 生成修改后的完整内容。
7. 在目标目录原子替换客户端配置。
8. 修改需要的用户环境变量，并在 manifest 中记录旧状态。
9. 重新读取并检查客户端是否已指向。
10. 只有全部成功后才返回成功。

任一步骤失败时必须回滚已经完成的修改。回滚失败必须返回部分失败，并提供备份目录。

restore 必须：

1. 选择最近一次尚未还原的成功 point manifest。
2. 恢复原始文件字节；原文件不存在时删除由 point 创建的文件。
3. 恢复环境变量原值或删除由 point 新增的变量。
4. 标记 manifest 已还原。
5. 重新检查客户端状态。

重复 point：

- 若当前文件已经与本次目标一致，返回成功且不再创建备份。
- 若存在人工改动，状态为 `drifted`；新的 point 必须先创建新备份，再重新应用。

重复 restore：

- 没有可还原 manifest 时返回 409 和可读说明。

客户端配置重新序列化可以改变空白和键顺序，但必须保留未知字段及其语义；备份必须保留精确原始字节。

### 12.2 Backup manifest

最低结构：

```json
{
  "version": 1,
  "client": "codex",
  "created_at": "2026-08-14T08:00:00Z",
  "completed": true,
  "restored_at": null,
  "files": [
    {
      "target": "C:\\Users\\name\\.codex\\config.toml",
      "backup": "config.toml",
      "original_exists": true,
      "original_sha256": "..."
    }
  ],
  "environment": [
    {
      "name": "AI_GATEWAY_PLACEHOLDER_KEY",
      "original_exists": false,
      "original_value": null
    }
  ]
}
```

manifest 中可以保存指向前的占位环境变量值，因为该值不是上游钥匙；不得保存真实 provider secret。

### 12.3 Codex

用户配置：

- Windows：`%USERPROFILE%\.codex\config.toml`
- macOS 和 Linux：`~/.codex/config.toml`

写入语义：

```toml
model_provider = "ai-gateway"
model = "gateway-default"

[model_providers.ai-gateway]
name = "ai-gateway"
base_url = "http://127.0.0.1:12600/c/codex/v1"
wire_api = "responses"
env_key = "AI_GATEWAY_PLACEHOLDER_KEY"
```

规则：

- 禁止覆盖保留 id `openai`、`ollama` 或 `lmstudio`。
- `wire_api` 必须是 `responses`；当前 Codex 自定义 provider 不支持其他值。
- 用户环境变量 `AI_GATEWAY_PLACEHOLDER_KEY` 必须是任意非空占位值。
- `model` 固定为 `gateway-default`，即 §7.3 的启动首选模型。
- 禁止写入 `model_catalog_json`。该键确实可用，但自定义目录会整体替换 Codex 内置目录，
  并且条目的 `base_instructions` / `model_messages` 会替换 Codex 真正的系统提示词
  （实测 21178 字符降到 32 字符，克隆内置条目后降到 0），证据见 §20。
- 进入 agent 后选择其它已启用模型的方式是 `codex -m <provider-id>/<model-id>`，
  完整目录由 `/c/codex/v1/models` 提供。实测该形式可直接使用，上游收到的模型名逐字一致，
  仅多打印一行模型元数据缺失警告。
- 若环境变量原本存在，restore 必须恢复原值。
- `doctor` 必须检查 provider 块、base URL、wire API、模型和环境变量。

### 12.4 Claude Code

用户配置：

- 默认：`~/.claude/settings.json`
- 若当前环境存在 `CLAUDE_CONFIG_DIR`，必须先按官方当前行为确认其用户配置位置；无法确认时不得修改。

合并以下 `env` 字段，不得覆盖 `env` 中无关变量：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:12600/c/claude",
    "ANTHROPIC_API_KEY": "sk-ai-gateway-local",
    "ANTHROPIC_MODEL": "gateway-default",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "gateway-default",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gateway-default",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gateway-default",
    "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1"
  }
}
```

规则：

- `ANTHROPIC_BASE_URL` 不带 `/v1`。
- 四个模型环境变量固定为 `gateway-default`，即 §7.3 的启动首选模型。
- `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` 置为 `1`，让 Claude Code 启动时请求
  `/c/claude/v1/models?limit=1000` 并把结果加入模型选择器。
- 必须在指向说明中写明该发现机制的客户端侧限制：Claude Code 只保留 id 匹配
  `/(claude|anthropic)/i` 的条目，其余一律丢弃，因此选择器不会显示全部已启用模型。
  这是客户端行为，不得通过改写模型 id 去迎合它。证据见 §20。
- 完整目录仍由 `/c/claude/v1/models` 提供；`claude --model <provider-id>/<model-id>`
  可以直接使用任意已启用模型，实测上游收到的模型名逐字一致。
- 只改用户级配置，禁止改项目内 `.claude/settings.json`。
- 指向说明必须明确：自定义非 Anthropic base URL 会禁用 Remote Control。
- 指向说明必须明确：MCP tool search 在非第一方 host 下默认关闭或降级，行为取决于当前 Claude Code 版本和管理设置。
- `doctor` 必须检查 JSON 可解析、目标字段和值正确。

### 12.5 Grok Build

用户配置：

- 默认：`~/.grok/config.toml`
- Windows：`%USERPROFILE%\.grok\config.toml`
- `GROK_HOME` 存在时使用其目录。

写入语义：

```toml
[models]
default = "ai-gateway"

[model."ai-gateway"]
model = "gateway-default"
base_url = "http://127.0.0.1:12600/c/grok/v1"
name = "gateway-default"
api_backend = "responses"
api_key = "sk-ai-gateway-local"

[model."ai-gateway:openrouter/anthropic/claude-sonnet-4"]
model = "openrouter/anthropic/claude-sonnet-4"
base_url = "http://127.0.0.1:12600/c/grok/v1"
name = "openrouter/anthropic/claude-sonnet-4"
api_backend = "responses"
api_key = "sk-ai-gateway-local"
```

规则：

- `api_backend` 第一阶段固定为 `responses`。
- `[model."ai-gateway"]` 是 §7.3 的启动首选模型，由 `[models] default` 指向。
- Grok Build 是唯一能在配置文件里承载完整目录的客户端：每个已启用的
  `<provider-id>/<model-id>` 各写一个 `[model."ai-gateway:<provider-id>/<model-id>"]`
  条目，与内置模型并存而非替换（证据见 §20）。
- 选择器显示名 `name` 必须写成该条目的模型 id（`gateway-default` 或
  `<provider-id>/<model-id>`），不得改写成目录中的友好名称。
- 网关写入的条目一律以 `ai-gateway:` 为 id 前缀，除首选模型条目 `ai-gateway` 之外。
  restore 必须删除网关写入的全部条目，不得删除用户自己声明的模型。
- 目录条目的增删只发生在 point 与路由同步时，且不得替换最初 point 创建的恢复点。
- 不修改项目级 `.grok/config.toml`。
- 保留现有其他模型、MCP、插件、权限和 UI 配置。
- `doctor` 应当提示用户可以运行 `grok inspect` 验证配置来源。

### 12.6 通用应用

不自动修改文件。安装文档只提供：

```text
Base URL: http://127.0.0.1:12600/v1
API key: 任意非空占位值
Model: gateway-default，或 provider-id/model-name
```

---

## 13. 日志和用量

### 13.1 日志开关

- `logging.enabled` 默认 `true`。
- `false` 时，新请求不得创建正文日志文件。
- 关闭日志不删除历史文件。
- 首次打开桌面必须确认风险说明后才进入主界面。
- 无头 `serve` 每次启动在 stderr 输出一次风险说明。

固定风险文案：

> ai-gateway 的正文日志可能包含提示词、源代码、工具参数、工具结果和图片原文。日志不会自动轮转或脱敏，请仅在可信设备上启用并自行管理磁盘空间。

### 13.2 JSONL 格式

每个请求一个文件：

```text
logs/<local-date>/<request-id>.jsonl
```

事件最低类型：

```text
request
route
upstream_request
upstream_event
client_event
warning
result
```

每行必须是独立 JSON 对象，至少包含：

- `timestamp`
- `request_id`
- `type`

要求：

- `request` 记录入站协议、客户端、方法、完整地址、路径、查询参数、HTTP 版本、主机、远端地址、内容长度、传输编码、非敏感请求头、尾部字段和原始 JSON 正文。
- `route` 记录最终 provider、model 和 adapter。
- `upstream_request` 记录去除认证头后的 URL、方法、实际非敏感请求头和正文。
- 流式时逐条追加 `upstream_event` 与 `client_event`。
- `warning` 记录 reasoning 或扩展字段降级。
- `result` 记录状态、耗时、用量、错误和完成时间。
- 不记录 Authorization、x-api-key、Cookie、密码、令牌、会话标识或系统 secret；事件使用 `omitted_sensitive_header_count` 和 `omitted_sensitive_query_count` 表示被省略的敏感字段数量。
- 文件写入必须串行且可在异常结束时保留已有事件。

### 13.3 用量

第一阶段从日志中的 `result.usage` 汇总：

- 输入 token。
- 输出 token。
- reasoning token，若上游提供。
- 总请求数。
- 成功、失败和取消数。
- 按 provider、model、client、日期分组。

没有日志或上游没有 usage 时，返回 `null` 或“不完整”标志，禁止估算 token。

### 13.4 doctor

必须报告：

- 日志是否启用。
- 日志目录是否可写。
- 当前总大小。
- 最近一个日志文件是否可解析。
- 是否存在没有 `result` 事件的中断请求。

---

## 14. 进程、CLI 和登录启动

### 14.1 CLI

必须实现：

```text
ai-gateway serve
ai-gateway stop
ai-gateway status
ai-gateway doctor
ai-gateway autostart on
ai-gateway autostart off
ai-gateway version
```

语义：

- `serve`：前台运行，写 stderr，不自行转后台。
- `stop`：请求本机管理 API 优雅关闭；未运行时返回可读状态。
- `status`：优先请求管理 API；不可达时结合锁和 PID 元数据诊断。
- `doctor`：即使网关未运行也能检查配置、目录、客户端配置和系统钥匙存储。
- `version`：输出版本、提交号、构建时间、Go 版本。

统一退出码：

| 退出码 | 含义 |
|---:|---|
| 0 | 成功 |
| 1 | 一般运行错误 |
| 2 | 参数错误 |
| 3 | 配置错误 |
| 4 | 网关未运行或不可达 |
| 5 | 部分成功，需要人工处理 |

### 14.2 单实例

- 启动时获取 `gateway.lock` 的独占锁。
- 已有活跃实例时，新实例退出并打印现有监听地址。
- 仅凭陈旧 PID 文件不得拒绝启动。
- 退出时释放锁；异常退出后的锁必须可由下一实例安全恢复。

### 14.3 信号

- Windows 控制台关闭、Ctrl+C、服务关闭事件应触发优雅关闭。
- macOS 和 Linux 的 SIGINT、SIGTERM 触发同一关闭流程。

### 14.4 登录启动

默认关闭：

- Windows：当前用户计划任务，登录时执行安装目录中的 `ai-gateway serve`。
- macOS：用户级 launchd。
- Linux：用户级 systemd。

规则：

- 禁止创建 SYSTEM、root 或系统级服务。
- 开启后必须读取并验证已注册任务。
- 关闭或卸载必须移除任务。
- 可执行文件路径含空格时必须正确转义。
- 安装目录变化后 `doctor` 必须报告旧路径。

---

## 15. 桌面控制台

### 15.1 运行边界

- 桌面通过 `http://127.0.0.1:<port>/api/v1` 控制网关。
- 桌面不监听数据面端口。
- 网关未运行时，桌面可以启动已安装的 `ai-gateway serve` 进程。
- 停止网关通过管理 API 完成。
- 关闭窗口不停止网关。
- 退出托盘不等于停止网关。

### 15.2 页面

第一阶段必须有：

1. 首次风险说明。
2. 总览：网关状态、监听地址、日志和登录启动状态。
3. Providers：列表、新增、编辑、删除、探测、模型列表。
4. Routes：Codex、Claude Code、Grok Build、Generic 的当前 provider 和 model。
5. Clients：检查、point、restore、漂移状态和影响说明。
6. Logs：摘要列表、正文详情、筛选。
7. Usage：按日期、客户端、provider 和 model 的粗汇总。
8. Settings：端口、日志开关、登录启动和语言。

### 15.3 托盘

必须提供：

- 显示网关运行状态。
- 打开主窗口。
- 分别切换三个一等客户端的当前路由。
- 开关正文日志。
- 启动或停止网关。
- 退出桌面。

托盘切换路由只能调用 `PUT /api/v1/routes/{client}`。

### 15.4 视觉和无障碍

- 简体中文和英文必须完整覆盖，不允许界面中混入未翻译 key。
- 默认跟随系统明暗主题，并允许用户显式选择。
- 使用 Geist 或系统无衬线字体；代码和精确标识符使用等宽字体。
- 内容层级优先，避免卡片墙、装饰渐变和无意义阴影。
- 键盘可以完成新增 provider、切路由、point、restore、开关日志。
- 焦点可见，状态不能只靠颜色表达。
- 最低目标为 WCAG 2.2 AA。

---

## 16. 测试策略

### 16.1 测试层级

#### 单元测试

必须覆盖：

- 配置默认值、校验、原子写失败。
- 路由算法全部分支。
- IR 内容块和事件状态机。
- 三种协议的解析和生成。
- 错误映射。
- 日志 JSONL 写入和中断恢复。
- point/restore 的不存在文件、已有文件、漂移和回滚。

#### 合同测试

`testdata/protocols` 保存脱敏后的真实协议夹具。每种协议至少包含：

- 非流式文本。
- 流式文本。
- 单工具调用。
- 工具结果续轮。
- 多工具调用。
- 图片输入。
- reasoning 或 thinking。
- 上游 4xx。
- 上游 5xx。
- 流中断。

合同测试必须同时断言：

- HTTP 状态。
- 关键头。
- 事件顺序。
- 工具调用 id。
- 模型重写。
- 不能表达字段的警告。

#### 集成测试

使用 `httptest.Server` 或独立 fake upstream，覆盖：

- 三种同协议直通。
- Chat 到 Messages。
- Messages 到 Chat。
- Chat 到 Responses。
- Responses 到 Chat。
- Messages 到 Responses。
- Responses 到 Messages。
- 客户端取消后上游 context 被取消。
- 并发请求不串路由、不串日志。

#### 真实客户端验收

真实 Codex、Claude Code、Grok Build 的测试不能由 mock 替代。执行版本、命令、结果和脱敏抓包摘要写入 `docs/test-matrix.md`。

### 16.2 测试数据规则

- 夹具不得包含真实 API key、Cookie、账户 id、私人代码或个人路径。
- 从真实流量生成夹具时必须先脱敏，再进入仓库。
- golden 文件更新必须由行为变更任务显式完成，禁止测试失败时无审查批量覆盖。

### 16.3 统一验证命令

根目录 `scripts/verify.ps1` 最终必须依次执行：

```powershell
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
npm --prefix desktop ci
npm --prefix desktop run lint
npm --prefix desktop run test
npm --prefix desktop run build
```

在桌面切片开始前，脚本可以跳过尚不存在的 `desktop`，但必须打印明确的 `SKIPPED`。

---

## 17. Agent 开发规则

每个 Agent 开始任务前必须：

1. 阅读本文。
2. 确认任务包的前置条件已经通过。
3. 检查现有文件，避免覆盖其他任务的修改。
4. 只修改任务包允许的主要目录；确需越界时在任务结果中说明原因。

每个 Agent 完成任务时必须：

1. 运行任务包要求的测试。
2. 运行所有已经存在且受影响的测试。
3. 不留下空实现、伪实现、跳过测试或只返回固定值的接口。
4. 在结果中列出修改文件、验证命令和未解决风险。
5. 若外部契约与本文不一致，停止相关实现，提交证据和规格修改，不得私自猜字段。

跨任务的硬性规则：

- 不得把真实钥匙写入测试、日志、配置或错误消息。
- 不得为了测试通过而弱化协议断言。
- 不得在数据面中加入桌面依赖。
- 不得在第一阶段顺手实现非目标。
- 不得并行开发有直接依赖的切片。

---

## 18. 实施任务包

任务必须按依赖顺序推进。只有当前任务包的完成标准全部满足，才能开始依赖它的任务。

### A. 仓库引导与无头骨架

前置条件：无。

主要修改范围：

```text
go.mod
go.sum
cmd/gateway/
internal/app/
internal/config/
internal/process/
internal/server/
internal/version/
scripts/verify.ps1
LICENSE
README.md
```

必须交付：

- 初始化 module `ai-gateway`。
- 锁定 Go 工具链。
- 创建 CLI 命令框架。
- 默认配置生成、读取、校验和原子写。
- 单实例锁。
- `serve`、`stop`、`status`、`version`。
- `GET /healthz`、`GET /readyz`、`GET /api/v1/status`、`POST /api/v1/shutdown`。
- 只监听 `127.0.0.1`。
- 端口冲突明确失败。
- 优雅关闭。

必须测试：

- 配置不存在时生成。
- 非法 YAML 不覆盖。
- 非法端口拒绝。
- 两个实例不能同时运行。
- healthz 成功。
- shutdown 后端口释放。

完成命令：

```powershell
go test ./...
go vet ./...
go run ./cmd/gateway version
go run ./cmd/gateway serve
```

人工验收：

```powershell
Invoke-RestMethod http://127.0.0.1:12600/healthz
```

期望：

```json
{"status":"ok"}
```

### B. 系统钥匙存储

前置条件：A。

主要修改范围：

```text
internal/secret/
internal/config/
internal/server/
```

必须交付：

- `secret.Store` 接口。
- Windows DPAPI 实现。
- macOS Keychain 和 Linux Secret Service 的平台实现或明确的构建期实现文件。
- provider CRUD 的 secret 写入事务。
- `readyz` 和 `doctor` 的 secret 检查。

必须测试：

- Put/Get/Delete。
- 不存在 secret。
- 配置中不出现明文 key。
- provider 更新失败时恢复旧 secret。

Windows 人工验收：

1. 添加含钥匙 provider。
2. 搜索 `~/.ai-gateway`，确认没有明文钥匙。
3. 重启网关。
4. 仍能读取钥匙完成 probe。

### C. 路由与 OpenAI Chat 同协议

前置条件：A、B。

主要修改范围：

```text
internal/route/
internal/inbound/chat/
internal/outbound/openaichat/
internal/server/
testdata/protocols/chat/
```

必须交付：

- 客户端前缀路由。
- `gateway-default`。
- provider 前缀覆盖。
- Chat Completions 非流式和流式同协议转发。
- 入站占位认证丢弃，上游认证注入。
- 上游错误转发。
- fake upstream 集成测试。

必须测试：

- 四个客户端路由互不影响。
- `gateway-default` 使用当前路由模型。
- `openrouter/anthropic/claude...` 正确拆分。
- `anthropic/claude...` 在 `anthropic` 不是 provider id 时完整转发。
- SSE 实时 flush。
- 客户端取消会取消上游。

完成标准：

一个 OpenAI 兼容客户端可以经网关调用 Ollama 或 OpenRouter，并完成流式文本响应。

### D. IR 与三协议转换

前置条件：C。

主要修改范围：

```text
internal/ir/
internal/inbound/
internal/outbound/
internal/server/
testdata/protocols/
```

必须交付：

- 请求 IR 和响应事件 IR。
- Responses 入站和出站。
- Messages 入站和出站。
- 三种协议的非流式和流式处理。
- 工具定义、工具调用和工具结果。
- 所有六个跨协议方向的集成测试。

必须测试：

- 文本事件顺序。
- 工具参数增量拼接。
- 工具调用 id 保持。
- 多工具调用。
- 非流式聚合。
- 上游错误映射。
- 流中断。

禁止：

- 为快速通过而把不支持的跨协议请求伪装成成功文本。
- 在 adapter 之间互相调用。

### E. 图片、reasoning 与能力降级

前置条件：D。

主要修改范围：

```text
internal/ir/
internal/inbound/
internal/outbound/
internal/server/
internal/logstore/
testdata/protocols/
```

必须交付：

- 图片 URL 和 base64 输入。
- provider 图片能力检查。
- reasoning/thinking 直通或显式降级。
- 422 能力错误。
- 降级 warning 日志事件。

必须测试：

- 支持图片时保留图片。
- 不支持图片时不访问上游且返回 422。
- reasoning 可表达时保留。
- reasoning 不可表达时记录警告。

### F. 正文日志与用量

前置条件：C；完整协议日志依赖 D、E。

主要修改范围：

```text
internal/logstore/
internal/server/
internal/config/
```

必须交付：

- 每请求 JSONL。
- 日志开关。
- 请求摘要和详情 API。
- usage 汇总 API。
- 日志目录体积和中断文件 doctor。
- stderr 风险说明。

必须测试：

- 开启时写入完整事件。
- 关闭后新请求不创建文件。
- 日志不包含 Authorization 或真实 secret。
- 并发请求各自写入独立文件。
- 没有 usage 时不估算。

### G. 完整管理 API

前置条件：A 至 F。

主要修改范围：

```text
internal/server/
internal/config/
internal/route/
internal/secret/
internal/logstore/
docs/
```

必须交付：

- 第 11 节全部管理端点。
- 统一管理错误模型。
- 分页和筛选。
- provider probe 和模型列表。
- OpenAPI 文件可以作为附加交付，但不能替代实现测试。

完成标准：

不打开桌面，仅用 PowerShell 或 curl 可以：

1. 新增 provider 和 secret。
2. probe provider。
3. 设置四条 route。
4. 开关日志。
5. 查询日志和用量。

### H. 三客户端 point / restore

前置条件：A、G。

主要修改范围：

```text
internal/point/
internal/server/
testdata/point/
docs/install.md
```

必须交付：

- 通用 backup manifest 和事务。
- Codex adapter。
- Claude Code adapter。
- Grok Build adapter。
- point 状态检查和 drift。
- 管理 API。
- `doctor` 检查。

必须测试：

- 文件不存在时 point 和 restore。
- 文件已有无关配置时保留。
- 重复 point 幂等。
- 人工修改后显示 drift。
- 中途失败回滚。
- 环境变量原值恢复。
- 备份内容 SHA-256 正确。

完成标准：

在安装了对应客户端的 Windows 测试机上，三个客户端可以 point、发请求、restore，并恢复原上游使用方式。

### I. Wails 桌面主流程

前置条件：G、H。

主要修改范围：

```text
cmd/desktop/
desktop/
```

必须交付：

- 锁定 Wails v3 精确版本。
- React、TypeScript、Vite 和 npm lockfile。
- 首次风险说明。
- 总览、Providers、Routes、Clients、Logs、Usage、Settings。
- 中英文。
- 明暗主题。
- 网关未运行时启动网关。
- 所有状态和操作只走管理 API。

必须测试：

- API 客户端单元测试。
- 表单校验。
- route 切换。
- point/restore 确认流程。
- 日志关闭状态。
- 键盘操作主流程。
- 1440×900 和 390×844 视口。
- 明暗主题。

完成标准：

不打开终端即可完成安装当天闭环：启动网关、添加 provider、设置 route、point 客户端、查看日志、restore。

### J. 托盘、登录启动和发布

前置条件：I。

主要修改范围：

```text
internal/autostart/
cmd/desktop/
desktop/
scripts/
docs/
```

必须交付：

- 托盘菜单。
- 三个客户端独立切 route。
- 当前用户登录启动。
- Windows 构建和安装说明。
- macOS、Linux 尽力构建。
- 版本信息注入。
- 根 README 和安装文档。

必须测试：

- 窗口关闭后托盘可用。
- 退出托盘不停止网关。
- 登录任务开启、读取、关闭。
- 路径含空格。
- Windows 注销再登录后网关启动并能读取 secret。

---

## 19. Windows 最终验收

测试前记录：

- Windows 版本。
- ai-gateway 版本。
- Codex 版本。
- Claude Code 版本。
- Grok Build 版本。
- 使用的 provider、adapter 和脱敏模型名。

按顺序执行：

1. 不打开桌面，只运行 `ai-gateway serve`。
2. `healthz` 和 `readyz` 均成功。
3. 添加至少一个云 provider；本机有 Ollama 时再添加 Ollama。
4. 分别 point Codex、Claude Code、Grok Build，确认各自有备份。
5. 三个客户端分别完成一次流式工具调用，工具实际修改临时文件或执行安全命令。
6. 确认三个请求分别走对应客户端路由。
7. 托盘把 Codex 当前 route 切到另一个 provider。
8. 再发 Codex 请求，确认上游已改变，Codex 配置文件哈希没有改变。
9. 使用 `<existing-provider-id>/<model>` 发请求，确认仅该请求覆盖 provider。
10. 使用包含 `/` 但前缀不是 provider id 的模型名，确认完整转发到当前 provider。
11. 发送图片到支持图片的 provider，确认透传。
12. 发送图片到不支持图片的 provider，确认 422 且上游没有收到请求。
13. 检查日志默认包含正文，但不包含真实上游认证头。
14. 关闭日志，确认新请求不创建日志文件。
15. 查看 usage，确认只汇总上游实际返回的 token。
16. restore 三个客户端，确认原文件字节和环境变量状态恢复。
17. 打开登录启动，注销并重新登录。
18. 确认网关在当前用户会话启动并可解开 secret。
19. 占用 12600 端口后启动网关，确认明确失败且没有改端口。
20. 运行 `ai-gateway doctor`，确认没有阻断项。

任一项失败，第一阶段不得标记完成。

---

## 20. 外部契约核验记录

以下链接是 2026-08-14 的实现基线。开发对应 point adapter 前必须重新打开官方页面核对。

### Codex

- [Codex Advanced Configuration](https://developers.openai.com/codex/config-advanced)
- [Codex Configuration Reference](https://developers.openai.com/codex/config-reference)

已核验：

- 用户级配置位于 `~/.codex/config.toml`。
- 自定义 provider 通过 `model_providers.<id>` 配置。
- `openai`、`ollama`、`lmstudio` 是保留 id。
- 自定义 provider 的 `wire_api` 只有 `responses`。
- `env_key` 是提供 API key 的环境变量名。

### Claude Code

- [Claude Code Settings](https://code.claude.com/docs/en/settings)
- [Connect Claude Code to an LLM gateway](https://code.claude.com/docs/en/llm-gateway-connect)
- [Claude Code Feature Availability](https://code.claude.com/docs/en/feature-availability)
- [Claude Code Remote Control](https://code.claude.com/docs/en/remote-control)

已核验：

- 用户级设置是 `~/.claude/settings.json`。
- 环境变量可以写入 `settings.json` 的 `env`。
- `ANTHROPIC_BASE_URL` 用于自定义网关。
- 非 Anthropic host 会禁用 Remote Control。
- 非第一方 host 下 MCP tool search 默认关闭或降级，具体能力受版本和管理设置影响。

### Grok Build

- [Grok Build Settings](https://docs.x.ai/build/settings)
- [Grok Build Settings Reference](https://docs.x.ai/build/settings/reference)

已核验：

- 用户级配置是 `~/.grok/config.toml`，可由 `GROK_HOME` 覆盖。
- 自定义模型位于 `[model."<id>"]`。
- `api_backend` 支持 `chat_completions`、`responses`、`messages`。
- 配置支持 `base_url`、`model`、`env_key`、`api_key` 和 `extra_headers`。
- `grok inspect` 可检查实际配置来源。

### 工具链

- [Go Release History](https://go.dev/doc/devel/release)
- [Wails v3](https://v3.wails.io/)
- [Wails Releases](https://github.com/wailsapp/wails/releases)
- [Node.js Releases](https://nodejs.org/en/about/previous-releases)

已核验：

- Go `1.26.6` 于 2026-08-13 发布。
- Wails v3 在 2026-08-14 仍为 beta。
- Wails v3 当前采用 Go 安装 CLI，并发布 beta 标签。
- Node.js `24.19.0` 是 LTS 基线。

### 2026-08-15 复核：客户端可选模型目录

本次复核针对「把全部已启用 `<provider-id>/<model-id>` 交给客户端选择」这一需求，
在本机安装的真实客户端上做了可复现实验，结论直接约束第 7.3、7.5 和 12.3 至 12.5 节。

实验对象：Codex `0.147.0`、Claude Code `2.1.228`、Grok Build `1.0.4`。
方法：为每个客户端设置独立配置目录（`CODEX_HOME`、`CLAUDE_CONFIG_DIR`、`GROK_HOME`），
指向本地假上游，观察客户端实际发出的 HTTP 请求正文。

#### Codex：`model_catalog_json` 可用，但第一阶段不采用

- `model_catalog_json` 确实是 Codex `0.147.0` 的根级配置键，值为一个 JSON 文件路径。
  写在 `[model_providers.<id>]` 表内不生效。
- 文件结构为 `{"models":[...]}`。条目必填字段为 `slug`、`display_name`、
  `supported_reasoning_levels`、`shell_type`、`visibility`、`supported_in_api`、
  `priority`、`support_verbosity`、`truncation_policy`、`supports_parallel_tool_calls`、
  `experimental_supported_tools`，并且必须提供 `base_instructions` 或 `model_messages`
  之一，否则 Codex 拒绝启动。
- `visibility` 取值为 `list` 或 `hide`。`slug` 允许包含 `/`：`codex debug models`
  能正确列出 `openrouter/anthropic/claude-sonnet-4` 这类 slug。
- 自定义目录**整体替换**内置目录，不是追加。
- 决定性证据：条目中的 `base_instructions` 会替换 Codex 真正的 agent 系统提示词。
  用哨兵字符串做对照实验，上游收到的 `instructions` 从 21178 字符降到 32 字符；
  改为克隆内置条目的 `model_messages` 后，上游收到的 `instructions` 长度为 0。
- 同时验证了目录并非必需：只把根级 `model` 写成
  `openrouter/anthropic/claude-sonnet-4`、完全不配置目录，Codex 仍能正常完成回合，
  上游收到的模型名逐字一致，系统提示词保持 21178 字符，仅多打印一行元数据缺失警告。

结论：第一阶段不写 `model_catalog_json`。为了让选择器多出几行，代价是替换掉客户端
系统提示词、并把上游厂商的提示词文本复制进本仓库并随其版本腐坏。§1.3
「不修改 Codex 内置模型目录」继续有效。Codex 侧改为写入首选模型，
完整目录通过 `/c/codex/v1/models` 与 `codex -m <provider-id>/<model-id>` 使用。

#### Claude Code：网关模型发现可用，但只保留含 claude/anthropic 的 id

- `settings.json` 无法承载模型列表。可用机制有两个：
  `ANTHROPIC_CUSTOM_MODEL_OPTION`（外加 `_NAME`、`_DESCRIPTION`）只能新增**一个**
  选择器条目；`CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1` 可以发现多个。
- 发现流程已在真实二进制上跑通：Claude Code 启动时以
  `User-Agent: claude-code/2.1.228` 请求 `GET <ANTHROPIC_BASE_URL>/v1/models?limit=1000`，
  带 `anthropic-version: 2023-06-01`，超时 3 秒，`redirect: "error"`（重定向即失败），
  只读取 `data[].id` 和可选 `data[].display_name`，结果缓存到
  `<CLAUDE_CONFIG_DIR>/cache/gateway-models.json`，选择器中标注 `From gateway`。
- 生效前置条件：`ANTHROPIC_BASE_URL` 已设置且 host 不是 `api.anthropic.com`，
  并且存在 `ANTHROPIC_AUTH_TOKEN` 或 `ANTHROPIC_API_KEY`。缺少 key 时静默跳过发现。
- 决定性约束：Claude Code 只保留 id 匹配 `/(claude|anthropic)/i` 的条目。实验中网关
  返回 4 个模型，缓存文件里只剩 `openrouter/anthropic/claude-sonnet-4` 一个，
  `gateway-default`、`deepseek/deepseek-chat`、`zhipu/glm-5` 全部被丢弃。
  因此 Claude Code 的选择器天然无法展示全部已启用模型，这是客户端行为，不是网关缺陷。
- 另一项验证：`--model <provider-id>/<model-id>` 可以直接使用任意模型名，
  上游收到的 `model` 逐字一致，不做改写；模型名不在其内置表中时只降级
  auto-compact 的上下文窗口估算，并按子串匹配打印退役提示（例如 id 中包含
  `claude-sonnet-4` 会触发退役警告），不影响请求成功。

#### Grok Build：原生支持完整目录

- `[model."<id>"]` 可以声明任意多个自定义模型，与内置模型**并存**而非替换。
- 第一次实测（id 直接用模型名）：内置 `grok-4.6`、`grok-4.5` 与自定义
  `gateway-default`、`openrouter/anthropic/claude-sonnet-4`、`deepseek/deepseek-chat`
  同时列出，`[models] default` 指定的条目被标记为默认。
- 第二次实测（id 加 `ai-gateway:` 前缀，并预置一个用户自有模型 `my-own-model`）：
  `grok models` 输出 `grok-4.6`、`grok-4.5`、`ai-gateway`（默认）、
  `ai-gateway:openrouter/anthropic/claude-sonnet-4`、`ai-gateway:deepseek/deepseek-chat`、
  `my-own-model`。即 id 含 `/` 和 `:` 都不影响解析，且用户自有条目不受影响。
- `grok models` 列出的是配置中的表 id，不是 `name`。`name` 按官方 settings reference
  是选择器显示名；第一阶段把该字段写成与 `model` 相同的
  `<provider-id>/<model-id>`（或 `gateway-default`），不使用目录中的友好名称。

#### 由此确定的第一阶段做法

- 客户端路由从「客户端唯一可用模型」改为「客户端启动首选模型」，语义见 §7.3。
- 完整的已启用 `<provider-id>/<model-id>` 目录一律由 `/c/{client}/v1/models` 提供，
  这是三个客户端唯一共同可用的入口。
- 只有 Grok Build 在配置文件里落地完整目录；Codex 与 Claude Code 只写首选模型。
- `/v1/models` 每项增加 `display_name`，取值等于该项 `id`，供 Claude Code
  发现与 Grok 选择器按 `供应商/模型 ID` 显示。

---

## 21. 第一个 Agent 的开工指令

第一个 Agent 只执行任务包 A，不提前实现 provider、协议转换、桌面或 point。

可直接使用以下任务描述：

> 阅读 `docs/v1-scheme.md`，只实施“任务包 A：仓库引导与无头骨架”。从当前空实现仓库创建 Go module、目录骨架、配置模型、原子写、单实例锁、CLI 命令、健康检查、状态管理 API 和优雅关闭。严格监听 `127.0.0.1`，端口冲突必须失败。补齐任务包 A 的单元测试和集成测试，运行 `go test ./...` 与 `go vet ./...`。不要实现 provider、secret、协议 adapter、日志正文、point、Wails 桌面或登录启动。完成后报告修改文件、验证命令、测试结果和仍存在的风险。

---

## 22. 第一期完成定义

陌生用户在 Windows 11 上安装无头网关和桌面后，可以：

1. 安全保存自己的上游 provider 和钥匙。
2. 一键把已经使用的 Codex、Claude Code、Grok Build 指向本机网关。
3. 让三个客户端继续执行流式工具调用。
4. 在不再次修改客户端配置的情况下切换各自上游。
5. 查看正文日志和真实上游返回的粗用量。
6. 随时关闭日志。
7. 精确还原指向前的客户端配置。
8. 关闭桌面后继续使用网关。
9. 在登录后由当前用户会话自动启动网关并读取钥匙。

全部成立并通过第 19 节验收后，第一阶段完成。此时停止扩展范围，不顺手进入第二阶段。
