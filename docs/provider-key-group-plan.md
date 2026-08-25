# 按密钥分组的供应商管理方案

> 状态：实施中 v0.3
> 日期：2026-08-25
> 适用版本：`0.1.0-rc1` 之后的下一阶段变更
> 本文记录已批准并正在落地的新契约。明文密钥策略、统一 `claude` 命名、Anthropic `/v1/messages`、共享 `base_url`、无旧供应商兼容、按密钥组探测、按当前路由检查 `readyz` 和删除引用资源返回 `409` 均已确认。

## 1. 背景

当前供应商模型是：一个供应商拥有共享 `base_url`，并由多个密钥组分别拥有 API Key、协议端点和模型目录。模型可以覆盖密钥组级适配器，也可以通过 `custom` 端点覆盖请求路径。

这无法表达下面这种真实配置：同一个上游地址下，不同密钥拥有不同模型集合；同一个供应商下，GPT 模型和 Claude 模型还需要使用不同的请求接口。

目标结构是：

```text
供应商 agent
  共享：名称、Base URL、共享请求头、能力开关
  密钥组 gpt
    密钥组名称：只有 gpt 模型
    密钥值：在配置文件中明文保存
    默认端点：无
    模型 gpt-5.4       -> /v1/responses
    模型 gpt-5.6       -> /v1/chat/completions
  密钥组 claude
    密钥组名称：只有 claude 模型
    密钥值：在配置文件中明文保存
    默认端点：由产品决定
    模型 claude-sonnet-4-6 -> 跟随密钥组端点
```

## 2. 现状核对

以下结论来自当前源码，而不是对系统行为的推测：

| 当前能力 | 位置 | 对本次变更的影响 |
|---|---|---|
| `config.Provider` 只有一个 `SecretRef` | `internal/config/config.go` | 必须新增密钥组层，并把新密钥改为密钥组内的 `api_key` 明文字段 |
| provider 有 `BaseURL`、`ExtraHeaders`、`Capabilities` | `internal/config/config.go` | 这些字段可继续留在供应商层，作为所有密钥组的共享配置 |
| 模型已有 `Adapter`、`Endpoint` 覆盖能力 | `internal/config/config.go`、`internal/endpoint/endpoint.go` | 新方案保留模型级协议和端点字段；旧供应商结构不属于本阶段运行时兼容范围 |
| `route.Resolve` 只返回 provider 和 model | `internal/route/route.go` | 路由结果必须增加密钥组标识，默认路由也必须能选中密钥组 |
| 数据面 `providerInfo` 只携带一个 `secretRef` 和一个端点 | `internal/server/dataplane.go` | 出站前必须根据 provider、密钥组、模型计算最终密钥和端点 |
| 三个出站池以 `baseURL + secretRef` 创建客户端 | `internal/outbound/*` | 客户端接口需要接收已解析的密钥组 `api_key`，连接池仍可以共享 |
| probe 和模型发现按 provider 执行 | `internal/server/providerprobe.go` | probe、发现和缓存都必须按密钥组执行，否则会混用模型目录和密钥 |
| Provider 页面只有一个 API key 和一张模型表 | `desktop/src/App.tsx`、`desktop/src/types.ts` | 页面需要改成供应商下的密钥组树形编辑流程 |
| 模型目录使用 `<provider-id>/<key-id>/<model-id>` | `internal/server/dataplane.go`、`internal/point` | 新目录需要包含密钥组，避免同名模型冲突 |

## 3. 设计目标与非目标

### 3.1 目标

1. 同一个供应商可以保存多个密钥组。
2. 每个密钥组拥有独立的密钥、名称、启用状态、默认端点和模型目录。
3. 每个模型可以继承密钥组端点，也可以覆盖密钥组端点。
4. 不同密钥组可以声明同名模型而不发生路由歧义。
5. 模型发现和连接探测必须使用指定密钥组的密钥和端点。
6. 本阶段不实现旧供应商配置自动迁移；旧供应商由其他维护方负责，当前解析器拒绝旧 provider-level 字段。
7. 密钥组的 `api_key` 明文保存到配置文件，并可通过本机管理 API 和桌面端查看、复制和比较；请求日志、上游日志、错误消息和客户端配置仍不得记录密钥。
8. 已指向的 Codex、Claude Code 和 Grok Build 仍能使用新的完整模型目录。

