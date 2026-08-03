// Data-driven supplier presets. supplierID stays stable across saved adapters and backend routing.
const MODEL_CATALOG_STATUSES = new Set(["openai_models", "gemini_models", "custom_url", "manual_only"]);
const USAGE_STATUSES = new Set(["fixed", "token_plan", "newapi", "general", "custom_only", "none"]);
const SUPPLIER_ICON_LIGHT_IDS = new Set(["custom", "openai", "codex", "github_copilot"]);
const SUPPLIER_ICON_FILES = Object.freeze({
  custom: "openai.svg",
  openai: "openai.svg",
  anthropic: "anthropic.svg",
  gemini: "gemini.svg",
  kimi: "kimi.svg",
  kimi_coding: "kimi.svg",
  packycode: "packycode.svg",
  zetaapi: "zetaapi-icon.png",
  apinebula: "apinebula_icon.png",
  aicodemirror: "aicodemirror.svg",
  patewayai: "pateway.jpg",
  fennoai: "fenno-icon.webp",
  runapi: "runapi.jpg",
  shengsuanyun: "shengsuanyun.svg",
  aigocode: "aigocode.svg",
  aicoding: "aicoding.svg",
  subrouter: "subrouter.svg",
  apikeyfun: "apikeyfun.png",
  claudeapi: "ClaudeApi.png",
  code0: "code0.png",
  teamorouter: "TeamoRouter-icon-dark.png",
  claudecn: "claudecn.png",
  volcengine_agent: "huoshan.png",
  byteplus: "byteplus.png",
  doubaoseed: "doubao.svg",
  siliconflow: "siliconflow.svg",
  siliconflow_en: "siliconflow.svg",
  nekocode: "nekocode-icon.png",
  a6api: "a6-icon.png",
  atlascloud: "atlascloud_icon.png",
  ucloud: "ucloud.svg",
  ucloud_coding: "ucloud.svg",
  ccsub: "ccsub.svg",
  sssaicode: "sssaicode.svg",
  micu: "micu.svg",
  rightcode: "rc.svg",
  etok: "etok.png",
  cubence: "cubence.svg",
  crazyrouter: "crazyrouter.svg",
  dmxapi: "qiniu.png",
  qiniu: "qiniu.png",
  sudocode_chat: "sudocode.png",
  sudocode_us: "sudocode-us.png",
  aihubmix: "aihubmix-color.svg",
  amux: "amuxapi-icon.svg",
  qianfan_coding: "baidu.svg",
  bailian: "bailian.svg",
  bailian_coding: "bailian.svg",
  bailing: "aihubmix-color.svg",
  cherryin: "cherryin.png",
  codex: "openai.svg",
  deepseek: "deepseek.svg",
  gemini_native: "gemini.svg",
  github_copilot: "github.svg",
  longcat: "longcat-color.svg",
  minimax: "minimax.svg",
  minimax_en: "minimax.svg",
  modelscope: "modelscope-color.svg",
  novita: "novita.svg",
  nvidia: "nvidia.svg",
  opencode_go: "opencode-logo-light.svg",
  openrouter: "openrouter.svg",
  pipellm: "pipellm.png",
  relaxycode: "relaxcode.png",
  eflowcode: "eflowcode.png",
  stepfun: "stepfun.svg",
  stepfun_en: "stepfun.svg",
  therouter: "novita.svg",
  xai: "xai.svg",
  xiaomi_mimo: "xiaomimimo.svg",
  xiaomi_mimo_token_plan_cn: "xiaomimimo.svg",
  zhipu_glm: "zhipu.svg",
  zhipu_glm_en: "zhipu.svg",
  moonshot: "kimi.svg",
  zhipu: "zhipu.svg",
  zhipu_team: "zhipu.svg",
  zenmux: "newapi.svg",
  volcengine: "huoshan.png",
  unity2: "unity2.png",
});

function supplierIconURL(id) {
  const fileName = SUPPLIER_ICON_FILES[id];
  return fileName ? `/supplier-icons/${fileName}` : "";
}

