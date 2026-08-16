# ai-gateway 第一期进度与接续

> 状态：发布候选，第一期尚未验收完成  
> 文档日期：2026-08-16  
> 当前提交：`3d029b31e88b66f0717fdad6bc2d606ecc98fbf8`（发布包内二进制提交）  
> 当前发布：`0.1.0-rc1`（`dist/ai-gateway-0.1.0-rc1-windows-amd64.zip`，SHA-256 `73751348263C33939AE27D7F518BB2EC3F27802866ADC2564318633D5064B004`）  
> 第一验收平台：Windows 11

本文给后续 Agent（含 Grok）接续工作。本文记录进度、权威边界、已落地增量和下一步，**不是新的工程合同**。

开始任何实现或改协议之前，必须先读 [v1-scheme.md](v1-scheme.md)。本文与规格冲突时，以规格为准。外部契约变化时，按规格 §17 / §20 先记证据再改规格，禁止凭记忆兼容。

---

## 1. 现在处于哪一步

任务包 A 到 J 已经全部实现。仓库是**发布候选**，不是第一期完成。

第一期完成定义在规格 §22：陌生用户在 Windows 11 上装好无头网关和桌面后，能保存钥匙、指向三个客户端、完成流式工具调用、不改客户端文件就切换上游、看正文日志和真实用量、随时关日志、精确还原、关桌面后网关仍运行、登录后自动启动并解开钥匙。这些必须全部成立，并且通过规格 §19 的二十项真实验收。

§19 仍未执行。在它通过之前，不得把第一期标成完成，也不得顺手进入第二期。

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

规格示例里的 `listen` 仍只写了端口，§5.2 仍写“hostname 不进入配置”。那是旧句子。当前实现允许显式配置 `0.0.0.0` 给局域网客户端，默认继续 `127.0.0.1`。

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
| F | 正文日志与用量 | 已完成 | 每请求 JSONL；用量只信上游，缺失标不完整，禁止估算 |
| G | 完整管理 API | 已完成 | 无头即可完成供应商、路由、日志、用量 |
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
4. **桌面「客户端路由」是单一默认模型选择器**，选项为全部已启用的 `供应商/模型ID`。托盘菜单同一套目录。
5. **Claude Code `messages` 数组里的 `role: system` 已兼容**，与顶层 `system` 合并为内部系统提示。
6. **探测响应**可在桌面以格式化 JSON 查看；失败正文不回传，避免错误响应泄露钥匙。
7. **桌面操作反馈**是右上角可关闭气泡，不是整页横幅。
8. **日志详情与行内复制**输出完整 JSON；敏感头只记省略计数。
9. **Codex 远程压缩开关。** 桌面「客户端」页的 Codex 卡片可开关
   `clients.codex.remote_compaction`。开启后指向/同步把
   `[model_providers.ai-gateway].name` 写成 `OpenAI`，并转发
   `POST /v1/responses/compact`。关闭则改回 `ai-gateway`。不得因此新建还原点。
   只有 `openai-responses` 上游可以转发 compact；其它适配器 422。
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

相关证据在规格 §20「2026-08-15 复核：客户端可选模型目录」、
「2026-08-16 复核：Codex 远程压缩触发条件」、
「2026-08-16 复核：Responses 历史回放使用 output_text」、
「2026-08-16 复核：Chat 出站工具参数必须是 JSON 字符串」和
「2026-08-16 复核：Chat 出站工具结果必须紧跟 tool_calls」和
「2026-08-16 复核：Claude Code `/model` 使用可逆选择器别名」和
「2026-08-16 复核：Claude Code `/model` 必须预写 gateway-models.json」和
「2026-08-16 复核：Responses 出站助手历史必须用 output_text」和
「2026-08-16 复核：Claude Code 用户消息里的 tool_result 必须转成 function_call_output」。不要重新发明 Codex 目录方案，也不要只靠 Claude 启动发现而不写缓存。

---

## 5. 三个客户端的目录契约（不要改回去）

| 客户端 | 配置里写什么 | 用户怎么选其它模型 | 禁止事项 |
|---|---|---|---|
| Codex | 首选 `model = "gateway-default"`；根键 `model_catalog_json` 指向同目录 `ai-gateway-catalog.json` | `/model`、`codex debug models`、`codex -m <provider-id>/<model-id>`，或 `/c/codex/v1/models` | 目录条目必须从本机 `codex debug models --bundled` 克隆，保留 `base_instructions`，删除 `model_messages`。禁止手写短提示词。找不到模板则拒绝指向。 |
| Claude Code | 四个模型环境变量都是 `gateway-default`；`CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`；`<CLAUDE_CONFIG_DIR>/cache/gateway-models.json` 写入全部已启用模型 | `/model` 读这份缓存（`display_name` 为 `供应商/模型 ID`）；启动时若发现未被关掉也会刷新同一文件；或 `claude --model <provider-id>/<model-id>` | `/c/claude/v1/models` 与缓存里的 `id` 必须是可逆 `claude-gw*` 别名。禁止把别名写进启动环境变量、Codex/Grok 目录或发给上游。不得覆盖用户已有的无关 `env`（包括 `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`）。 |
| Grok Build | `[models] default = "ai-gateway"`；每个已启用模型一条 `ai-gateway:` 前缀表 | 配置目录与内置模型并存 | restore 只删网关写过的条目，必须保留用户自己的模型。 |

切换路由只改网关配置。客户端文件里只要还是 `gateway-default`，就不要去改它。

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

### 7.1 主线：规格 §19 Windows 最终验收

这是第一期唯一还没做完的验收门。mocks 和 fake upstream **不能**代替这一节。