### 3.2 非目标

1. 本阶段不实现密钥之间的轮询、负载均衡或自动故障转移。
2. 本阶段不把不同密钥组放到不同主机上。供应商仍然共享一个 `base_url`。
3. 本阶段不为密钥组增加独立请求头、独立能力开关或独立限流。若后续有需要，可以在同一层扩展。
4. 本阶段不改变三种现有出站报文协议的语义，不把不兼容协议强行转换成成功响应。
5. 本阶段不修改客户端原生配置中的真实上游密钥。point 仍只写网关占位值。

## 4. 术语

| 术语 | 定义 |
|---|---|
| 供应商 provider | 一个共享上游连接的逻辑对象，拥有 `provider_id`、名称、Base URL、共享请求头和能力开关 |
| 密钥组 key group | 供应商下的一组上游凭据及其模型目录。密钥组标识是非敏感的逻辑 ID，不是密钥值 |
| 密钥值 secret value | 真实 API key 或等价凭据，保存在密钥组的 `api_key` 字段中。该字段在配置文件中明文存在，属于本方案明确接受的风险 |
| 密钥组标识 `key_id` | 用户可见、可用于路由和模型目录的稳定标识，例如 `gpt`、`claude` |
| 组级端点 | 密钥组的默认请求路径。组内没有端点覆盖的模型继承此值 |
| 模型级端点 | 某个模型的请求路径。设置后覆盖组级端点 |
| 规范模型 ID | 新格式的完整标识：`<provider-id>/<key-id>/<model-id>` |

## 5. 配置模型

### 5.1 推荐的新配置形状

建议把 `key_groups` 作为 provider 下的 map。使用 map 可以让 `key_id` 在一个供应商内天然唯一，并且让配置字段路径稳定，例如 `providers.agent.key_groups.gpt`。

```yaml
providers:
  agent:
    name: agent路由
    base_url: https://example.invalid
    extra_headers: {}
    capabilities:
      image_input: true
      reasoning: true
      context_management: false
    key_groups:
      gpt:
        name: 只有gpt模型
        enabled: true
        api_key: "sk-example-agent-gpt"
        endpoint: ""
        default_model: gpt-5.4
        models:
          - id: gpt-5.4
            endpoint: /v1/responses
            enabled: true
          - id: gpt-5.6
            endpoint: /v1/chat/completions
            enabled: true
      claude:
        name: 只有claude模型
        enabled: true
        api_key: "sk-example-agent-claude"
        endpoint: /v1/messagess
        default_model: claude-sonnet-4-6
        models:
          - id: claude-sonnet-4-6
            enabled: true
```

说明：

- `api_key` 是配置中的明文字段。示例中的值只是占位符，真实配置会保存用户输入的完整密钥。
- `api_key` 可以在密钥组详情中读取；桌面端必须提供显示、复制和清除操作，不能只返回 `has_secret`。
- 同一个供应商下的非空 `api_key` 按完整字节值比较，重复时显示重复组列表。重复默认产生可确认的警告，不自动禁止保存，因为同一把密钥可能被用户有意拆成多个模型组。
- `endpoint` 为空表示该密钥组不提供默认端点。此时每一个会被调用的模型都必须填写模型级端点。
- 模型端点为空时继承密钥组端点，继承关系必须在配置校验和管理 API 返回中可解释。
- `default_model` 用于 `gateway-default`、密钥组 probe 和新配置迁移后的默认路由。
- `adapter` 可作为密钥组或模型的显式协议字段；新配置不依赖旧 provider-level `adapter`、`default_model`、`models` 或 `secret_ref`。
- provider 级 `base_url`、`extra_headers`、`disguise_client` 和 `capabilities` 继续共享。密钥组只承载与凭据和模型集合直接相关的字段。

### 5.2 数据约束

新增校验规则：

