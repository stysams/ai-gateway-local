# ai-gateway 代码地图

> 给后续 Grok / Agent 按任务找文件用。  
> 进度和下一步见 [progress.md](progress.md)。  
> 行为合同见 [v1-scheme.md](v1-scheme.md)。  
> 改代码前先读源码注释里引用的规格小节。

本文只描述**现在仓库里实际有什么、该去哪改**。不要把本文当成新规格。

---

## 1. 先看这一张表

| 你要改的事 | 先打开 | 再打开 |
|---|---|---|
| CLI 命令、退出码、启动/停止 | `cmd/gateway/main.go` → `internal/app/app.go` | `internal/process/` |
| 监听地址、端口、数据根 | `internal/config/config.go` `Listen` | `internal/app/datadir.go`、`internal/server/server.go` |
| `config.yaml` 字段、校验、原子写 | `internal/config/config.go`、`validate.go` | `internal/config/manager.go` |
| 管理 API 路由表 | `internal/server/handlers.go` `routes()` | 对应 `internal/server/*.go` |
| 数据面整条管道 | `internal/server/dataplane.go` `serveDataPlane` | `handlers.go` 里的 `/v1/*` 注册 |
| §7.4 模型解析 | `internal/route/route.go` `Resolve` | `internal/route/availability_test.go` |
| `/v1/models` 与客户端目录 | `dataplane.go` `modelCatalog` | `internal/server/clients.go` `clientSettings` |
| 同协议转发 | `dataplane.go` `serveSameProtocol` | `inbound/*/Parse` + `Rewrite` |
| 跨协议转换 | `dataplane.go` `serveCrossProtocol` | `internal/ir/ir.go`，再进 inbound / outbound |
| 入站协议外形 | `internal/inbound/{chat,responses,messages}` | `Parse` / `ParseRequest` / `Encode*` / `WriteError` |
| 出站协议外形与上游 HTTP | `internal/outbound/{openaichat,openairesponses,anthropic}` | `outbound/internal/upstream` |
| 图片 / reasoning / context_management 门 | `dataplane.go` `inspectRequestFeatures`、`normalizeReasoning`、`Drop*` | 各 inbound 的 `InspectFeatures` / `DropReasoning` |
| 钥匙读写与事务 | `internal/secret/` | `internal/server/providers.go`（§6.3） |
| 指向 / 还原 / 漂移 | `internal/point/point.go` | `point/{codex,claude,grok}`、`point/clientcatalog` |
| Codex `/model` 目录 sidecar | `internal/point/codex/catalog.go` `BuildCatalog` | `codex/template.go`、`point.go` 多文件备份 |
| 路由变更同步客户端目录 | `internal/server/clients.go` `applyClientSettingsChanges` | `point.Manager.SyncSettings` |
| 正文日志与用量 | `internal/server/trace.go` | `internal/logstore/store.go`、`server/logs.go` |
| 管理错误外形 | `internal/server/apierror.go` | 数据面错误走 inbound `WriteError` |
| 登录启动 | `internal/autostart/` | `internal/server/autostart.go`、`internal/app` 的 `autostart` 子命令 |
| 桌面窗口与七个页面 | `desktop/src/App.tsx` | `desktop/src/api.ts`、`catalog.ts`、`i18n.ts` |
| 桌面默认模型选择器 | `desktop/src/catalog.ts` | `App.tsx` `Routes` |
| 托盘切路由 | `cmd/desktop/tray.go` | `cmd/desktop/trayclient.go` |
| 桌面如何拉起网关 | `cmd/desktop/main.go` | `cmd/desktop/launcher.go` |
| 发布与校验 | `scripts/verify.ps1` | `scripts/build-release.ps1` |

---

## 2. 两个二进制，一个模块

一个 Go module：`ai-gateway`（根目录 `go.mod`）。`desktop/` 另有一份 **stub** `go.mod`，根模块的 `./...` 不会走进前端。

| 二进制 | 入口 | 做什么 |
|---|---|---|
| `ai-gateway` | `cmd/gateway/main.go` → `app.Main` | 无头网关。听端口，同时挂管理面和数据面。 |
| `ai-gateway-desktop` | `cmd/desktop/main.go` | Wails 壳。`go:embed` 前端；业务只打回环 `/api/v1`。关闭窗口只隐藏，不杀网关。 |

