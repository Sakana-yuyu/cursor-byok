# 供应商预设目录与自动用量匹配设计

## 背景

模型配置页目前只有少量供应商模板，模型目录请求主要把 Base URL 统一拼接为 /models，余额查询则由少量具名 provider、Token Plan、New API、通用模板和自定义查询组成。图一中的供应商数量远大于现有模板，且各厂商的 Anthropic、OpenAI、Gemini、Coding Plan 和余额接口路径并不统一。

本功能将图一中的所有品牌登记为数据驱动的供应商预设，并把 cc-switch 已验证的模型目录与用量查询规则接入当前模型配置链路。Claude Desktop 保留现有专用逻辑，不进入普通模型配置预设和余额自动匹配链。

## 目标

1. 图一中的每个品牌均有独立、稳定的 supplierID 和可编辑预设。
2. 预设提供正确的官网地址、API Base URL、协议类型、模型目录策略和推荐模型。
3. 模型目录拉取支持 OpenAI 兼容 /v1/models、Anthropic 兼容 relay、Gemini 原生目录和供应商明确给出的自定义目录 URL。
4. 用量查询优先按 supplierID 和 host 自动匹配已确认的固定规则，尽量不要求用户手动选择模板。
5. 没有公开或已验证用量接口的供应商仍可正常配置模型，但查询结果明确显示“暂无自动查询”，不发送猜测性的余额请求。
6. 保留自定义供应商模板，允许用户填写模型目录 URL、请求头和余额查询 URL/字段路径。
7. 兼容已有配置字段和旧版 supplierID，不破坏现有模型转发配置。

## 非目标

- 不把 Claude Desktop 的角色模型映射、OAuth、订阅额度或本地路由改造成普通模型适配器。
- 不抓取需要登录的供应商后台页面，也不通过 HTML 页面内容猜测余额。
- 不为没有公开接口的厂商编造余额 URL、字段或签名算法。
- 不在本阶段自动执行 JavaScript 用量脚本；当前后端继续使用结构化 URL、请求头和 dot-path 字段读取。
- 不修改已安装的 Cursor 或 cc-switch bundle。

## 覆盖矩阵

### 普通模型配置预设

预设目录覆盖图一中的以下条目；自定义配置对应 custom，Claude Desktop 保留专用入口并排除在普通目录之外。

- Kimi、Kimi For Coding、PackyCode、ZetaAPI、APINebula、AICodeMirror、PatewayAI、FennoAI、RunAPI、Unity2.ai、胜算云、AIGoCode
- AICoding、SubRouter、APIKEY.FUN、ClaudeAPI、Code0、TeamoRouter、ClaudeCN、火山Agentplan、BytePlus、DouBaoSeed、SiliconFlow、SiliconFlow en
- NekoCode、A6API、AtlasCloud、优云智算、优云智算 Coding Plan、CCSub、SSSAiCode、Micu、RightCode、ETok.ai、Cubence、CrazyRouter、DMXAPI
- 七牛云、SudoCode.chat、SudoCode.us、AiHubMix、Amux、Baidu Qianfan Coding Plan、Bailian、Bailian For Coding、BaiLing、CherryIN
- Codex、DeepSeek、E-FlowCode、Gemini Native、GitHub Copilot、Longcat、MiniMax、MiniMax en、ModelScope、Novita AI、Nvidia
- OpenCode Go、OpenRouter、PIPELLM、RelaxyCode、StepFun、StepFun en、TheRouter、xAI (Grok)、Xiaomi MiMo、Xiaomi MiMo Token Plan (China)、Zhipu GLM、Zhipu GLM en

其中“优云智算”按 cc-switch 的 Compshare/UCloud 预设登记，“七牛云”按 Qiniu 预设登记，“Xiaomi MiMo Token Plan (China)”保留独立 supplierID，避免与普通 Xiaomi MiMo 的用量规则混用。

### 每条预设的验证状态

每条预设包含三个独立状态，不用单一的“支持/不支持”混淆能力：

