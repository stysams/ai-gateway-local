# ai-gateway

`ai-gateway` 是一个运行在本机当前用户会话中的单用户人工智能代理网关。它让 Codex、Claude Code、Grok Build，以及兼容 OpenAI 或 Anthropic 协议的本地应用统一通过一个本地地址访问多个上游模型。

网关负责本地路由、协议转换、模型目录、请求日志和用量统计。上游 API 密钥保存在操作系统的密钥存储中，不写入 `config.yaml`、客户端配置文件、管理接口响应或正文日志。

默认监听地址为 `127.0.0.1:12600`。桌面程序只负责管理网关，不承载 `/v1/*` 数据面请求。

> 当前版本：`0.1.0-rc1`（Windows amd64）。功能主线已完成，本文及相关文档均以现有发布包为唯一版本基准，不再维护其他优化规划。第一期五项真实验收遗留和源码、发布包差异保留为版本记录，详见 [`docs/feature-checklist.md`](docs/feature-checklist.md)。

## 最新更新

### 2026-08-24

- 供应商伪装客户端新增 Pi。第三方 `/v1` 请求可按 Pi 客户端身份发送 `User-Agent: Pi Agent/1.0`，桌面供应商表单同时提供 Pi 预设。
- 客户端高级设置新增 Codex 与 Claude Code 的子代理模型、标题生成模型选择；空值跟随当前客户端路由。

[查看完整发布说明](docs/releases/release-notes.md)

## 功能概览

- 支持 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages，包括三协议间的 JSON Schema 结构化输出和严格函数工具转换。
- 为 Codex、Claude Code、Grok Build 和通用应用分别配置默认路由。
- 一个提供商可以维护多个模型，每个模型可以单独选择 Chat、Responses 或 Messages 接口，并可从上游模型接口发现模型元数据。
- 支持图片输入、推理或思考内容，以及不支持能力时的明确降级提示。
- 为每个请求记录脱敏的 JSONL 事件，并汇总上游真实返回的令牌用量。
- 支持客户端指向网关、漂移检测、还原、Codex 远程压缩，以及 Codex 与 Claude Code 的辅助模型分流。
- 提供 Windows 桌面程序、系统托盘和命令行管理工具。
- 默认只监听回环地址；需要局域网访问时可以显式改为 `0.0.0.0`。

## 安装

### Windows 发布包

从发布目录取得 Windows amd64 压缩包，例如：

```text
dist/ai-gateway-0.1.0-rc1-windows-amd64.zip
```

解压整个目录到稳定的当前用户目录，例如：

```text
%LOCALAPPDATA%\Programs\ai-gateway
```

压缩包包含：

- `ai-gateway.exe`：无头网关和命令行程序。
- `ai-gateway-desktop.exe`：桌面窗口、系统托盘和桌面管理流程。
- `README.md`、`LICENSE` 和 `docs/install.md`。

当前版本元数据：

- 版本：`0.1.0-rc1`
- 包内构建提交：`829777f7b4913020f5619787a65837acfd3ab4f0`
- Windows amd64 压缩包：`dist/ai-gateway-0.1.0-rc1-windows-amd64.zip`
- 压缩包 SHA-256：`F32A38C208ECF74FEBA2C7EBF0C5D9E6DA6706651E3A46CC33487655002355CC`

双击 `ai-gateway-desktop.exe` 即可启动桌面程序。桌面程序会在网关未运行时启动独立的 `serve` 进程；再次启动桌面程序只会聚焦已有窗口，不会创建第二个网关进程或托盘图标。

关闭窗口只会隐藏桌面窗口，网关仍然运行。需要停止网关时，请使用托盘菜单中的停止操作，或执行 `ai-gateway.exe stop`。

启用登录启动后不要直接移动安装目录。需要移动时，先关闭登录启动，移动目录，再重新启用，否则诊断工具会报告旧的可执行文件路径。

### 从源码构建

开发环境需要：

- Go `1.26+`，项目工具链固定为 `go1.26.6`。
- Node.js 和 npm。
- Windows 桌面构建需要 Wails `v3.0.0-beta.8` 所需的本机 WebView 工具链。