桌面二进制如果带参数 `serve`，会直接进入 `app.Main`，和托盘拉起的无头进程是同一条 CLI。

前端源码在 `desktop/src/`。`npm --prefix desktop run build` 写进 `cmd/desktop/assets/`，该目录已提交，由 `cmd/desktop/main.go` embed。只改 `desktop/src` 而不 build，桌面 exe 看不到新界面。

---

## 3. 依赖方向（禁止逆流）

```text
cmd/gateway, cmd/desktop
        ↓
internal/app
        ↓
config  secret  route  server  logstore  point  autostart  process  version

server → inbound, outbound, route, logstore, point, secret, config, autostart
inbound → ir
outbound → ir
outbound/{chat,responses,anthropic} → outbound/internal/upstream → secret

desktop/src  只通过 HTTP /api/v1
             不 import 任何 internal 包
             不直接改 config.yaml
```

硬规则：

- `ir` 不 import 任何具体协议包。
- `inbound` 不碰钥匙库。
- `outbound` 不决定路由。
- adapter 互不调用；跨协议只经 `ir`。
- 平台差异用构建标签文件（`*_windows.go`、`*_unix.go`、`*_darwin.go`、`*_other.go`），禁止在共享文件里写 `runtime.GOOS`。

---

## 4. 进程怎么起来

```text
ai-gateway serve
    internal/app/app.go          解析 flag，算数据根
    internal/app/datadir.go      AI_GATEWAY_DATA_DIR 或 ~/.ai-gateway
    internal/config.Manager      LoadOrCreate config.yaml
    internal/secret.New          平台钥匙库
    internal/process             单实例锁 gateway.lock + pid.json
    internal/server.Server       Listen + Serve
    信号 / 控制台关闭 / POST /api/v1/shutdown
    30s 优雅关闭
```

CLI 子命令都在 `internal/app/app.go`：`serve`、`stop`、`status`、`doctor`、`autostart`、`version`。退出码见规格 §14.1，常量在同文件。

`stop` / `status` / `doctor` 走 `internal/server/adminclient.go`，打本机管理 API，不另起一套逻辑。

---

## 5. HTTP 面：一张 mux

注册表：`internal/server/handlers.go` `routes()`。一个 `http.ServeMux` 挂三类路径。

### 5.1 健康

| 路径 | 函数 | 文件 |
|---|---|---|
| `GET /healthz` | `handleHealthz` | `handlers.go` |
| `GET /readyz` | `handleReadyz` | `handlers.go`（配置 + 必需钥匙） |

### 5.2 管理面 `/api/v1`

| 路径 | 函数 | 文件 |
|---|---|---|
| `GET /api/v1/status` | `handleStatus` | `handlers.go` |
| `POST /api/v1/shutdown` | `handleShutdown` | `handlers.go` |
| `GET /api/v1/doctor` | `handleDoctor` | `doctor.go` |
| `GET/PUT /api/v1/config` | `handleGetConfig` / `handlePutConfig` | `configapi.go` |
| `GET/POST /api/v1/providers` | `handleListProviders` / `handleCreateProvider` | `providers.go` |
| `GET/PUT/DELETE /api/v1/providers/{id}` | `handleGet/Update/DeleteProvider` | `providers.go` |
| `PUT /api/v1/providers/{id}/availability` | `handleUpdateProviderAvailability` | `providers.go` |
| `POST /api/v1/providers/{id}/probe` | `handleProbeProvider` | `providerprobe.go` |
| `GET /api/v1/providers/{id}/models` | `handleProviderModels` | `providerprobe.go` |
| `POST /api/v1/provider-models/discover` | `handleDiscoverProviderModels` | `providerprobe.go` |
| `PUT /api/v1/routes/{client}` | `handlePutRoute` | `routesapi.go` |
| `GET /api/v1/clients/{client}` | `handleGetClient` | `clients.go` |
| `POST .../point`、`.../restore` | `handlePointClient`、`handleRestoreClient` | `clients.go` |
| `PUT /api/v1/clients/codex/remote-compaction` | `handlePutClientRemoteCompaction` | `clients.go` |
| `GET /api/v1/logs`、`/logs/{id}`、`/usage` | `handleLogs` 等 | `logs.go` |
| `PUT /api/v1/logging` | `handleLogging` | `logs.go` |
| `PUT /api/v1/autostart` | `handleAutostart` | `autostart.go` |