- modelCatalogStatus：openai_models、gemini_models、custom_url、manual_only。
- usageStatus：fixed、token_plan、newapi、general、custom_only、none。
- verification：cc_switch、official_docs、mixed、unverified。

manual_only 或 none 仍然是完整预设，只表示该能力不能自动请求。UI 显示状态和来源链接，但不把来源链接当作 API URL。

## 数据模型

在 frontend/src/utils/supplierCatalog.js 中将 SUPPLIER_TEMPLATES 扩展为数据目录。每条模板包含 id、label、websiteURL、apiKeyURL、type、baseURL、requestGroup、modelCatalog、usage、models 和 allowCustomURL。modelCatalog 保存 status、urls 和 appendCandidates；usage 保存 status、provider、source、queryURL、field 和 currency。

字段约定：

- id 是持久化和后端自动匹配的稳定标识，不能使用展示名。
- baseURL 是模型请求使用的地址，不等同于官网 websiteURL 或用量 queryURL。
- modelCatalog.urls 是可验证的目录 URL；为空时由后端按协议生成候选，不把官网地址直接拼成 API。
- usage.provider 是后端具名查询路由标识；none 和 custom_only 不设置该字段。
- models 只放推荐/备用模型，成功拉取后仍以远端目录为准。
- 所有可编辑预设都允许用户覆盖 Base URL、模型目录地址和自定义请求头。

## 模型目录请求

ModelEditor 选择供应商模板后填充 type、baseURL、协议分组、supplierID 和预设模型；点击拉取模型时传递 supplierID、modelCatalogURL 和候选策略。ModelCatalog 继续使用现有批量选择、探测和保存流程。

扩展 ModelCatalogRequest，增加 SupplierID、ModelCatalogURL 和 ModelCatalogURLsJSON 字段。

候选优先级：

1. 用户显式填写的完整 ModelCatalogURL。
2. 预设登记的 modelCatalog.urls。
3. 已经以 /models 结尾的 Base URL 原样使用。
4. OpenAI 兼容 Base URL 追加 /v1/models；已带 /vN 时追加 /models。
5. 对 cc-switch 已验证的兼容子路径，先尝试原路径，再剥离 /anthropic、/coding、/compatible、/step_plan 等后缀后追加 /v1/models 或 /models。
6. Gemini 原生使用 /v1beta/models 或预设提供的完整 URL，不使用 OpenAI Authorization Bearer 头。

候选地址去重并按顺序尝试；只有 GET 成功且 JSON 中能解析到模型 ID 才视为成功。所有候选失败时返回脱敏错误摘要，不能把一次失败缓存成空模型目录。

协议鉴权：OpenAI/兼容 relay 默认 Authorization Bearer；Anthropic 默认 x-api-key 和 anthropic-version；Gemini 默认 x-goog-api-key。自定义请求头覆盖默认同名请求头。

## 用量查询自动匹配

QueryProviderBalance 按以下顺序解析：

1. 当前请求显式提供的 BalanceProfile、查询 URL、字段和凭据。
2. 持久化模型适配器中同一 supplierID、type、baseURL、apiKey 的配置。
3. 预设的稳定 supplierID 规则。
4. Base URL host 规则，用于兼容用户把预设改成等价路径的情况。
5. newapi、token_plan、general 仅在预设状态或用户显式选择允许时执行。
6. none、custom_only 直接返回结构化“不支持自动查询”，不进入猜测性端点探测。

已有旧配置在 supplierID 为空或为 custom 时保留当前通用试探链兼容行为；新建的具名供应商不继承该猜测链。

已验证的固定接口：

