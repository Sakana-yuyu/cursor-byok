# ccSwitch 供应商目录完全对齐设计

## 背景

模型编辑页的供应商下拉需要与 `farion1231/cc-switch` 当前 `main` 的普通供应商目录对齐。当前仓库已经登记了大部分条目，包括 `DeepSeek`，但目录顺序、展示名和上游名称存在差异，导致用户难以核对完整性，也容易误以为缺少供应商。

## 目标

1. 普通模型供应商下拉完整展示 ccSwitch `main` 的非 Claude Desktop 目录中的 69 个供应商，并在四个基础模板之后按显示名首字母排序。
2. 保留 `custom`、`openai`、`anthropic`、`gemini` 四个基础模板。
3. `DeepSeek` 使用稳定的 `supplierID: "deepseek"` 独立登记。
4. 供应商显示名、Base URL、协议、推荐模型和模型目录候选与上游目录对齐；显示顺序采用稳定的首字母排序。
5. 现有配置的 supplierID 和兼容模板继续可用，不批量迁移用户配置。
6. Claude Desktop 专用 OAuth、角色路由和工具专属配置不进入普通模型下拉。

## 非目标

- 不在运行时从网络读取 ccSwitch 目录。
- 不把 Claude Desktop 的 OAuth/角色配置转换成普通模型适配器。
- 不新增测试文件；仓库约束禁止新增测试。
- 不修改与供应商目录无关的本地代理、委派和历史逻辑。

## 权威来源与范围

权威来源为：

- 仓库：`https://github.com/farion1231/cc-switch`
- 分支：`main`
- 目录：`src/config/claudeDesktopProviderPresets.ts`
- 范围：排除 `Claude Desktop Official`，保留其余 69 个普通供应商。

普通目录的上游登记顺序为（实现时会按显示名首字母排序）：

1. Kimi
2. Kimi For Coding
3. PackyCode
4. ZetaAPI
5. APINebula
6. AICodeMirror
7. PatewayAI
8. FennoAI
9. RunAPI
10. Shengsuanyun
11. AIGoCode
12. AICoding
13. SubRouter
14. APIKEY.FUN
15. ClaudeAPI
16. Code0
17. TeamoRouter
18. ClaudeCN
19. 火山Agent Plan
20. BytePlus
21. DouBaoSeed
22. SiliconFlow
23. SiliconFlow en
24. NekoCode
25. A6API
26. AtlasCloud
27. Compshare
28. Compshare Coding Plan
29. CCSub
30. SSSAiCode
31. Micu
32. RightCode
33. ETok.ai
34. Cubence
35. CrazyRouter
36. DMXAPI
37. Qiniu
38. SudoCode.chat
39. SudoCode.us
40. Amux
41. Gemini Native
42. GitHub Copilot
43. Codex
44. xAI (Grok)
45. DeepSeek
46. OpenCode Go
47. Zhipu GLM
48. Zhipu GLM en
49. Baidu Qianfan Coding Plan
50. Bailian
51. Bailian For Coding
52. StepFun
53. StepFun en
54. ModelScope
55. Longcat
56. MiniMax
57. MiniMax en
58. BaiLing
59. AiHubMix
60. CherryIN
61. RelaxyCode
62. E-FlowCode
63. OpenRouter
64. TheRouter
65. Novita AI
66. Nvidia
67. PIPELLM
68. Xiaomi MiMo
69. Xiaomi MiMo Token Plan (China)

以上顺序只用于核对 ccSwitch 来源。实际下拉展示保留四个基础模板在前，其后的 69 个普通供应商统一按显示名首字母排序。

当前实现已将 `Shengsuanyun`、`Compshare`、`Qiniu`、`xAI (Grok)` 等显示名恢复为上游名称；火山供应商保留仓库已有的 `火山Agent Plan` 本地化显示名以复用现有翻译，同时继续沿用已有稳定 ID，例如 `shengsuanyun`、`ucloud` 和 `ucloud_coding`。

## 数据模型

`frontend/src/utils/supplierCatalog.js` 是唯一供应商注册表。每条模板至少包含：

- `id` / `supplierID`：稳定内部 ID；不依赖展示名。
- `label`：用户可见的上游对齐名称。
- `type`：`openai`、`anthropic` 或 `gemini`。
- `baseURL`：模型请求地址，不得使用官网地址代替。
- `models`：推荐模型初始值。
- `modelCatalog`：目录能力、完整候选 URL 和是否追加通用候选。
- `usage`：余额/额度策略及 provider 标识。

`DeepSeek` 的正式模板使用 `deepseek` ID、OpenAI 兼容协议、官方 Base URL、模型目录候选和已有固定余额 provider。没有已核验用量接口的供应商声明为 `none`，不进入猜测性余额请求。

兼容模板继续存在，但仅在保存配置使用旧兼容 ID 时追加到选择项，不在正常目录中重复展示：

- `moonshot`
- `zhipu`
- `zhipu_team`
- `zenmux`
- `volcengine`

## 数据流

```text
ccSwitch main 普通供应商矩阵
        │
        ▼
frontend/src/utils/supplierCatalog.js
        │
        ├─ ModelEditor：下拉、协议、Base URL、推荐模型
        ├─ ModelCatalog：模型目录候选 URL
        ├─ SupplierDetail：能力和用量来源
        └─ bridge/backend：supplierID、目录策略、用量策略
```

供应商选择只填充默认值，不覆盖用户已经填写的 API Key、Base URL、模型 ID、请求头和显式余额配置。

## 兼容与错误处理

- 未知 supplierID 继续回退到自定义模板，避免旧配置崩溃。
- 已有 supplierID 不迁移、不重命名；只修正展示名和目录顺序。
- 用户显式填写的模型目录 URL 优先于预设 URL。
- 供应商官网链接只用于帮助跳转，不能参与 API 请求。
- `none` / `custom_only` 只返回“暂无自动查询”状态，不发送猜测路径。

## 验收

不新增测试文件，执行以下验证：

1. 静态检查普通目录数量为 69，基础模板加目录后 ID 唯一。
2. 检查顺序、`deepseek`、`xAI (Grok)`、`Compshare`、`Xiaomi MiMo Token Plan (China)` 等关键条目存在。
3. 检查 Claude Desktop 专用项不出现在普通下拉。
4. 运行前端 i18n 扫描和生产构建。
5. 手动打开模型编辑页下拉，确认完整列表可滚动选择；验证 DeepSeek 和尾部条目可见。
6. 验证自定义供应商、旧兼容 supplierID 和已保存模型配置仍可正常编辑保存。

## 文件边界

预计主要修改：

- `frontend/src/utils/supplierCatalog.js`

必要时才修改：

- `frontend/src/views/ModelEditor.vue`
- `frontend/src/components/ui/Select.vue`
- 与模型目录/供应商元数据直接相关的前端文件

不修改已安装的 Cursor 客户端、无关工作区改动和 Claude Desktop 专用路由逻辑。