常用构建命令：

```powershell
go build ./...
npm --prefix desktop ci
npm --prefix desktop run build
```

前端构建会更新 `cmd/desktop/assets/`，这些资源会被桌面程序嵌入。完整 Windows 发布包使用：

```powershell
.\scripts\build-release.ps1 -Version 0.1.0-rc1 -Commit <完整提交哈希>
```

交叉构建只生成 macOS 和 Linux 的无头网关程序。桌面程序需要在目标系统本机进行构建：

```powershell
.\scripts\build-cross.ps1 -Version 0.1.0-rc1 -Commit <完整提交哈希>
```

## 快速开始

### 1. 启动网关

使用桌面程序，或者在终端前台启动：

```powershell
.\ai-gateway.exe serve
```

默认地址为 `http://127.0.0.1:12600`。可以只为本次运行临时覆盖端口，配置文件不会被修改：

```powershell
.\ai-gateway.exe serve --port 12601
```

启动成功后检查健康状态：

```powershell
Invoke-RestMethod http://127.0.0.1:12600/healthz
Invoke-RestMethod http://127.0.0.1:12600/readyz
```

两个接口都应返回成功结果。`readyz` 还会检查配置、系统密钥存储和必需的提供商密钥。日志目录、客户端指向和登录启动等完整检查请使用 `doctor`。

### 2. 添加提供商

在桌面程序中打开“提供商”页面，选择“添加提供商”，填写：

1. 提供商标识，例如 `openrouter` 或 `ollama`。
2. 显示名称。
3. 适配器：`openai-chat`、`openai-responses` 或 `anthropic`。
4. 上游基础地址，例如 `https://openrouter.ai/api/v1`。
5. API 密钥（如果上游需要）。
6. 默认模型和模型目录。

填写基础地址后，可以使用“获取模型”读取上游模型列表。只有上游明确返回的上下文窗口和最大输出令牌才会被填入；缺少的数据会保持未知，网关不会根据模型名称猜测限制。

API 密钥只通过写入接口进入系统密钥存储。读取提供商列表、配置或日志时不会返回明文密钥。

### 3. 配置路由

在“路由”页面为 Codex、Claude Code、Grok Build 和通用应用选择默认模型。

路由是客户端启动时的首选模型，不是唯一可用模型。客户端被指向网关后，仍然可以从模型目录选择任何已启用的 `提供商标识/模型标识`。

在提供商模型树中禁用提供商或模型后，它会从 `/v1/models` 中移除，也不会再参与路由解析。

### 4. 连接本地应用

在桌面程序的“AI 中台”页面查看当前连接参数。常见的 OpenAI 兼容应用可以使用：

```text
Base URL:   http://127.0.0.1:12600/v1
API Key:    ai-gateway
Model:      gateway-default 或 <provider-id>/<model-id>
Models URL: http://127.0.0.1:12600/v1/models
```

`ai-gateway` 只是要求非空密钥的应用使用的占位值，不是网关凭据。回环数据面不会验证这个值，也不会把它转发给上游。

`gateway-default` 使用“通用应用”路由。指定完整的 `提供商标识/模型标识` 可以直接选择某个已启用模型。

### 5. 指向客户端

桌面程序的“客户端”页面可以为 Codex、Claude Code 和 Grok Build 执行指向、查看状态和还原。指向操作会：

- 创建带有 SHA-256 清单的备份。
- 原子写入客户端配置。
- 将客户端默认地址改为本机网关对应的客户端路径。
- 同步该客户端可用的模型目录。
- 按高级设置同步子代理和标题生成模型；Codex 在请求到达时分类，Claude Code 写入对应模型槽位。
- 在失败时尝试还原原始内容。

也可以使用管理接口执行相同操作，具体接口见下方“管理接口”。开始真实客户端验收前，请确认客户端已经安装，并准备好至少一个可用的上游提供商。

## 命令行使用

在 Windows 发布目录中执行：