1. `key_id` 在同一个 provider 内唯一，格式建议复用 provider ID 规则：`^[a-z][a-z0-9_-]{0,31}$`。
2. `key_groups` 至少有一个组时，provider 的旧 `secret_ref`、旧 `default_model` 和旧 `models` 不再作为运行时数据源。
3. 一个密钥组的 `default_model` 必须是该组已声明且已启用的模型。
4. 一个模型的有效端点按以下顺序确定：模型级端点、密钥组端点。新配置不能在这两层都为空时进入数据面。
5. 模型端点和密钥组端点都必须是以 `/` 开头的路径，不包含查询串、片段、空白或换行。
6. 已知协议端点必须继续遵守当前协议路径约束：`/chat/completions`、`/responses`、`/messages`。
7. 端点对应的出站协议必须能够唯一确定。对于不能由路径后缀确定协议的非标准路径，必须新增显式协议字段后才允许保存，禁止按供应商名称或模型名称猜测。
8. 同一个密钥组内模型 ID 必须唯一；不同密钥组可以有相同模型 ID。
9. 删除仍被路由引用的密钥组必须返回冲突错误，不得自动改路由。
10. 删除仍被默认路由引用的模型，应返回冲突错误，或要求先修改对应路由。具体采用哪种接口行为在实现前固定。

### 5.3 端点继承和协议解析

最终请求目标使用以下确定性流程：

```text
请求模型
  -> 解析 provider、key_id、model
  -> 读取 provider.base_url
  -> 读取 key_groups[key_id]
  -> model.endpoint 非空？使用 model.endpoint
  -> 否则 key_group.endpoint 非空？使用 key_group.endpoint
  -> 两层都为空则配置校验失败
  -> 端点映射到 openai-chat / openai-responses / anthropic
  -> 注入该 key_group.api_key 对应的密钥
```

新配置的建议规则是：

| 模型端点 | 密钥组端点 | 有效端点 | 结果 |
|---|---|---|---|
| 有 | 有或无 | 模型端点 | 模型覆盖密钥组 |
| 无 | 有 | 密钥组端点 | 模型跟随密钥组 |
| 无 | 无 | 无 | 配置校验失败，或显式请求时返回可定位错误 |

路径拼接需要明确区分两类：

- 预设端点：沿用当前 `endpoint.Join` 的 `/v1` 补全和重复 `/v1` 消除规则。
- 显式端点：按用户填写的路径拼接，不按主机名猜测，也不静默补 `/v1`。实现时需要补充“Base URL 已含路径”和显式 `/v1` 的测试矩阵。

## 6. 路由和模型标识

### 6.1 路由字段

当前 route 只有 `provider` 和 `model`。建议增加 `key_id`：

```yaml
routes:
  generic:
    provider: agent
    key_id: gpt
    model: gpt-5.4
```

`model` 仍保存发送给上游的原始模型名，不保存带前缀的目录 ID。

`gateway-default` 的含义变为使用当前客户端 route 的 provider、key group 和 model。只填写 provider 而不填写 key 的旧 route，在迁移后必须补成实际密钥组。

### 6.2 请求模型解析顺序

推荐的新解析顺序如下：

1. 先解码 Claude Code 选择器别名。
2. 空模型或 `gateway-default` 使用当前客户端完整 route。
3. 完整三段 ID `<provider-id>/<key-id>/<model-id>` 命中已启用 provider 和密钥组时，使用指定目标。
4. 旧的两段 ID `<provider-id>/<model-id>` 不再支持；调用方必须使用三段 ID。
5. 对 `generic`，裸模型名只在全局唯一归属到一个 provider、一个密钥组时成功。
6. 若裸模型名在当前 route 的 provider 和 key group 中唯一匹配，可以使用当前 route 消除归属。
7. 其他情况在接触上游前返回未匹配或歧义错误。

模型 ID 含 `/` 时，解析必须只把第一个分隔段作为 provider，再按第二个分隔段尝试识别 key ID，剩余部分完整保留为上游模型名。若模型名本身含 `/`，必须有测试证明三段 ID仍可逆解析。

### 6.3 模型目录

新模型目录的规范 ID 使用：