- DeepSeek：GET https://api.deepseek.com/user/balance，读取 balance_infos[].total_balance。
- Moonshot/Kimi 普通 API：GET https://api.moonshot.cn/v1/users/me/balance，读取余额字段，单位 USD。
- StepFun：GET https://api.stepfun.com/v1/accounts 或国际域名对应端点，读取 balance。
- SiliconFlow：GET https://api.siliconflow.cn/v1/user/info 或 .com 对应端点，读取 data.totalBalance。
- OpenRouter：GET https://openrouter.ai/api/v1/credits，remaining 等于 total_credits 减 total_usage。
- Novita：GET https://api.novita.ai/v3/user/balance，读取 availableBalance 并按上游单位换算。
- Kimi For Coding、Zhipu、MiniMax、ZenMux、火山 Coding Plan：使用 token_plan 专用解析，不误当普通余额。
- New API：用户提供访问令牌与用户 ID 时请求站点根 /api/user/self，读取 data.quota 和 data.used_quota。
- 通用余额模板：仅对明确声明 usage.status 为 general 的预设或用户显式选择执行 GET <base>/user/balance。

固定接口的 URL、Header、字段和单位集中在后端 provider balance 模块，供应商目录只负责声明路由 ID 和能力状态，避免前后端各自维护一套 host 判断。

当 usage.status 为 none 或 custom_only：供应商卡片显示“暂无自动查询”，不请求猜测路径；编辑器仍显示自定义查询模板，支持 apiKey、baseUrl、accessToken、userId 占位符和 dot-path。成功后结果来源标记为 configured。

成功结果继续使用现有 TTL 缓存；缓存键纳入供应商 ID、查询策略、查询 URL、字段、访问令牌和请求头摘要。传输错误标记 Transient 并保留上一次成功值；确定性失败清除当前失败值；不缓存“不支持自动查询”和候选失败结果。

## UI 变化

- 模型编辑页供应商下拉展示全部品牌和自定义供应商；Claude Desktop 不出现在普通模型编辑器下拉中。
- 选择预设后显示协议、Base URL、模型目录状态和用量匹配状态。
- 模型目录 URL 支持使用预设候选、自定义完整 URL、自动候选三种模式。
- 用量查询区域显示自动匹配来源；仅需凭据的模板才显示额外令牌或用户 ID 输入。
- 供应商列表/详情的余额旁显示固定来源、Token Plan、通用模板、自定义或暂无自动查询。
- 预设的官网/文档链接只作为帮助跳转，不参与请求。

## 兼容与迁移

- 现有 supplierID 保持原值；新增预设只补充目录，不批量改写用户已有 Base URL 或模型 ID。
- balanceProfile 为空或 auto 的旧配置维持当前行为；新建具名预设进入严格的能力状态匹配。
- 旧的 balanceQueryHeaders、balanceQueryHeadersJSON、balanceAccessToken、balanceUserID 字段继续兼容。
- 对相同 host 的多个预设以 supplierID 优先，host 只作兜底。

## 验证策略

本仓库禁止新增测试文件，因此沿用现有测试、构建、静态检查和人工流程验证：前端构建触发 i18n 扫描；Go 构建、go vet、既有相关测试和 git diff --check；静态检查供应商 ID 唯一、Base URL 合法、官网 URL 与 API URL 不混用、每条预设都有状态字段；人工验证全部品牌选择、自定义模板、模型拉取、余额成功、余额不支持和自定义查询。不得使用真实 API Key 做自动化验证，优先使用本地 HTTP fixture 或浏览器预览 mock。

## 文件边界

预计修改：frontend/src/utils/supplierCatalog.js、frontend/src/views/ModelEditor.vue、frontend/src/views/ModelCatalog.vue、frontend/src/views/ModelConfig.vue、frontend/src/views/SupplierDetail.vue、frontend/src/state/appState.js、frontend/src/services/clientApi.js、frontend/src/services/browserBindings.js、internal/client/model_catalog.go、internal/client/provider_balance.go、internal/client/provider_balance_named.go、internal/client/provider_balance_configured.go，以及必要的配置转换文件。

不修改 Claude Desktop 专用预设和本地路由逻辑，也不修改与供应商配置无关的代理、用量统计汇总和 Cursor 启动流程。