开始前记录：

- Windows 版本
- `ai-gateway.exe version` 的版本、提交、Go、平台
- Codex、Claude Code、Grok Build 的版本
- 使用的 provider、adapter、脱敏后的模型名

然后按规格 §19 的 1 到 20 项顺序做，不要跳项、不要改顺序。任一项失败，第一期不得标完成。

失败时：

1. 留下可复现证据（命令、状态码、脱敏日志、客户端版本）。
2. 若是外部契约变了，先改规格 §20 和测试，再改代码。
3. 若是实现缺陷，修代码并补不会过期的测试。
4. 不要为了过验收而放宽断言。

本机已知障碍：

- 当前用户创建计划任务曾返回 `0x80070005`。登录启动的代码和 XML 回读校验已在，缺的是有权限的 Windows 11 会话。不要在本机留下探测任务。
- `go test -race` 需要 cgo 和可执行 C 编译器（Windows 上是 mingw-w64 gcc）。没有编译器时，`scripts/verify.ps1` 必须打印 `SKIPPED`，不得报成通过。
- 真实云 provider 需要用户自己的钥匙。测试和日记里禁止出现真实钥匙。

### 7.2 验收里顺手可做、但不是新功能

- 在有权限的机器上走通：打开登录启动 → 注销再登录 → 网关起来 → DPAPI 能解开钥匙 → 再关闭登录启动。
- 装好 C 编译器后跑 `go test -race ./...`。
- 三个客户端各做一次真实流式工具调用（工具必须真的改临时文件或执行安全命令）。
- 托盘把 Codex 路由切到另一个已启用 `供应商/模型ID`，确认上游变了，且 Codex 配置文件哈希不变。

### 7.3 明确不要做

- 不要开始第二期。
- 不要升级 Wails。
- 不要手写短 `base_instructions` 或把官方提示词检入仓库。Codex 目录必须从本机 bundled 模板克隆。
- 不要把选择器别名发给上游，也不要把别名写进 Codex / Grok 目录或四个 Claude 启动环境变量。`/c/claude/v1/models` 的可逆 `claude-gw*` 别名是已冻结的例外。
- 不要做日志轮转、脱敏、大小上限、多租户、远程鉴权、自动更新。
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
internal/point       指向事务；clientcatalog 是叶子包
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
4. `route.Resolve`：先解码 Claude 选择器别名 → 路由默认 → 空或 `gateway-default` → 命中已配置 provider 前缀则覆盖 → 否则整段模型名交给当前路由的 provider。含 `/` 的模型名不得报“未知供应商”。
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
提交：`3d029b31e88b66f0717fdad6bc2d606ecc98fbf8`  
SHA-256：`73751348263C33939AE27D7F518BB2EC3F27802866ADC2564318633D5064B004`

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

> 阅读 `docs/progress.md` 和 `docs/v1-scheme.md` §19、§22。不要实现新功能。在已安装 Codex、Claude Code、Grok Build，并且有至少一个云 provider 钥匙的 Windows 11 上，按 §19 第 1 到 20 项做真实验收。每一项记下通过或失败、命令、脱敏证据。失败则按 §17 停下来：外部契约变化先更新 §20 和测试，实现缺陷再改代码。不要升级 Wails，不要改模型 id 去迎合 Claude Code。做完后更新本文第 12 节的验收记录，并说明第一期是否可以标完成。

若用户指定了功能：先核对本文件第 7.3 节和第 6 节，确认那不是禁区；再读规格对应小节；只改需要改的目录。

改完后用受影响的测试和 `.\scripts\verify.ps1` 验证。不能跑的步骤必须 `SKIPPED`。

---

## 12. §19 验收记录

尚未开始。下一次执行验收的 Agent 按项填写，不要删掉未做的行。

| 项 | 内容 | 结果 | 证据 |
|---|---|---|---|
| 环境 | Windows / 网关 / 三客户端 / provider 版本 | 未记 | |
| 1 | 只跑 `ai-gateway serve`，不打开桌面 | 未做 | |
| 2 | `healthz` 与 `readyz` 成功 | 未做 | |
| 3 | 添加云 provider；有 Ollama 再加 Ollama | 未做 | |
| 4 | 分别 point 三个客户端，确认备份 | 未做 | |
| 5 | 三个客户端各完成一次流式工具调用 | 未做 | |
| 6 | 三个请求分别走对应客户端路由 | 未做 | |
| 7 | 托盘把 Codex 路由切到另一模型 | 未做 | |
| 8 | 再发 Codex 请求，上游已变，配置哈希不变 | 未做 | |
| 9 | `<已有-provider-id>/<model>` 只覆盖该请求 | 未做 | |
| 10 | 含 `/` 但前缀不是 provider id 的模型名完整转发 | 未做 | |
| 11 | 图片发到支持图片的 provider，确认透传 | 未做 | |
| 12 | 图片发到不支持的 provider，422 且上游零请求 | 未做 | |
| 13 | 默认日志有正文，无真实上游认证头 | 未做 | |
| 14 | 关日志后新请求不建文件 | 未做 | |
| 15 | usage 只汇总上游真实 token | 未做 | |
| 16 | restore 三个客户端，字节和环境变量恢复 | 未做 | |
| 17 | 打开登录启动，注销再登录 | 未做 | |
| 18 | 登录后网关启动且能解开 secret | 未做 | |
| 19 | 占用 12600 后再启动，明确失败且不改端口 | 未做 | |
| 20 | `ai-gateway doctor` 无阻断项 | 未做 | |

二十项全部通过之前，第一期保持「发布候选」。