function createTemplate(definition) {
  const id = String(definition.id || "custom").trim().toLowerCase();
  const type = definition.type || "anthropic";
  const modelCatalogURLs = Array.isArray(definition.modelCatalogURLs)
    ? definition.modelCatalogURLs.filter(Boolean)
    : [];
  const catalogStatus = MODEL_CATALOG_STATUSES.has(definition.modelCatalogStatus)
    ? definition.modelCatalogStatus
    : type === "gemini"
      ? "gemini_models"
      : type === "openai" || modelCatalogURLs.length > 0
        ? "openai_models"
        : "manual_only";
  const usageStatus = USAGE_STATUSES.has(definition.usageStatus) ? definition.usageStatus : "none";
  const baseURL = String(definition.baseURL || "").trim();
  return {
    id,
    supplierID: id,
    label: definition.label || id,
    iconURL: definition.iconURL || supplierIconURL(id),
    iconLight: definition.iconLight ?? SUPPLIER_ICON_LIGHT_IDS.has(id),
    websiteURL: definition.websiteURL || "",
    apiKeyURL: definition.apiKeyURL || definition.websiteURL || "",
    type,
    baseURL,
    endpoint: definition.endpoint || (type === "openai" ? "/v1/chat/completions" : ""),
    requestGroup: definition.requestGroup || (type === "openai" ? "chat_completions" : type === "gemini" ? "gemini_native" : ""),
    models: Array.isArray(definition.models) ? definition.models : [],
    presets: Array.isArray(definition.presets) ? definition.presets : [],
    allowCustomURL: definition.allowCustomURL !== false,
    modelCatalog: {
      status: catalogStatus,
      urls: modelCatalogURLs,
      appendCandidates: definition.appendCandidates !== false,
      source: definition.source || "cc_switch",
    },
    usage: {
      status: usageStatus,
      provider: definition.usageProvider || "",
      source: definition.usageSource || (usageStatus === "none" ? "未核验公开用量接口" : "cc_switch / 官方接口"),
    },
    verification: definition.verification || (definition.source === "official_docs" ? "official_docs" : "cc_switch"),
  };
}

const coreTemplates = [
  createTemplate({
    id: "custom",
    label: "自定义供应商",
    type: "openai",
    modelCatalogStatus: "custom_url",
    usageStatus: "custom_only",
    usageSource: "自定义查询模板",
    source: "manual",
  }),
  createTemplate({
    id: "openai",
    label: "OpenAI",
    type: "openai",
    websiteURL: "https://platform.openai.com",
    apiKeyURL: "https://platform.openai.com/api-keys",
    baseURL: "https://api.openai.com/v1",
    endpoint: "/v1/responses",
    requestGroup: "responses",
    models: ["gpt-5", "gpt-4.1"],
    usageStatus: "none",
    usageSource: "官方模型目录可用；公开用量接口未接入",
    source: "official_docs",
    verification: "official_docs",
  }),
  createTemplate({
    id: "anthropic",
    label: "Anthropic",
    type: "anthropic",
    websiteURL: "https://www.anthropic.com",
    apiKeyURL: "https://console.anthropic.com/settings/keys",
    baseURL: "https://api.anthropic.com",
    models: ["claude-sonnet-4-5", "claude-haiku-4-5"],
    modelCatalogStatus: "custom_url",
    modelCatalogURLs: ["https://api.anthropic.com/v1/models"],
    usageStatus: "none",
    usageSource: "官方模型目录未提供统一公开余额接口",
    source: "official_docs",
    verification: "official_docs",
  }),
  createTemplate({
    id: "gemini",
    label: "Gemini",
    type: "gemini",
    websiteURL: "https://ai.google.dev/gemini-api",
    apiKeyURL: "https://aistudio.google.com/app/apikey",
    baseURL: "https://generativelanguage.googleapis.com/v1beta",
    models: ["gemini-2.5-pro", "gemini-2.5-flash"],
    usageStatus: "none",
    usageSource: "官方模型目录可用；公开用量接口未接入",
    source: "official_docs",
    verification: "official_docs",
  }),
];

