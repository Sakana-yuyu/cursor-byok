## v0.0.53

### 修复
- **修复 Kimi 思考内容流式截断**：reasoning 内容会先于正文处理，并只在 reasoning 真正结束或轮次结束时关闭思考状态，避免思考链路被正文 delta 提前打断。
- **修复 Kimi/兼容供应商工具 schema 报错**：统一清理 Chat Completions 与 Responses 请求中的工具参数 schema，修复 `required: null`、空 parameters、重复/非法 tool 等严格校验问题。
- **修复兼容网关不支持的缓存字段**：仅在明确支持或用户显式配置时发送 `prompt_cache_key`，减少 OpenRouter、Kimi 等网关因 OpenAI/Codex 字段返回 400。

### 新功能
- **新增 Gemini 原生模型适配**：支持 `gemini_native` 协议组、Gemini 请求构造、流式文本/思考/工具调用/usage 事件桥接。
- **新增 Gemini 前端配置入口**：模型编辑器、供应商模板、供应商详情批量拉取与探测流程均可选择 Gemini，并自动使用 Gemini 官方模型列表接口。
- **扩展 provider 兼容层**：补充 Kimi、OpenRouter、DeepSeek、Qwen/DashScope、GLM、MiniMax、StepFun、xAI/Grok、GitHub Copilot 等常见兼容规则。

### 优化
- **供应商余额查询更稳健**：自定义余额查询与命名供应商识别更好地协同，避免 relay baseURL 误匹配到内置供应商。
- **浏览器预览覆盖 Gemini**：browser-preview mock 增加 Gemini 示例模型和模型目录返回，便于无 Wails 环境下验证新入口。
- **模型配置校验更完整**：前后端配置白名单、协议归类、运行时归一化和模型目录拉取均覆盖 OpenAI、Anthropic、Gemini。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