管理错误统一走 `apierror.go`：`{"error":{code,message,details,request_id}}`。

供应商写事务（创建 / 更新 / 删除 / 可用性）必须拿 `Server.txMu`。顺序是：校验 → 写钥匙 → 原子写配置 → 失败则恢复旧钥匙。见 `providers.go`。

`handlePutRoute` 和 `handlePutConfig` 若改变了已启用模型目录，会经 `clients.go` 的 `applyClientSettingsChanges` 调用 `point.SyncSettings`，只改已经托管的客户端文件，不新建还原点。

### 5.3 数据面 `/v1` 与 `/c/{client}/v1`

无前缀的 `/v1/*` 等于 `/c/generic/v1/*`。未知 `{client}` 必须 404，不能当成 generic。

| 路径 | 入站协议 | 入口 |
|---|---|---|
| `POST /v1/chat/completions` | Chat | `handleChatCompletions` |
| `POST /v1/responses` | Responses | `handleResponses` |
| `POST /v1/responses/compact` | Responses compact | `handleResponsesCompact` |
| `POST /v1/messages` | Messages | `handleMessages` |
| `GET /v1/models` | 模型列表 | `handleModels` → `modelCatalog` |

带 `/c/{client}` 的对应函数在同文件，先 `route.ParseClientID` 再进 `serveDataPlane`。

桌面开发服务器的 CORS 在 `cors.go`，只放行本机 Vite 源。

---

## 6. 数据面管道（改协议必读）

唯一入口：`internal/server/dataplane.go` `serveDataPlane`。

```text
HTTP 请求
  Content-Type JSON？否则 415；体 > 128MiB 则 413
  parseForRouting          只取 model、stream
  startTrace               JSONL + X-Request-Id     → trace.go
  route.Resolve            客户端 + 模型 + 配置快照  → route.go
  能力门
      图片不支持 → 422，上游零请求
      context_management 未声明 → 剥离，记警告
      reasoning 不可表达 → 删除，记 reasoning_dropped
  inProto == outProto ?
      是 → serveSameProtocol
            inbound.Parse + Rewrite(model,stream)
            outbound.Pool.Do（注入认证，清零钥匙）
            白名单头 + 状态 + 字节原样转发；SSE 每块 Flush
      否 → serveCrossProtocol
            parseRequestIR → GenerateRequest → upstream
            流：streamCrossProtocol + ir.Sequencer + encodeStream
            非流：ParseResponse → Sequencer.Accumulate → encodeNonStream
```

协议分发辅助函数也在 `dataplane.go`：`parseRequestIR`、`generateRequest`、`parseResponseIR`、`newOutStreamReader`、`encodeStream`、`encodeNonStream`。不要在 handlers 里再写一套。

错误映射：

| 情况 | 状态 | 注意 |
|---|---|---|
| 钥匙库失败 | 500 | 不是 502 |
| 上游不可达 | 502 | |
| 上游响应头超时 | 504 | 只有 `ResponseHeaderTimeout`，没有总流时限 |
| 能力不够 | 422 | 上游零请求 |
| 未知客户端 / 非法路径 | 404 | |

数据面错误用**入站协议**的原生外形（`inbound/*/WriteError`）。不要套管理面信封。

重定向：`outbound/internal/upstream.NoRedirectClient` 不跟随。状态和 `Location` 原样给客户端，避免钥匙打到第二目标。

---

## 7. 协议三层

### 7.1 `internal/ir`

`ir.go`：协议无关的 `Request`、`Message`、`Block`、`Tool`、`Image`、`ReasoningConfig`、`Event`、`Response`。

`Sequencer`：统一事件流状态机。工具 id 稳定；参数 delta 按到达顺序拼接；最多一次 `response.completed`；出错后禁止再发成功事件。上游断流必须变成协议错误事件，禁止伪造完成。

### 7.2 入站 `internal/inbound`

每个协议一套。同协议转发用 `Parse` + `Rewrite`，顶层字段放在 `map[string]json.RawMessage` 里，字段值无损；键序和空白不保证。