```text
gateway-default
agent/gpt/gpt-5.4
agent/gpt/gpt-5.6
agent/claude/claude-sonnet-4-6
```

这样即使两个密钥组都提供 `gpt-5.4`，客户端选择器仍然可以区分它们。除 Claude Code 专用别名外，`display_name` 继续等于规范 ID，符合现有合同。

Claude Code 的 `claude-gw*` 别名需要把新的完整模型 ID 当作整体编码。现有 v2/v3 转义规则可以继续使用，但必须增加包含 key ID 的测试和旧别名解码测试。

Codex sidecar、Grok 配置、Claude 缓存和 `/c/{client}/v1/models` 必须来自同一份新模型目录，避免桌面、客户端选择器和数据面产生不同结果。

## 7. 明文密钥与事务

本节是本方案相对于当前仓库安全合同的明确变更。当前 `docs/v1-scheme.md` §6 和仓库规则要求真实密钥不得进入 YAML、管理 API 响应或桌面存储；本方案为了支持用户查看、复制和重复比较，改为把密钥组的 `api_key` 明文保存到 `config.yaml`。未经该变更批准，不得开始实现。

### 7.1 明文存储范围

每个密钥组直接保存 `api_key`：

```yaml
key_groups:
  gpt:
    api_key: "用户输入的完整 API key"
```

明文密钥允许出现在以下用户明确需要的范围：

- `config.yaml` 的密钥组 `api_key` 字段
- `GET /api/v1/providers/{provider_id}/keys` 和单个密钥组详情响应
- 桌面端密钥组编辑器、重复检测和复制操作

除密钥组明确读取接口外，明文密钥仍然禁止出现在以下范围：

- 错误消息、普通管理 API 响应和数据面 HTTP 响应
- 请求正文日志、上游请求日志、探测响应记录和用量统计
- 客户端配置文件
- 截图、导出日志和桌面端缓存文件
- `GET /api/v1/providers` 的列表摘要，除非调用方明确请求密钥组详情

管理 API 仍然只接受真实 TCP 回环来源。密钥组详情接口返回明文是有意设计，不得扩展为远程管理能力。桌面端复制使用系统剪贴板，但不得把密钥写入网关日志、浏览器持久化存储或应用诊断文件。离开编辑页面后，前端状态应清理已经删除的密钥值。

重复检测规则：

1. 只比较同一个 provider 下的非空 `api_key`。
2. 比较使用完整字符串值，不自动删除首尾字符；保存前如果输入包含首尾空白，界面应提示用户确认。
3. 重复不自动拒绝保存，返回 `duplicate_key_groups` 告警和重复的 `key_id` 列表。
4. 不同 provider 的相同密钥不在本阶段自动合并，也不因重复而阻止保存。

### 7.2 密钥组写事务

新增或更新密钥组时，事务顺序建议为：

1. 校验 provider、key group、模型、端点和路由引用。
2. 在内存快照中写入或保留明文 `api_key`。
3. 检测同 provider 下的重复密钥，生成警告但不改变用户输入。
4. 原子写配置文件；配置写入失败时当前配置和当前密钥值都保持不变。
5. 发布新的配置快照。
6. 清理该 provider/key group 的模型发现缓存。

删除密钥组时：

1. 检查所有 route、客户端辅助模型和其他持久化引用。
2. 原子删除配置中的密钥组。
3. 配置提交成功后，从进程内快照和桌面响应中移除该组的明文 `api_key`。
4. 删除失败时保留原配置文件，不留下部分删除状态。

供应商级 `txMu` 仍然必须覆盖整个快照、重复检测和配置写入流程，不能让两个密钥组更新基于同一份旧快照互相覆盖。`internal/secret` 不再是新密钥组运行时的必需依赖；旧配置迁移期间仍可能需要它读取历史 `secret_ref`。

## 8. 管理 API

### 8.1 供应商接口调整

现有 provider CRUD 继续负责共享字段：

```text
GET    /api/v1/providers
POST   /api/v1/providers
GET    /api/v1/providers/{provider_id}
PUT    /api/v1/providers/{provider_id}
DELETE /api/v1/providers/{provider_id}
POST   /api/v1/providers/{provider_id}/probe
```