// The following rows mirror cc-switch's provider preset catalog. Unknown usage APIs intentionally stay none.
const catalogRows = [
  ["kimi", "Kimi", "anthropic", "https://platform.kimi.com?aff=cc-switch", "https://platform.kimi.com?aff=cc-switch", "https://api.moonshot.cn/anthropic", ["kimi-k2.7-code"], { usageStatus: "fixed", usageProvider: "moonshot", modelCatalogURLs: ["https://api.moonshot.cn/anthropic/v1/models"] }],
  ["kimi_coding", "Kimi For Coding", "anthropic", "https://www.kimi.com/code/?aff=cc-switch", "https://www.kimi.com/code/?aff=cc-switch", "https://api.kimi.com/coding", [], { usageStatus: "token_plan", usageProvider: "kimi", modelCatalogURLs: ["https://api.kimi.com/coding/v1/models"] }],
  ["packycode", "PackyCode", "anthropic", "https://www.packyapi.ai", "https://www.packyapi.ai/register?aff=cc-switch", "https://www.packyapi.ai/v1", [], { modelCatalogURLs: ["https://www.packyapi.ai/v1/models", "https://cf.api.fan/v1/models", "https://slb-v1.api.fan/v1/models"] }],
  ["zetaapi", "ZetaAPI", "anthropic", "https://zetaapi.ai", "https://zetaapi.ai/go/u117", "https://api.zetaapi.ai/v1", [], {}],
  ["apinebula", "APINebula", "anthropic", "https://apinebula.ai", "https://apinebula.ai/VjM74M", "https://apinebula.ai/v1", [], {}],
  ["aicodemirror", "AICodeMirror", "anthropic", "https://www.aicodemirror.ai", "https://www.aicodemirror.ai/register?invitecode=9915W3", "https://api.aicodemirror.ai/api/claudecode", [], {}],
  ["patewayai", "PatewayAI", "anthropic", "https://pateway.ai", "https://pateway.ai/?ch=etzpm8&aff=WB6M6F67#/", "https://api.pateway.ai/v1", [], {}],
  ["fennoai", "FennoAI", "anthropic", "https://api.fenno.ai", "https://api.fenno.ai/register?redirect=/purchase?tab=subscription%26group=16&aff=P9MR3D3PLCNL", "https://api.fenno.ai/v1", [], {}],
  ["runapi", "RunAPI", "anthropic", "https://runapi.co", "https://runapi.co/register?aff=iOKB", "https://runapi.co/v1", [], {}],
  ["shengsuanyun", "Shengsuanyun", "anthropic", "https://www.shengsuanyun.com/?from=CH_4HHXMRYF", "https://www.shengsuanyun.com/?from=CH_4HHXMRYF", "https://router.shengsuanyun.com/api", ["anthropic/claude-sonnet-5", "anthropic/claude-opus-5", "anthropic/claude-haiku-4.5"], {}],
  ["aigocode", "AIGoCode", "anthropic", "https://aigocode.app", "https://aigocode.app/invite/CC-SWITCH", "https://api.aigocode.app/v1", [], {}],
  ["aicoding", "AICoding", "anthropic", "https://aicoding.inc", "https://aicoding.inc/i/CCSWITCH", "https://api.aicoding.inc/v1", [], {}],
  ["subrouter", "SubRouter", "anthropic", "https://subrouter.ai", "https://subrouter.ai/register?aff=l3ri", "https://subrouter.ai/v1", [], {}],
  ["apikeyfun", "APIKEY.FUN", "anthropic", "https://apikey.fun", "https://apikey.fun/register?aff=CCSwitch", "https://api.apikey.fun/v1", [], { modelCatalogURLs: ["https://api.apikey.fun/v1/models", "https://slb.apikey.fun/v1/models"] }],
  ["claudeapi", "ClaudeAPI", "anthropic", "https://www.apito.ai", "https://console.apito.ai/agent/register/pQBql2buaqiX3dDS", "https://gw.apito.ai/v1", [], {}],
  ["code0", "Code0", "anthropic", "https://code0.ai", "https://code0.ai/agent/register/B2XHxGjGmRvqgznY", "https://code0.ai/v1", [], {}],
  ["teamorouter", "TeamoRouter", "anthropic", "https://teamorouter.com", "https://teamorouter.com/?utm_source=cc_switch&utm_medium=referral&utm_campaign=ai_directory", "https://api.teamorouter.com/v1", [], {}],
  ["claudecn", "ClaudeCN", "anthropic", "https://claudecn.top", "https://claudecn.ai/register?aff=HEL9", "https://claudecn.top/v1", [], {}],
  ["volcengine_agent", "火山Agent Plan", "anthropic", "https://www.volcengine.com/activity/codingplan", "https://www.volcengine.com/activity/codingplan", "https://ark.cn-beijing.volces.com/api/coding", ["ark-code-latest"], { usageStatus: "token_plan", usageProvider: "volcengine", modelCatalogURLs: ["https://ark.cn-beijing.volces.com/api/coding/v1/models"] }],
  ["byteplus", "BytePlus", "anthropic", "https://www.byteplus.com/en/product/modelark", "https://www.byteplus.com/en/product/modelark", "https://ark.ap-southeast.bytepluses.com/api/coding", ["ark-code-latest"], {}],
  ["doubaoseed", "DouBaoSeed", "anthropic", "https://console.volcengine.com/ark", "https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey", "https://ark.cn-beijing.volces.com/api/compatible", ["doubao-seed-2-1-pro-260628"], {}],
  ["siliconflow", "SiliconFlow", "openai", "https://siliconflow.cn", "https://cloud.siliconflow.cn/i/YflgU2Ve", "https://api.siliconflow.cn/v1", ["deepseek-ai/DeepSeek-V3", "Qwen/Qwen3-Coder-480B-A35B-Instruct"], { usageStatus: "fixed", usageProvider: "siliconflow", modelCatalogURLs: ["https://api.siliconflow.cn/v1/models"] }],
  ["siliconflow_en", "SiliconFlow en", "openai", "https://siliconflow.com", "https://cloud.siliconflow.cn/i/YflgU2Ve", "https://api.siliconflow.com/v1", ["MiniMaxAI/MiniMax-M2.7"], { usageStatus: "fixed", usageProvider: "siliconflow_en", modelCatalogURLs: ["https://api.siliconflow.com/v1/models"] }],
  ["nekocode", "NekoCode", "anthropic", "https://nekocode.ai", "https://nekocode.ai?aff=CCSWITCH", "https://nekocode.ai/v1", [], {}],
  ["a6api", "A6API", "anthropic", "https://www.a6api.com", "https://a6api.com/register?aff=AqNr", "https://api.a6api.com/v1", [], {}],
  ["atlascloud", "AtlasCloud", "anthropic", "https://www.atlascloud.ai/console/coding-plan", "https://www.atlascloud.ai/console/coding-plan", "https://api.atlascloud.ai/v1", [], {}],
  ["ucloud", "Compshare", "anthropic", "https://www.compshare.cn", "https://www.compshare.cn/coding-plan", "https://api.modelverse.cn/v1", [], {}],
  ["ucloud_coding", "Compshare Coding Plan", "anthropic", "https://www.compshare.cn", "https://www.compshare.cn/coding-plan", "https://cp.compshare.cn/v1", [], {}],
  ["ccsub", "CCSub", "anthropic", "https://www.ccsub.net", "https://www.ccsub.net/register?ref=Y6Z8DXEA", "https://www.ccsub.net/v1", [], {}],
  ["sssaicode", "SSSAiCode", "anthropic", "https://sssaicodeapi.com", "https://sssaicodeapi.com/register?ref=DCP0SM", "https://node-hk.sssaicodeapi.com/api", [], { modelCatalogURLs: ["https://node-hk.sssaicodeapi.com/api/v1/models", "https://node-hk.sssaiapi.com/api/v1/models", "https://node-cf.sssaicodeapi.com/api/v1/models"] }],
  ["micu", "Micu", "anthropic", "https://www.micuapi.ai", "https://www.micuapi.ai/register?aff=aOYQ", "https://www.micuapi.ai/v1", [], {}],
  ["rightcode", "RightCode", "anthropic", "https://www.rightapi.ai", "https://www.rightapi.ai/register?aff=CCSWITCH", "https://www.rightapi.ai/claude", [], {}],
  ["etok", "ETok.ai", "anthropic", "https://etok.ai", "https://etok.ai", "https://api.etok.ai/v1", [], {}],
  ["cubence", "Cubence", "anthropic", "https://cubence.com", "https://cubence.com/signup?code=CCSWITCH&source=ccs", "https://api.cubence.com/v1", [], { modelCatalogURLs: ["https://api.cubence.com/v1/models", "https://api-cf.cubence.com/v1/models", "https://api-dmit.cubence.com/v1/models", "https://api-bwg.cubence.com/v1/models"] }],
  ["crazyrouter", "CrazyRouter", "anthropic", "https://www.crazyrouter.com", "https://www.crazyrouter.com/register?aff=OZcm&ref=cc-switch", "https://cn.crazyrouter.com/v1", [], {}],
  ["dmxapi", "DMXAPI", "anthropic", "https://www.dmxapi.cn", "https://www.dmxapi.cn", "https://www.dmxapi.cn/v1", [], { modelCatalogURLs: ["https://www.dmxapi.cn/v1/models", "https://api.dmxapi.cn/v1/models"] }],
  ["qiniu", "Qiniu", "anthropic", "https://s.qiniu.com/nMvAvy", "https://s.qiniu.com/nMvAvy", "https://api.qnaigc.com/v1", [], { modelCatalogURLs: ["https://api.qnaigc.com/v1/models", "https://api.modelink.ai/v1/models"] }],
  ["sudocode_chat", "SudoCode.chat", "anthropic", "https://sudocode.chat", "https://sudocode.chat/sign-up?aff=CC-SWITCH", "https://api.sudocode.chat/v1", [], {}],
  ["sudocode_us", "SudoCode.us", "anthropic", "https://sudocode.us", "https://sudocode.us", "https://sudocode.us/v1", [], { modelCatalogURLs: ["https://sudocode.us/v1/models", "https://sudocode.run/v1/models"] }],
  ["aihubmix", "AiHubMix", "anthropic", "https://aihubmix.com", "https://aihubmix.com", "https://aihubmix.com/v1", [], { modelCatalogURLs: ["https://aihubmix.com/v1/models", "https://api.aihubmix.com/v1/models"] }],
  ["amux", "Amux", "anthropic", "https://amux.ai", "https://amux.ai", "https://api.amux.ai/v1", [], {}],
  ["qianfan_coding", "Baidu Qianfan Coding Plan", "anthropic", "https://cloud.baidu.com/product/qianfan_modelbuilder", "https://console.bce.baidu.com/qianfan/ais/console/applicationConsole/application", "https://qianfan.baidubce.com/anthropic/coding", ["qianfan-code-latest"], {}],
  ["bailian", "Bailian", "anthropic", "https://bailian.console.aliyun.com", "https://bailian.console.aliyun.com", "https://dashscope.aliyuncs.com/apps/anthropic", [], {}],
  ["bailian_coding", "Bailian For Coding", "anthropic", "https://bailian.console.aliyun.com", "https://bailian.console.aliyun.com", "https://coding.dashscope.aliyuncs.com/apps/anthropic", [], {}],
  ["bailing", "BaiLing", "anthropic", "https://alipaytbox.yuque.com/sxs0ba/ling/get_started", "https://alipaytbox.yuque.com/sxs0ba/ling/get_started", "https://api.tbox.cn/api/anthropic", ["Ling-2.5-1T"], {}],
  ["cherryin", "CherryIN", "anthropic", "https://open.cherryin.ai", "https://open.cherryin.ai/console/token", "https://open.cherryin.net", ["anthropic/claude-sonnet-5", "anthropic/claude-opus-5", "anthropic/claude-haiku-4.5"], { modelCatalogURLs: ["https://open.cherryin.net/v1/models"] }],
  ["codex", "Codex", "openai", "https://openai.com/chatgpt/pricing", "https://chatgpt.com", "https://chatgpt.com/backend-api/codex", ["gpt-5.6-sol", "gpt-5.6-luna"], { modelCatalogStatus: "manual_only", usageStatus: "none", usageSource: "OAuth 专用入口，不走普通 API Key 余额查询" }],
  ["deepseek", "DeepSeek", "openai", "https://platform.deepseek.com", "https://platform.deepseek.com/api_keys", "https://api.deepseek.com/v1", ["deepseek-chat", "deepseek-reasoner"], { usageStatus: "fixed", usageProvider: "deepseek", modelCatalogURLs: ["https://api.deepseek.com/v1/models", "https://api.deepseek.com/anthropic/v1/models"] }],
  ["eflowcode", "E-FlowCode", "anthropic", "https://e-flowcode.cc", "https://e-flowcode.cc", "https://e-flowcode.cc/v1", [], {}],
  ["gemini_native", "Gemini Native", "gemini", "https://ai.google.dev/gemini-api", "https://aistudio.google.com/app/apikey", "https://generativelanguage.googleapis.com/v1beta", ["gemini-2.5-pro", "gemini-2.5-flash"], { modelCatalogStatus: "gemini_models", modelCatalogURLs: ["https://generativelanguage.googleapis.com/v1beta/models"], usageStatus: "none", usageSource: "Google 官方模型目录；公开用量接口未接入" }],
  ["github_copilot", "GitHub Copilot", "openai", "https://github.com/features/copilot", "https://github.com/settings/copilot", "https://api.githubcopilot.com", ["claude-sonnet-4-5", "gpt-4.1"], { modelCatalogStatus: "manual_only", usageStatus: "none", usageSource: "OAuth 专用入口，不走普通 API Key 余额查询" }],
  ["longcat", "Longcat", "anthropic", "https://longcat.chat/platform", "https://longcat.chat/platform/api_keys", "https://api.longcat.chat/anthropic", ["LongCat-2.0"], {}],
  ["minimax", "MiniMax", "anthropic", "https://platform.minimaxi.com", "https://platform.minimaxi.com/subscribe/coding-plan", "https://api.minimaxi.com/anthropic", ["MiniMax-M2.7"], { usageStatus: "token_plan", usageProvider: "minimax" }],
  ["minimax_en", "MiniMax en", "anthropic", "https://platform.minimax.io", "https://platform.minimax.io/subscribe/coding-plan", "https://api.minimax.io/anthropic", ["MiniMax-M2.7"], { usageStatus: "token_plan", usageProvider: "minimax" }],
  ["modelscope", "ModelScope", "anthropic", "https://modelscope.cn", "https://modelscope.cn", "https://api-inference.modelscope.cn", ["ZhipuAI/GLM-5.1"], {}],
  ["novita", "Novita AI", "anthropic", "https://novita.ai", "https://novita.ai", "https://api.novita.ai/anthropic", ["zai-org/glm-5.1"], { usageStatus: "fixed", usageProvider: "novita", modelCatalogURLs: ["https://api.novita.ai/anthropic/v1/models"] }],
  ["nvidia", "Nvidia", "openai", "https://build.nvidia.com", "https://build.nvidia.com/settings/api-keys", "https://integrate.api.nvidia.com/v1", ["moonshotai/kimi-k2.5"], {}],
  ["opencode_go", "OpenCode Go", "openai", "https://opencode.ai/go", "https://opencode.ai/go?ref=2YTRG2NGTX", "https://opencode.ai/zen/go", ["deepseek-v4-flash"], { modelCatalogURLs: ["https://opencode.ai/zen/go/v1/models"] }],
  ["openrouter", "OpenRouter", "openai", "https://openrouter.ai", "https://openrouter.ai/keys", "https://openrouter.ai/api/v1", ["openai/gpt-4.1", "anthropic/claude-sonnet-4"], { usageStatus: "fixed", usageProvider: "openrouter", modelCatalogURLs: ["https://openrouter.ai/api/v1/models"] }],
  ["pipellm", "PIPELLM", "anthropic", "https://code.pipellm.ai", "https://code.pipellm.ai/login?ref=uvw650za", "https://cc-api.pipellm.ai/v1", [], {}],
  ["relaxycode", "RelaxyCode", "anthropic", "https://www.relaxycode.com", "https://www.relaxycode.com/register", "https://www.relaxycode.com/v1", [], {}],
  ["stepfun", "StepFun", "openai", "https://platform.stepfun.com/step-plan", "https://platform.stepfun.com/interface-key", "https://api.stepfun.com/step_plan", ["step-3.5-flash-2603"], { usageStatus: "fixed", usageProvider: "stepfun", modelCatalogURLs: ["https://api.stepfun.com/step_plan/v1/models", "https://api.stepfun.com/v1/models"] }],
  ["stepfun_en", "StepFun en", "openai", "https://platform.stepfun.ai/step-plan", "https://platform.stepfun.ai/interface-key", "https://api.stepfun.ai/step_plan", ["step-3.5-flash-2603"], { usageStatus: "fixed", usageProvider: "stepfun", modelCatalogURLs: ["https://api.stepfun.ai/step_plan/v1/models", "https://api.stepfun.ai/v1/models"] }],
  ["therouter", "TheRouter", "anthropic", "https://therouter.ai", "https://dashboard.therouter.ai", "https://api.therouter.ai/v1", ["anthropic/claude-sonnet-5", "anthropic/claude-opus-5"], {}],
  ["xai", "xAI (Grok)", "openai", "https://x.ai/grok", "https://console.x.ai", "https://api.x.ai/v1", ["grok-4.5"], {}],
  ["xiaomi_mimo", "Xiaomi MiMo", "anthropic", "https://platform.xiaomimimo.com", "https://platform.xiaomimimo.com/#/console/api-keys", "https://api.xiaomimimo.com/anthropic", ["mimo-v2.5-pro"], {}],
  ["xiaomi_mimo_token_plan_cn", "Xiaomi MiMo Token Plan (China)", "anthropic", "https://platform.xiaomimimo.com/#/token-plan", "https://platform.xiaomimimo.com/#/console/plan-manage", "https://token-plan-cn.xiaomimimo.com/anthropic", ["mimo-v2.5-pro"], { usageStatus: "none", usageSource: "Token Plan 用量接口未在当前后端核验" }],
  ["zhipu_glm", "Zhipu GLM", "anthropic", "https://open.bigmodel.cn", "https://www.bigmodel.cn/claude-code?ic=RRVJPB5SII", "https://open.bigmodel.cn/api/anthropic", ["glm-5.1"], { usageStatus: "token_plan", usageProvider: "zhipu" }],
  ["zhipu_glm_en", "Zhipu GLM en", "anthropic", "https://z.ai", "https://z.ai/subscribe?ic=8JVLJQFSKB", "https://api.z.ai/api/anthropic", ["glm-5.1"], { usageStatus: "token_plan", usageProvider: "zhipu" }],
];