| 协议 | 目录 | 同协议 | 转 IR | 编码回客户端 |
|---|---|---|---|---|
| Chat Completions | `inbound/chat` | `chat.go` | `parse.go` `ParseRequest` | `encode.go` |
| Responses | `inbound/responses` | `responses.go` `Parse`/`Rewrite` | `ParseRequest` | `Encode*` |
| Messages | `inbound/messages` | `messages.go` `Parse`/`Rewrite` | `ParseRequest` | `Encode*` |

Claude Code 会在 `messages` 数组里发 `role: system`。合并逻辑在 `inbound/messages` 的 `parseMessages`，测试是 `TestParseRequestMergesClaudeCodeSystemRoleMessages`。不要再拒这种请求。

`DropContextManagement` 只在 Messages 入站。`InspectFeatures` / `DropReasoning` 三个协议都有。

### 7.3 出站 `internal/outbound`

| 适配器配置值 | 包 | 上游 URL |
|---|---|---|
| `openai-chat` | `outbound/openaichat` | `<base>/chat/completions` |
| `openai-responses` | `outbound/openairesponses` | `<base>/responses` |
| `anthropic` | `outbound/anthropic` | `<base>/v1/messages` |

每个包都有：`Pool`、`Client.Do`、`CompletionURL`、`GenerateRequest`、`ParseResponse`、`NewStreamReader`。

认证和传输在 `outbound/internal/upstream`：

- `NewTransport`：连接池 + `ResponseHeaderTimeout` 60s，无总 deadline
- `NoRedirectClient`
- `Bearer` / `XAPIKey`：读钥匙、用完 `Zero`
- `ErrSecretMissing`、`ErrSecretStore` → 数据面 500

OpenAI 系用 Bearer。Anthropic 用 `x-api-key` + 固定 `anthropic-version: 2023-06-01`。

---

## 8. 配置、钥匙、路由

### 8.1 `internal/config`

| 文件 | 职责 |
|---|---|
| `config.go` | 结构体、`Defaults`、`EnabledValue` / `HostValue` / `PortValue`、指针可选字段、`Extra` 保未知顶层键 |
| `validate.go` | 完整校验；provider id、adapter、URL、路由引用、模型目录与默认模型一致 |
| `manager.go` | `Load` / `LoadOrCreate` / `Snapshot` / `Write`；同目录临时文件原子替换；写之前完整校验 |

数据面永远读 `Manager.Snapshot()`，不要自己读磁盘。

`listen.host` 现在可以是 `127.0.0.1` 或 `0.0.0.0`。`ClientBaseURL`（`handlers.go`）写进客户端配置时永远是 `http://127.0.0.1:<port>`，即使监听 `0.0.0.0`。

### 8.2 `internal/secret`

接口在 `store.go`：`Put` / `Get` / `Delete` / `Available`，错误是 `ErrNotFound` 与 `ErrUnavailable`。

| 文件 | 平台 |
|---|---|
| `store_windows.go` | 当前用户 DPAPI，密文在 `<data root>/secrets/<ref>.bin` |
| `store_darwin.go`、`store_linux.go`、`store_other.go` | 明确失败，无明文回退 |
| `unavailable.go` | 不可用实现 |
| `NewMemStore` | 测试用内存库 |

`Get` 返回的字节用完必须 `secret.Zero`。真实钥匙只经供应商写路径进入。

### 8.3 `internal/route`

| 符号 | 含义 |
|---|---|
| `ClientID` | `codex` / `claude` / `grok` / `generic` |
| `ReservedModel` | `"gateway-default"`，必须与 `clientcatalog.ReservedModel` 相等（`clients_test.go` 有守卫） |
| `ParseClientID` | 非法客户端 → 404 |
| `RouteFor` | 读该客户端当前路由 |
| `Resolve` | §7.4 四步，并检查 provider / 模型 `enabled` |

`enabled == false` 的供应商或模型不能被解析到。前缀覆盖只在 `<prefix>` **正好是**已配置 provider id 时生效。`anthropic/claude-...` 在没有名为 `anthropic` 的 provider 时，整段交给当前路由的供应商。

---

## 9. 指向与客户端目录

`internal/point/point.go` 是事务外壳：

1. 定位用户级配置文件（可被环境变量改路径）。
2. 读原始字节。
3. 写 `backups/<client>/<utc>/` 和带 SHA-256 的 `manifest.json`。
4. 调适配器 `Transform`。
5. 原子写回；失败则文件和环境变量一起回滚。