响应新增 `key_groups` 摘要。供应商列表只返回密钥组标识、名称、启用状态、`has_api_key` 和重复告警数量；明文 `api_key` 只通过显式密钥组列表或详情接口返回。旧 provider-level 字段不返回。

### 8.2 密钥组接口

建议增加独立资源接口，使单组密钥更新不会因为全量提交其他密钥组而产生误删：

```text
GET    /api/v1/providers/{provider_id}/keys
POST   /api/v1/providers/{provider_id}/keys
GET    /api/v1/providers/{provider_id}/keys/{key_id}
PUT    /api/v1/providers/{provider_id}/keys/{key_id}
DELETE /api/v1/providers/{provider_id}/keys/{key_id}
POST   /api/v1/providers/{provider_id}/keys/{key_id}/probe
GET    /api/v1/providers/{provider_id}/keys/{key_id}/models
```

写请求可以包含 `api_key`，并在保存后持久化。读取响应返回明文 `api_key`，以支持用户查看和复制：

```json
{
  "provider_id": "agent",
  "key_id": "gpt",
  "name": "只有gpt模型",
  "enabled": true,
  "api_key": "sk-example-agent-gpt",
  "has_api_key": true,
  "duplicate_key_groups": [],
  "endpoint": "",
  "default_model": "gpt-5.4",
  "models": [
    {"id": "gpt-5.4", "endpoint": "/v1/responses", "effective_endpoint": "/v1/responses"},
    {"id": "gpt-5.6", "endpoint": "/v1/chat/completions", "effective_endpoint": "/v1/chat/completions"}
  ]
}
```

`effective_endpoint` 是管理面用于解释继承关系的非敏感派生字段，不写回配置。`duplicate_key_groups` 只包含重复密钥所在的 `key_id`，不重复回显其他密钥。必要时同时返回 `effective_protocol`，避免桌面自行解析端点。

### 8.3 模型发现

现有 `POST /api/v1/provider-models/discover` 需要增加 `key_id` 和针对草稿密钥的处理：

- 编辑已有密钥组且请求没有 `api_key` 时，使用已保存的该组明文密钥。
- 新建密钥组时使用请求中的草稿 `api_key`；用户保存密钥组后写入配置，取消编辑则丢弃草稿。
- 发现请求使用该密钥组的 endpoint 或显式发现端点，并使用该组的身份头。
- 缓存键从 `provider_id` 改为 `(provider_id, key_id)`。
- 发现结果只更新当前编辑草稿，不污染其他密钥组和数据面配置。

### 8.4 路由接口

现有：

```json
{"provider":"agent","model":"gpt-5.4"}
```

新增字段：

```json
{"provider":"agent","key_id":"gpt","model":"gpt-5.4"}
```

新标准：

- route 必须包含 `key_id`。
- 未传 `key_id` 返回 `route_key_required`；不根据旧 provider/model 结构自动补全。

## 9. 探测、日志和诊断

### 9.1 Probe

probe 的目标从 provider 变为明确的 provider/key group/model：

1. 使用密钥组 `default_model`。
2. 根据模型级端点或组级端点生成请求 URL。
3. 使用该密钥组的明文 `api_key`。
4. 返回实际端点、协议、状态和延迟；probe 响应不回显 `api_key`，详情接口仍按本方案的明文读取规则返回。

供应商级 probe 可以保留，行为改为探测其默认密钥组；当供应商有多个密钥组且没有唯一默认组时，管理 API 必须要求调用方指定 `key_id`，禁止随机选择。

### 9.2 日志

`route` 事件增加：

```json
{
  "provider": "agent",
  "key_id": "gpt",
  "model": "gpt-5.4",
  "adapter": "openai-responses",
  "endpoint": "/v1/responses"
}
```

不记录：密钥值、`api_key`、认证头。端点路径和 key ID 属于配置标识，可以记录，但如果用户把敏感信息写入名称，管理面仍应对自由文本字段按现有日志策略处理。配置文件和密钥组详情 API 是本方案明确允许的例外，日志导出和请求追踪不属于例外。