const SUPPLIER_LABEL_COLLATOR = new Intl.Collator("en", {
  numeric: true,
  sensitivity: "base",
});

function compareSupplierRows(left, right) {
  const labelOrder = SUPPLIER_LABEL_COLLATOR.compare(left[1], right[1]);
  return labelOrder || String(left[0]).localeCompare(String(right[0]));
}

const supplierRows = catalogRows
  .slice()
  .sort(compareSupplierRows)
  .map(([id, label, type, websiteURL, apiKeyURL, baseURL, models, options]) =>
    createTemplate({ id, label, type, websiteURL, apiKeyURL, baseURL, models, ...options }),
  );

export const SUPPLIER_TEMPLATES = [...coreTemplates, ...supplierRows];

const supplierByID = new Map(SUPPLIER_TEMPLATES.map((item) => [item.id, item]));
const compatibilityTemplates = new Map([
  ["moonshot", createTemplate({
    id: "moonshot",
    label: "Kimi / Moonshot（兼容配置）",
    type: "openai",
    baseURL: "https://api.moonshot.cn/v1",
    models: ["kimi-k2.5", "moonshot-v1-128k"],
    usageStatus: "fixed",
    usageProvider: "moonshot",
    modelCatalogURLs: ["https://api.moonshot.cn/v1/models"],
  })],
  ["zhipu", createTemplate({
    id: "zhipu",
    label: "Zhipu GLM（兼容配置）",
    type: "openai",
    baseURL: "https://open.bigmodel.cn/api/paas/v4",
    models: ["glm-4.5", "glm-4.6"],
    usageStatus: "token_plan",
    usageProvider: "zhipu",
  })],
  ["zhipu_team", createTemplate({
    id: "zhipu_team",
    label: "Zhipu GLM Team（兼容配置）",
    type: "openai",
    baseURL: "https://open.bigmodel.cn/api/paas/v4",
    models: ["glm-4.5", "glm-4.6"],
    usageStatus: "token_plan",
    usageProvider: "zhipu_team",
  })],
  ["zenmux", createTemplate({
    id: "zenmux",
    label: "ZenMux（兼容配置）",
    type: "anthropic",
    baseURL: "https://zenmux.ai/api/anthropic",
    usageStatus: "token_plan",
    usageProvider: "zenmux",
  })],
  ["volcengine", createTemplate({
    id: "volcengine",
    label: "火山方舟（兼容配置）",
    type: "openai",
    baseURL: "https://ark.cn-beijing.volces.com/api/coding/v3",
    models: ["doubao-seed-code"],
    usageStatus: "token_plan",
    usageProvider: "volcengine",
  })],
  ["unity2", createTemplate({
    id: "unity2",
    label: "Unity2.ai（兼容配置）",
    type: "anthropic",
    baseURL: "https://api.unity2.ai/v1",
  })],
]);

