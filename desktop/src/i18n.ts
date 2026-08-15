export type Language = "zh-CN" | "en-US";

const messages = {
  "zh-CN": {
    overview: "总览", providers: "提供商", routes: "路由", clients: "客户端", logs: "日志", usage: "用量", settings: "设置",
    connected: "网关已连接", refresh: "刷新", addProvider: "添加提供商", save: "保存", cancel: "取消", edit: "编辑", remove: "删除", probe: "探测",
    provider: "提供商", model: "模型", adapter: "适配器", baseURL: "基础地址", apiKey: "API 密钥", name: "名称", identifier: "标识符",
    imageInput: "图片输入", reasoning: "推理", actions: "操作", status: "状态", route: "当前路由", apply: "应用", point: "指向网关", restore: "还原",
    target: "配置文件", logging: "正文日志", enabled: "已启用", disabled: "已关闭", requests: "请求", tokens: "令牌", incomplete: "用量不完整",
    language: "语言", theme: "主题", light: "明亮", dark: "暗色", system: "跟随系统", port: "监听端口", logDir: "日志目录", saveSettings: "保存设置", autostart: "登录启动", currentUserSession: "当前用户会话",
    riskTitle: "正文日志风险确认", riskBody: "正文日志可能包含提示词、源代码、工具参数、工具结果和图片原文。日志不会自动脱敏，请仅在可信设备上启用并自行管理磁盘空间。",
    accept: "我已了解并继续", requestLog: "请求日志", noLogs: "尚无请求日志", noProviders: "尚无提供商", loading: "正在读取网关状态", retry: "重试",
    success: "成功", failed: "失败", cancelled: "已取消", interrupted: "已中断", details: "详情", close: "关闭", systemNotifications: "系统通知", runtime: "运行信息", clientRoutes: "客户端路由",
    confirmPoint: "确认修改该客户端配置并创建备份？", confirmRestore: "确认从最新备份还原客户端配置？", confirmDelete: "确认删除此提供商？", validation: "请修正标记的字段。",
    routesDescription: "路由变更从下一次请求开始生效。", clientsDescription: "使用事务更新客户端配置，并按原始字节精确还原。", configured: "个已配置",
    localPreference: "桌面端本地偏好", loopbackOnly: "仅限本机回环地址", relativeDataRoot: "相对于数据根目录", keepKey: "留空以保留当前密钥",
    pointed: "已指向", notPointed: "未指向", drifted: "配置已漂移", clientNotInstalled: "客户端未安装", unknown: "未知", keyReady: "密钥就绪", keyless: "无密钥", api: "接口", ok: "正常",
  },
  "en-US": {
    overview: "Overview", providers: "Providers", routes: "Routes", clients: "Clients", logs: "Logs", usage: "Usage", settings: "Settings",
    connected: "Gateway connected", refresh: "Refresh", addProvider: "Add provider", save: "Save", cancel: "Cancel", edit: "Edit", remove: "Delete", probe: "Probe",
    provider: "Provider", model: "Model", adapter: "Adapter", baseURL: "Base URL", apiKey: "API key", name: "Name", identifier: "Identifier",
    imageInput: "Image input", reasoning: "Reasoning", actions: "Actions", status: "Status", route: "Current route", apply: "Apply", point: "Point to gateway", restore: "Restore",
    target: "Config file", logging: "Body logging", enabled: "Enabled", disabled: "Disabled", requests: "Requests", tokens: "Tokens", incomplete: "Usage incomplete",
    language: "Language", theme: "Theme", light: "Light", dark: "Dark", system: "System", port: "Listen port", logDir: "Log directory", saveSettings: "Save settings", autostart: "Start at login", currentUserSession: "Current user session",
    riskTitle: "Body logging risk", riskBody: "Body logs may contain prompts, source code, tool arguments, tool results, and raw images. Logs are not automatically redacted. Enable them only on a trusted device and manage disk usage yourself.",
    accept: "I understand and want to continue", requestLog: "Request log", noLogs: "No request logs yet", noProviders: "No providers yet", loading: "Reading gateway status", retry: "Retry",
    success: "Success", failed: "Failed", cancelled: "Cancelled", interrupted: "Interrupted", details: "Details", close: "Close", systemNotifications: "System notifications", runtime: "Runtime", clientRoutes: "Client routes",
    confirmPoint: "Modify this client configuration and create a backup?", confirmRestore: "Restore this client configuration from the latest backup?", confirmDelete: "Delete this provider?", validation: "Correct the marked fields.",
    routesDescription: "Route changes apply to the next request.", clientsDescription: "Transactional client updates with exact-byte restore.", configured: " configured",
    localPreference: "Local desktop preference", loopbackOnly: "Loopback only", relativeDataRoot: "Relative to data root", keepKey: "Leave empty to keep current key",
    pointed: "Pointed", notPointed: "Not pointed", drifted: "Configuration drifted", clientNotInstalled: "Client not installed", unknown: "Unknown", keyReady: "Key ready", keyless: "Keyless", api: "API", ok: "OK",
  },
} as const;

export type MessageKey = keyof typeof messages["zh-CN"];
export function translator(language: Language) { return (key: MessageKey) => messages[language][key]; }