用量查询建议新增可选 `key_id` 过滤，并在 provider、key group、model 三个层级提供汇总。若第一版不改用量聚合，至少要保证日志中保留 key ID，以便后续离线区分。

### 9.3 Doctor 和 readyz

doctor 应逐组检查：

- key group ID 和名称
- 是否启用
- `api_key` 是否为空
- 同 provider 下是否存在重复 `api_key`
- 默认模型是否存在并启用
- 有效端点是否可解析为支持的协议
- 是否存在被 route 引用但已删除或禁用的组、模型
- 是否存在迁移遗留的旧 provider 级 `secret_ref` 或系统密钥存储条目

`readyz` 不再要求系统密钥存储可用；它只检查当前 route 使用的密钥组是否启用、`api_key` 是否满足配置要求和默认模型是否有效，不对所有密钥组发真实上游请求。未使用的可选密钥组缺少 `api_key` 时，readyz 不阻断网关启动。

## 10. 后续版本迁移（本阶段不实施）

### 10.1 未来迁移原则

本阶段不读取、不转换、不写回旧 provider-level `adapter`、`default_model`、`models`、`secret_ref`。旧供应商配置由其他维护方负责；本项目收到旧字段时直接返回带路径的解析错误，不创建隐式 `default` 密钥组，也不访问系统密钥存储。

如果未来单独立项实现迁移，必须在 `internal/config.Parse` 或独立版本迁移层完成，不应由桌面临时拼装。原则如下：

1. 原始 YAML 在写入前完成解析和备份。
2. 只有完整迁移、校验和原子写都成功后，才发布新配置快照。
3. 迁移失败保留原文件，并返回带字段路径的错误。
4. 迁移必须幂等，重复启动不能继续创建新的密钥组。
5. 若未来单独实现迁移，旧系统密钥只在迁移内存中使用；本阶段不得读取系统密钥存储。
6. 未知顶层字段继续由 `Extra` 保留。

### 10.2 未来的旧 provider 到新 key group

对旧结构：

```yaml
providers:
  openrouter:
    secret_ref: provider.openrouter
    adapter: openai-chat
    default_model: anthropic/claude-sonnet-4
    models:
      - id: anthropic/claude-sonnet-4
        adapter: openai-chat
```

迁移为：

```yaml
providers:
  openrouter:
    key_groups:
      default:
        name: 默认密钥
        api_key: "从旧系统密钥存储读取的完整密钥"
        default_model: anthropic/claude-sonnet-4
        models:
          - id: anthropic/claude-sonnet-4
            adapter: openai-chat
```

迁移细节：

- 旧 `secret_ref` 指向的密钥必须从系统密钥存储读取，并写入新 `key_groups.default.api_key`；读取失败时迁移失败，原配置不覆盖。
- 旧 `default_model` 和 `models` 移入 `key_groups.default`。
- 旧 provider 的模型级 `adapter` 和 `endpoint` 原样保留到组内模型。
- 旧 provider 级 `adapter` 作为迁移兼容回退，直到组内所有有效端点都能确定协议。
- `routes.*.key_id` 自动填 `default`。
- 以上仅是未来单独迁移项目的设计草案，不属于当前实现；当前解析器直接拒绝旧字段。

### 10.3 未来迁移后的路由兼容

未来若另行迁移旧调用方，请求模型也必须显式改写为三段 ID；当前运行时不执行以下兼容解析：

- 恰好一个组拥有该模型：成功，并得到该组。
- 多个组拥有该模型：返回要求三段完整 ID 的歧义错误。
- 没有组拥有该模型：按照现有未匹配错误返回，不再透传到任意组。

## 11. 桌面端方案

### 11.1 Providers 页面

供应商列表仍展示供应商级信息，但模型数量改为“密钥组数量 + 模型数量”。展开供应商后显示密钥组列表：

- 密钥组标识和名称
- 启用状态
- 明文 API key 输入框、显示/隐藏和复制按钮
- 与其他密钥组重复时的明确告警
- 默认端点或“模型分别配置端点”
- 模型数量
- probe、编辑、删除操作