| 客户端 | 适配器 | 配置 | 适配器写什么 |
|---|---|---|---|
| Codex | `point/codex` | `~/.codex/config.toml` 与 `ai-gateway-catalog.json` | `model_provider = ai-gateway`，`model = gateway-default`，根键 `model_catalog_json` |
| Claude Code | `point/claude` | `~/.claude/settings.json` | `ANTHROPIC_*` 四个模型环境变量 = `gateway-default`，打开 `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` |
| Grok Build | `point/grok` | `~/.grok/config.toml` | `[model."ai-gateway"]` 首选；其余 `[model."ai-gateway:<id>"]`；`name` = 模型 id |

`point/clientcatalog` 是叶子包：`ReservedModel`、`Settings{PreferredModel, Catalog}`。适配器可以 import 它，不能 import `internal/point`（避免环）。

`server/clients.go` `clientSettings` 从 `modelCatalog` 填 `Catalog`，`PreferredModel` 固定为 `gateway-default`。这样切路由不必改客户端文件。

`SyncSettings`：已经托管的配置就地改目录，不新建还原点。Grok 收缩目录时只删 `ai-gateway` / `ai-gateway:` 前缀条目，用户自己的模型必须留下。

Codex 的占位环境变量在 `point/environment_windows.go`。非 Windows 上持久环境不可用会明确失败。

---

## 10. 日志、用量、诊断

| 文件 | 职责 |
|---|---|
| `server/trace.go` | 每请求打开 JSONL，写 `request` / `route` / `upstream_request` / 事件 / `warning` / `result` |
| `logstore/store.go` | 追加、列表、详情、用量汇总、doctor 巡检 |
| `logstore/warning.go` | 能力降级等警告事件 |
| `server/logs.go` | 管理查询 API |
| `server/doctor.go` | 配置、钥匙、日志目录、指向状态、登录启动 |

日志路径：`<data root>/logs/<本地日期>/<request-id>.jsonl`。`logging.enabled == false` 时新请求不得建文件。

禁止写入：`Authorization`、`x-api-key`、Cookie、token、secret。省略的敏感头只记数量。用量只信上游 `result.usage`，缺失标 `incomplete`，禁止估算。

---

## 11. 桌面

### 11.1 Go 壳 `cmd/desktop`

| 文件 | 职责 |
|---|---|
| `main.go` | embed 资源、开窗口、关窗隐藏、组装托盘；`serve` 参数转给 `app.Main` |
| `launcher.go` | `ensureGateway`：`healthz` 不通就 `exe serve` 拉起独立进程 |
| `process_windows.go` / `process_other.go` | 分离进程、不继承控制台 |
| `tray.go` | 菜单：状态、打开窗口、三个客户端的已启用 `供应商/模型ID`、日志、登录启动、启停网关、退出桌面 |
| `trayclient.go` | 托盘自己的管理 HTTP 客户端 |

退出托盘不等于停止网关。停止网关是菜单上单独一项。

### 11.2 前端 `desktop/src`

| 文件 | 职责 |
|---|---|
| `main.tsx` | React 入口 |
| `App.tsx` | 七个页面：`Overview` `Providers` `Routes` `Clients` `Logs` `Usage` `SettingsPage` |
| `api.ts` | 全部 `/api/v1` 调用；基址来自 `?api=` 或 `VITE_API_URL` |
| `types.ts` | 与管理 API 对齐的类型 |
| `catalog.ts` | `enabledCatalog`：扁平已启用 `provider/model` 列表 |
| `validation.ts` | 供应商表单校验 |
| `i18n.ts` | 简体中文 / English |
| `styles.css` | Geist 视觉，明暗主题 |

`Routes`：上面是供应商/模型启用树，下面是四个客户端的「默认选中的模型」单选框，选项来自 `enabledCatalog`。应用时 `PUT /api/v1/routes/{client}`，body 仍是 `{provider, model}`，其中 `model` 是上游模型名，不是带前缀的目录 id。

测前端：`desktop/src/*.test.ts*`（vitest）、`desktop/e2e/desktop.spec.ts`（Playwright，`127.0.0.1:9245`，1440×900 浅色 + 390×844 深色）。

---

## 12. 平台与脚本