export function supplierTemplate(id) {
  const normalizedID = String(id || "custom").trim().toLowerCase() || "custom";
  const direct = supplierByID.get(normalizedID);
  if (direct) return direct;
  return compatibilityTemplates.get(normalizedID) || supplierByID.get("custom");
}

export function supplierSelectOptions(currentID = "") {
  const options = SUPPLIER_TEMPLATES.map(({ id, label, iconURL, iconLight }) => ({
    value: id,
    label,
    iconURL,
    iconLight,
  }));
  const normalizedID = String(currentID || "").trim().toLowerCase();
  if (compatibilityTemplates.has(normalizedID)) {
    const compatibility = compatibilityTemplates.get(normalizedID);
    options.push({
      value: compatibility.id,
      label: compatibility.label,
      iconURL: compatibility.iconURL,
      iconLight: compatibility.iconLight,
    });
  }
  return options;
}

export function supplierLabel(id) {
  return supplierTemplate(id)?.label || "自定义供应商";
}

export function supplierUsageStatus(id) {
  return supplierTemplate(id)?.usage || supplierTemplate("custom").usage;
}

export function supplierUsageRequest(adapterOrID) {
  const adapter = adapterOrID && typeof adapterOrID === "object" ? adapterOrID : null;
  const id = String(adapter ? adapter.supplierID : adapterOrID || "custom").trim().toLowerCase() || "custom";
  if (adapter) {
    const profile = String(adapter.balanceProfile || "").trim().toLowerCase();
    if (profile === "none") return { status: "none", provider: "" };
    if (profile === "custom") return { status: "custom_only", provider: "" };
    if (profile === "" || profile === "auto") {
      const queryURL = String(adapter.balanceQueryURL || "").trim();
      const queryField = String(adapter.balanceQueryField || "").trim();
      if (queryURL && queryField) return { status: "custom_only", provider: "" };
    }
    if (profile === "general") return { status: "general", provider: "" };
    if (profile === "newapi") return { status: "newapi", provider: "" };
    if (profile === "token_plan") {
      const usage = supplierUsageStatus(id);
      return { status: "token_plan", provider: adapter.balanceCodingPlanProvider || usage.provider || "" };
    }
    if (profile === "official") {
      const usage = supplierUsageStatus(id);
      return { status: "fixed", provider: usage.provider || "" };
    }
  }
  const usage = supplierUsageStatus(id);
  return { status: usage.status || "", provider: usage.provider || "" };
}

export function supplierModelCatalog(id) {
  return supplierTemplate(id)?.modelCatalog || supplierTemplate("custom").modelCatalog;
}

export function supplierCatalogURLs(id) {
  const catalog = supplierModelCatalog(id);
  return Array.isArray(catalog.urls) ? catalog.urls.slice() : [];
}