供应商编辑器分为两层：

1. 供应商共享设置：名称、Base URL、共享请求头、伪装客户端、能力开关。
2. 密钥组编辑器：标识、名称、明文 API key、启用状态、组级端点、默认模型和模型表。

模型表至少显示：

- 模型 ID
- 是否为密钥组默认模型
- 模型级端点
- 实际生效端点
- 实际报文协议
- 启用状态

模型端点为空时，界面必须明确显示“跟随密钥组”，不能显示成空白让用户误以为没有端点。

模型发现按钮必须位于密钥组层级，并且只用当前密钥组的草稿或已保存明文密钥。复制按钮只复制当前密钥组的值，并在复制成功后给出可见反馈。

### 11.2 Routes 页面

路由选择器的选项按以下层次展示，但保存值使用稳定字段：

```text
agent
  gpt
    gpt-5.4
    gpt-5.6
  claude
    claude-sonnet-4-6
```

保存请求使用 `{provider, key_id, model}`。模型目录的内部 ID 使用完整三段形式，避免 UI 选择后再自行猜测密钥组。

### 11.3 前端接口和测试影响

预计需要调整：

- `desktop/src/types.ts`：Provider、ProviderModel、Route、发现结果和 key group 类型。
- `desktop/src/api.ts`：密钥组 CRUD、按组 probe、按组发现模型、带 `key_id` 的 route 请求。
- `desktop/src/App.tsx`：Providers 编辑流程和 Routes 选择器。
- `desktop/src/validation.ts`：key ID、端点继承、默认模型和重复模型校验。
- `desktop/src/catalog.ts`：从三段规范模型 ID生成客户端选择器目录。
- `desktop/src/i18n.ts`：密钥组、端点继承、歧义和迁移提示的中英文文案。

## 12. 后端实施分阶段

### 阶段一：合同和配置层

- 固定字段命名、旧配置迁移策略和端点语义。
- 增加 `KeyGroup`、`Route.KeyID` 和最终目标解析结构。
- 完成配置校验、深复制、YAML 读写和迁移测试。
- 不改数据面路由前，先确保旧配置和新配置都能得到稳定快照。

### 阶段二：密钥组事务和管理 API

- 实现 key group CRUD、明文 `api_key` 持久化和独立配置事务。
- 增加密钥组级 `has_api_key`、明文读取/复制、重复检测、删除引用保护和 doctor 检查。
- 把 provider probe、模型发现和缓存改成 provider/key group 维度。
- 覆盖密钥写入成功、配置写入失败、重复密钥告警、读取/复制和迁移失败五类情况。

### 阶段三：路由、端点和数据面

- 扩展 `route.Resolve` 返回 key group。
- 实现三段模型 ID和旧两段 ID兼容解析。
- 在数据面统一解析最终 endpoint、adapter 和明文 `api_key`。
- 改造同协议和跨协议请求，使两种不同 key 可以在同一 provider 下同时工作。
- 扩展 trace、usage、模型目录和 Claude 别名。

### 阶段四：桌面端

- 先改类型和 API 客户端，再改 Providers 页面。
- 增加密钥组层级编辑和模型端点继承显示。
- 改 Routes 页面为供应商、密钥组、模型三级选择。
- 完成中英文、键盘操作、错误回滚和窄视口测试。

### 阶段五：真实验收

- 用 fake upstream 验证每个模型实际命中的 key 和 URL。
- 在 Windows 真实客户端上重新执行 point、选择模型、流式工具调用和 restore。

## 13. 测试矩阵

### 13.1 配置和迁移

- 一个 provider 多个 key group 可以解析。
- `api_key` 明文可以保存、读取、复制和重新加载，配置写入失败时原值不变。
- 同 provider 下重复 `api_key` 返回重复组告警，但不自动拒绝保存。
- key ID 重复、模型 ID重复、默认模型不存在、端点为空分别返回可定位错误。
- 模型端点覆盖组级端点。
- 模型端点为空时正确继承组级端点。
- 组级和模型级端点都为空时不允许新配置进入数据面。
- 旧 provider 结构迁移为 `default` key group，并将旧 `secret_ref` 指向的密钥复制为明文 `api_key`。
- 迁移重复执行不会创建 `default-2` 或复制密钥。
- YAML 原子写失败时原文件和当前内存快照不变。