| 包 | 平台文件 |
|---|---|
| `internal/app` | `datadir_windows.go` / `datadir_unix.go` |
| `internal/process` | `lock_windows.go` / `lock_unix.go`，`signal_windows.go` / `signal_unix.go` |
| `internal/secret` | `store_windows.go` / `darwin` / `linux` / `other` |
| `internal/autostart` | Windows 计划任务 XML 回读；macOS launchd；Linux 用户 systemd |
| `internal/point` | `environment_windows.go` / `environment_other.go` |
| `cmd/desktop` | `process_windows.go` / `process_other.go` |

脚本：

| 脚本 | 作用 |
|---|---|
| `scripts/verify.ps1` | §16.3 统一校验；不能跑的步骤必须打印 `SKIPPED` |
| `scripts/build-desktop.ps1` | 只打桌面 |
| `scripts/build-release.ps1` | Windows 发布 zip（先 `npm ci` + `npm run build`）。改完会进入发布二进制的代码后必须跑；见 `docs/progress.md` 发布约定 |
| `scripts/build-cross.ps1` | macOS / Linux 无头交叉构建 |

---

## 13. 测试夹具落在哪

| 路径 | 内容 |
|---|---|
| `testdata/protocols/chat/` | Chat 请求 / 响应 / SSE / 图片 / reasoning / 错误 |
| `testdata/protocols/responses/` | 同上，Responses |
| `testdata/protocols/messages/` | 同上，Messages |
| `testdata/point/` | 已有客户端配置样例（无真实钥匙） |

测试里用 `filepath.Join("..", "..", "testdata", ...)`。禁止往夹具里写真实钥匙、Cookie、账号、个人路径。

主集成测试：

- 同协议：`internal/server/dataplane_test.go`（假上游）
- 跨协议：`internal/server/crossprotocol_test.go`
- 管理面：`management_test.go`、`providers_test.go`、`providers_tx_test.go`
- 指向：`internal/point/point_test.go`、`internal/server/clients_test.go`
- 可用性：`internal/route/availability_test.go`、`server/feature_test.go`

改协议行为时至少跑对应包和 `go test ./internal/server -run TestCross`。不要为了变绿批量覆盖金样。

---

## 14. 磁盘上的运行时布局

数据根默认 `%USERPROFILE%\.ai-gateway` 或 `~/.ai-gateway`，可被 `AI_GATEWAY_DATA_DIR` 覆盖。测试依赖这个覆盖。

```text
<data root>/
  config.yaml
  gateway.lock
  gateway.pid.json
  secrets/<ref>.bin          仅 Windows DPAPI 密文
  logs/<日期>/<request-id>.jsonl
  backups/<client>/<utc>/
      <原文件名>
      manifest.json
```

客户端自己的文件不在数据根里：`~/.codex/config.toml`、`~/.claude/settings.json`、`~/.grok/config.toml`（可被各自环境变量改路径）。

---

## 15. 常见改动的走法

**改路由算法或 enabled 语义**  
`internal/route/route.go` → `route_test.go` + `availability_test.go` → `dataplane_test.go` 里四客户端隔离和前缀覆盖。

**改客户端里能看到哪些模型、显示成什么**  
`dataplane.go` `modelCatalog`（`display_name` 必须等于 `id`）→ `clients.go` `clientSettings` → Grok 适配器 `point/grok` 的 `name`，Codex 适配器 `point/codex/catalog.go`。桌面选择器另改 `desktop/src/catalog.ts`。不要改模型 id 去迎合 Claude 的过滤器。

**加一种管理 API**  
`handlers.go` 注册 → 新文件或现有 `*api.go` → `apierror.go` 信封 → 桌面若要用再加 `desktop/src/api.ts`。桌面不得直接写 `config.yaml`。

**加跨协议字段**  
先扩 `internal/ir`，再同时改进出站和入站，最后在 `crossprotocol_test.go` 加六个方向里受影响的那些。禁止 inbound 调 outbound。

**修钥匙泄漏**  
查 `providers.go` 事务、`upstream` 的 `Zero`、`trace.go` / `logstore` 的字段过滤、doctor 响应。测试里用 `NewMemStore`，不要打真实 DPAPI，除非跑 `store_windows_test.go`。

**桌面只改文案或布局**  
`i18n.ts` / `styles.css` / `App.tsx` → `npm --prefix desktop run test` → `npm run test:e2e` → `npm run build` 更新 embed 资源。
