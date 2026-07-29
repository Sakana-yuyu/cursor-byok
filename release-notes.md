## v0.0.59

### 修复
- **中转站 max_tokens 400 报错（Neurons/quota abuse）**：catalog 的 `max_tokens` 硬上限此前用客户端内部哈希 `modelID`（如 `4fd90578ea9510b1`）查询，catalog 无法匹配返回 0，导致上限失效、发出默认 65536，被中转站以「max_tokens (65536) exceeds limit (4096)」拒绝。现改为优先用显示名 `modelName`（如 `kimi-k2.7-code`）查询 catalog，`modelID` 仅兜底，上限正确生效（如 kimi-k2.7-code 砍到 4096）。受影响模型：kimi-k2.7-code、glm-4.7-flash 等经 daoxe/Neurons 中转站且目录上限 < 65536 的模型。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