### 13.2 路由和目录

- `agent/gpt/gpt-5.4` 命中 gpt 组和 `/v1/responses`。
- `agent/gpt/gpt-5.6` 命中 gpt 组和 `/v1/chat/completions`。
- `agent/claude/claude-sonnet-4-6` 命中 claude 组并继承组端点。
- 两个 key group 有同名模型时，三段 ID成功，两段 ID返回歧义。
- 旧两段 ID直接返回格式错误。
- 模型名含 `/` 时，provider、key ID和上游模型名保持可逆。
- 被禁用的 provider、key group 或模型不出现在目录，也不能被 route.Resolve 选中。

### 13.3 密钥和出站

- 同一 provider 的两个 key group 同时请求，fake upstream 收到不同认证值。
- 同一 key group 的两个模型请求到不同端点。
- 组级端点继承与模型级覆盖分别验证 URL 和报文协议。
- 新 key group 不依赖 secret store；旧配置迁移时 secret store 不可用必须阻止迁移并保持原文件不变。
- 日志和错误中没有认证头、密钥值或请求中的占位 key。
- 客户端取消时对应 key 的上游请求被取消。

### 13.4 管理 API 和桌面

- 单组 API key 更新失败不会改变其他 key group。
- API key 详情可以明文读取并复制，列表显示重复组告警。
- 配置写入失败能够恢复旧 key。
- 删除被 route 使用的 key group 返回 409。
- 模型发现使用指定 key group，不能复用 provider 级缓存。
- Providers 页面能编辑组级端点和模型级端点，并显示实际继承结果。
- Routes 页面保存并重新加载 `provider/key_id/model`。
- 1440×900 和 390×844 视口下表单、模型表和错误状态不重叠。

## 14. 验收示例

在最终实现中，使用一个能记录认证和路径的 fake upstream，至少验证以下三条：

| 请求模型 | 预期密钥组 | 预期端点 | 预期报文协议 |
|---|---|---|---|
| `agent/gpt/gpt-5.4` | `gpt` | `/v1/responses` | OpenAI Responses |
| `agent/gpt/gpt-5.6` | `gpt` | `/v1/chat/completions` | OpenAI Chat Completions |
| `agent/claude/claude-sonnet-4-6` | `claude` | 组级端点 | Anthropic Messages，前提是组级端点通过协议校验 |

三条请求必须满足：

- 到达同一个 provider 的共享 Base URL。
- 使用不同 key group 时，向上游注入不同密钥。
- 上游收到的模型名仍是原始 `gpt-5.4`、`gpt-5.6` 或 `claude-sonnet-4-6`，不能把 provider/key 前缀透传上游。
- route、日志、模型目录和管理 API 对同一请求显示相同的 provider、key group 和 model；管理 API 只在密钥组详情范围返回明文 key，日志不返回。

## 15. 已确认决策

1. 正式接受真实 API key 明文写入密钥组配置，并允许显式密钥组接口和桌面编辑器读取。
2. Anthropic 标准端点统一为 `/v1/messages`。
3. 密钥组标识统一为 `claude`；`calude` 视为拼写错误。
4. 同一 provider 下所有密钥组共享 provider-level `base_url`。
5. 新配置和管理 API 只接受 `key_groups`；本阶段不实现旧供应商兼容或迁移。
6. 供应商有多个密钥组时，探测前弹窗选择一个密钥组；后端要求明确 `key_id`。
7. `readyz` 只检查当前 routes 使用的密钥组，不检查未使用的可选组。
8. 删除被 route 引用的密钥组或模型返回 `409`，不自动改写路由。

## 16. 当前实施顺序

按“合同与配置 -> 路由和数据面 -> 管理 API -> 桌面端 -> 自动化测试与真实验收”推进。所有新写入只输出密钥组结构，旧 provider-level 字段由解析器和严格 JSON 解码直接拒绝。