```powershell
.\ai-gateway.exe serve             # 前台运行网关
.\ai-gateway.exe stop              # 请求优雅关闭
.\ai-gateway.exe status            # 查看运行状态
.\ai-gateway.exe doctor            # 查看诊断报告
.\ai-gateway.exe autostart on      # 开启当前用户登录启动
.\ai-gateway.exe autostart off     # 关闭当前用户登录启动
.\ai-gateway.exe version            # 查看版本、提交、构建时间和平台
```

`serve --port N` 只覆盖本次运行使用的端口。配置文件仍然是下一次启动的唯一来源。

`status`、`stop` 和 `doctor` 会根据正在运行实例记录的监听地址访问管理接口，因此也可以正确处理使用临时端口启动的实例。

## 支持的接口

### 数据面

通用应用使用无客户端前缀的路径：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/models` | 查看通用应用的模型目录 |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions |
| `POST` | `/v1/responses` | OpenAI Responses |
| `POST` | `/v1/responses/compact` | Codex 远程压缩转发 |
| `POST` | `/v1/messages` | Anthropic Messages |

Codex、Claude Code 和 Grok Build 使用客户端前缀：

```text
GET  /c/{client}/v1/models
POST /c/{client}/v1/chat/completions
POST /c/{client}/v1/responses
POST /c/{client}/v1/responses/compact
POST /c/{client}/v1/messages
```

`{client}` 只能是 `codex`、`claude`、`grok` 或 `generic`。无前缀的 `/v1/*` 等价于 `/c/generic/v1/*`。

### 管理面

管理接口统一使用 `/api/v1` 前缀，与数据面共用监听器。管理接口只接受真实 TCP 回环来源；默认的 `127.0.0.1` 配置只允许本机访问，即使将监听地址改为 `0.0.0.0`，局域网客户端也不能读取配置、日志、供应商信息或触发关闭。数据面 `/v1/*` 仍按监听地址提供服务：

数据面资源边界可以在配置的 `limits` 中设置全局、客户端和提供商并发、流式空闲超时、请求体和请求头字节上限，以及单客户端每分钟请求数。填 `0` 表示对应限制关闭；达到并发、速率、请求体或请求头上限时网关分别返回协议原生 `429`、`413` 或 `431`，不会在内部排队或调用上游。

`GET /api/v1/metrics` 提供本地请求计数、状态分类、总延迟和首字节延迟百分位、上游错误、活跃请求与活动日志会话。指标默认不包含正文、请求参数或密钥。上游连续失败时，网关会按提供商短暂熔断并返回 `503 provider_circuit_open`；不会自动重试可能产生副作用的请求。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/status` | 网关状态、客户端状态和路由 |
| `GET` | `/api/v1/metrics` | 本地请求计数、状态分类、延迟百分位和日志活动会话数，不包含正文或密钥 |
| `POST` | `/api/v1/shutdown` | 请求优雅关闭 |
| `GET` | `/api/v1/doctor` | 配置、密钥、日志和客户端诊断 |
| `GET` / `PUT` | `/api/v1/config` | 读取或更新配置 |
| `GET` / `POST` | `/api/v1/providers` | 列出或创建提供商 |
| `GET` / `PUT` / `DELETE` | `/api/v1/providers/{id}` | 读取、更新或删除提供商 |
| `PUT` | `/api/v1/providers/{id}/availability` | 更新提供商或模型可用性 |
| `POST` | `/api/v1/providers/{id}/probe` | 探测提供商连通性 |
| `GET` | `/api/v1/providers/{id}/models` | 读取提供商模型列表 |
| `POST` | `/api/v1/provider-models/discover` | 发现未保存提供商的模型 |
| `PUT` | `/api/v1/routes/{client}` | 更新客户端路由；当前路由从状态接口读取 |
| `GET` | `/api/v1/clients/{client}` | 查看客户端指向状态 |
| `POST` | `/api/v1/clients/{client}/point` | 指向网关并创建备份 |
| `POST` | `/api/v1/clients/{client}/restore` | 还原客户端配置 |
| `PUT` | `/api/v1/clients/{client}/remote-compaction` | 更新客户端远程压缩设置 |
| `PUT` | `/api/v1/clients/{client}/helper-models` | 更新 Codex 或 Claude Code 的子代理与标题生成模型 |
| `GET` | `/api/v1/logs` | 查询请求日志 |
| `GET` | `/api/v1/logs/{request_id}` | 查看单个请求详情 |
| `GET` | `/api/v1/logs/{request_id}/export` | 下载强制脱敏的 JSONL 日志 |
| `DELETE` | `/api/v1/logs/{request_id}` | 删除单条非活动请求日志 |
| `DELETE` | `/api/v1/logs` | 清理全部非活动请求日志 |
| `GET` | `/api/v1/usage` | 查看用量汇总 |
| `PUT` | `/api/v1/logging` | 开关请求日志、正文保存和写盘脱敏 |
| `PUT` | `/api/v1/autostart` | 开关当前用户登录启动 |
| `GET` | `/api/v1/local-access` | 获取当前连接参数和模型目录 |

## 配置和数据目录

配置文件和运行数据默认位于：

```text
Windows: %USERPROFILE%\.ai-gateway
macOS/Linux: ~/.ai-gateway
```

可以使用环境变量覆盖数据根目录：

```powershell
$env:AI_GATEWAY_DATA_DIR = "D:\ai-gateway-data"
```

数据目录通常包含：

- `config.yaml`：网关配置和路由，不包含明文 API 密钥。
- `secrets/`：Windows DPAPI 等系统密钥存储产生的密文文件。
- `logs/`：请求 JSONL 日志，是否保存正文和写盘脱敏由日志设置控制。
- `gateway.lock`：单实例锁。
- `gateway.pid.json`：运行实例的进程和监听地址元数据。

默认只监听 `127.0.0.1`。如果确实需要局域网内的其他设备访问，在桌面“设置”页面将监听地址改为 `0.0.0.0`，并确认防火墙和网络边界。管理接口仍然只允许回环来源，不信任 `X-Forwarded-For` 等转发头；需要管理操作时必须在网关所在机器上执行桌面或命令行操作。

## 日志和安全

- 回环数据面当前不要求鉴权；应用填写的占位 API key 会被忽略。
- 管理接口只允许回环来源；切换为 `0.0.0.0` 只扩大数据面暴露范围，不会把配置、日志、供应商信息或关闭接口开放给局域网。
- 上游密钥只进入当前操作系统的密钥存储，不回退到明文文件。
- 请求日志默认递归脱敏 Authorization、Cookie、API key、token、password、secret 和 session 等常见凭据字段；桌面复制和下载会再次强制脱敏历史日志。
- 自由文本仍可能包含提示词、源代码或个人信息，首次启用正文日志时需要在桌面程序中确认风险；敏感环境不应仅依赖字段名脱敏。
- 活动请求日志不能手动删除；单条删除和批量清理只处理已经停止写入的文件。
- 启用 `0.0.0.0` 前请先完成网络隔离和访问控制评估。

## 开发和验证

运行 Go 测试、静态检查和桌面端测试：

```powershell
go test ./...
go vet ./...
npm --prefix desktop run test
npm --prefix desktop run lint
npm --prefix desktop run build
.\scripts\verify.ps1
```

启用竞态测试还需要 cgo 和可执行的 C 编译器（Windows 通常使用 mingw-w64 gcc）：

```powershell
go test -race ./...
```

桌面前端的开发服务器：

```powershell
npm --prefix desktop run dev
```

默认访问地址为 `http://127.0.0.1:5173/`。开发服务器只用于预览前端，桌面程序正式使用的是 `cmd/desktop/assets/` 中的嵌入资源。

完整协议、架构、管理接口合同和 Windows 最终验收步骤请参阅：

- [协议和架构规格](docs/v1-scheme.md)
- [安装、运行和还原指南](docs/install.md)
- [运维与故障排查](docs/operations.md)
- [开发进度和发布约定](docs/progress.md)
- [功能清单与当前版本记录](docs/feature-checklist.md)
- [已归档的优化路线图](docs/optimization-roadmap.md)
- [代码结构说明](docs/code-map.md)

## 许可证

本项目使用 MIT 许可证，详见 [LICENSE](LICENSE)。
