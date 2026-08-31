package config

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/runtimeconfig"
	"cursor/internal/i18n"
	"cursor/internal/modelchannel"
	"cursor/internal/modelcontext"
	"cursor/internal/routing"
	legacyruntime "cursor/internal/runtime"
)

const (
	DefaultBackendListenAddr                = "127.0.0.1:18090"
	DefaultProxyListenAddr                  = "127.0.0.1:18080"
	DefaultRoutingMode                      = "local"
	DefaultProviderStreamIdleTimeoutSeconds = 90
	MinProviderStreamIdleTimeoutSeconds     = 30
	// DefaultTurnStaleTimeoutSeconds 表示一轮回合进入「等待外部（工具/交互结果）」后，
	// 在无任何进展时由 turn-staleness 看门狗触发自救的默认阈值，单位秒。
	DefaultTurnStaleTimeoutSeconds = 120
	// MinTurnStaleTimeoutSeconds 表示 turn-staleness 看门狗允许的最小阈值，单位秒。
	MinTurnStaleTimeoutSeconds = 30
	// DefaultNativeDelegationProgressTimeoutSeconds 表示 native Cursor 子代理「无有效进展」的
	// 默认超时阈值，单位秒：子代理既无工具结果、又无模型输出/思考活动时判定超时。
	DefaultNativeDelegationProgressTimeoutSeconds = 300
	// MinNativeDelegationProgressTimeoutSeconds 表示 native 子代理无进展看门狗允许的最小阈值，单位秒。
	MinNativeDelegationProgressTimeoutSeconds = 60
	// DefaultLocalResponseCacheTTLSeconds 表示本地响应缓存条目的默认存活时长。
	// 条目持久化到磁盘，TTL 指「多久未访问则失效」；30 天覆盖跨会话的重复请求。
	DefaultLocalResponseCacheTTLSeconds = 30 * 24 * 3600
	// DefaultLocalResponseCacheMaxEntries 表示本地响应缓存的默认最大条目数（LRU 淘汰）。
	DefaultLocalResponseCacheMaxEntries = 2048
	// DefaultAutoMatchContextWindow 表示是否在启动时/手动触发时自动为所有模型适配器
	// 配对正确的上下文窗口（目录命中仅下调修正过大值，目录无则探测 provider /models 回填）。
	DefaultAutoMatchContextWindow = true
	// DefaultSkillMCPScanEnabled 表示是否默认开启跨工具 Skills/MCP 自动扫描与注入。
	// 开启时扫描主流编码工具的技能/MCP 配置，合并进 RequestContext，还原原生注入。
	DefaultSkillMCPScanEnabled = true
	// DefaultBillingQueryEnabled 表示是否默认允许向上游查询余额/计费接口。
	// 关闭后首页不再轮询、模型配置/供应商详情页的手动查询也会被拦截；本地成本估算不受影响。
	DefaultBillingQueryEnabled = true
)

type ModelPricing = legacyruntime.ModelPricing

type ModelAdapterConfig struct {
	ID string `json:"id,omitempty" yaml:"id,omitempty"`
	// Source 区分第三方 API 与 Cursor 账户模型；缺失值兼容为 third_party。
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
	// CredentialScope 限制真实凭据的注入边界，避免账户凭据落入第三方 adapter。
	CredentialScope string `json:"credentialScope,omitempty" yaml:"credentialScope,omitempty"`
	DisplayName     string `json:"displayName" yaml:"displayName"`
	GroupName       string `json:"groupName,omitempty" yaml:"groupName,omitempty"`
	Type            string `json:"type" yaml:"type"`
	// SupplierID 是品牌供应商标识，仅用于模板、展示和供应商专用能力；协议仍由 Type 决定。
	SupplierID    string `json:"supplierID,omitempty" yaml:"supplierID,omitempty"`
	ProtocolMode  string `json:"protocolMode,omitempty" yaml:"protocolMode,omitempty"`
	ProtocolGroup string `json:"protocolGroup,omitempty" yaml:"protocolGroup,omitempty"`
	BaseURL       string `json:"baseURL" yaml:"baseURL"`
	APIKey        string `json:"apiKey" yaml:"apiKey"`
	// APIKeys 是同渠道的备用密钥池（不含主 apiKey）：请求时按渠道维度轮换使用，
	// 每把密钥是独立的冷却单元——单把限流/失效只冷却该密钥，不拖垮整个模型。
	APIKeys                     []string `json:"apiKeys,omitempty" yaml:"apiKeys,omitempty"`
	TooltipData                 string   `json:"tooltipData" yaml:"tooltipData"`
	ModelID                     string   `json:"modelID" yaml:"modelID"`
	ReasoningEffort             string   `json:"reasoningEffort" yaml:"reasoningEffort"`
	OpenAIEndpoint              string   `json:"openAIEndpoint" yaml:"openAIEndpoint"`
	OpenAIRequestGroup          string   `json:"openAIRequestGroup,omitempty" yaml:"openAIRequestGroup,omitempty"`
	OpenAIExtraParamsEnabled    bool     `json:"openAIExtraParamsEnabled" yaml:"openAIExtraParamsEnabled"`
	OpenAIExtraParamsJSON       string   `json:"openAIExtraParamsJSON" yaml:"openAIExtraParamsJSON"`
	CustomHeadersEnabled        bool     `json:"customHeadersEnabled" yaml:"customHeadersEnabled"`
	CustomHeadersJSON           string   `json:"customHeadersJSON" yaml:"customHeadersJSON"`
	AnthropicExtraParamsEnabled bool     `json:"anthropicExtraParamsEnabled" yaml:"anthropicExtraParamsEnabled"`
	AnthropicExtraParamsJSON    string   `json:"anthropicExtraParamsJSON" yaml:"anthropicExtraParamsJSON"`
	// AnthropicAuthMode controls generated Messages authentication. Missing legacy
	// values normalize to legacy_dual to preserve existing proxy compatibility.
	AnthropicAuthMode       string        `json:"anthropicAuthMode,omitempty" yaml:"anthropicAuthMode,omitempty"`
	ContextWindowTokens     int           `json:"contextWindowTokens" yaml:"contextWindowTokens"`
	MaxCompletionTokens     int           `json:"maxCompletionTokens" yaml:"maxCompletionTokens"`
	AnthropicMaxTokens      int           `json:"anthropicMaxTokens" yaml:"anthropicMaxTokens"`
	AnthropicThinkingEffort string        `json:"anthropicThinkingEffort,omitempty" yaml:"anthropicThinkingEffort,omitempty"`
	ThinkingBudgetTokens    int           `json:"thinkingBudgetTokens" yaml:"thinkingBudgetTokens"`
	Pricing                 *ModelPricing `json:"pricing,omitempty" yaml:"pricing,omitempty"`
	FastMode                bool          `json:"fastMode,omitempty" yaml:"fastMode,omitempty"`
	OpenAIServiceTier       string        `json:"openAIServiceTier,omitempty" yaml:"openAIServiceTier,omitempty"`
	ModelCatalogURL         string        `json:"modelCatalogURL,omitempty" yaml:"modelCatalogURL,omitempty"`
	// 余额查询（可配置兜底）：全部可选，零值即「未启用」。
	// 具名 provider 未命中时，若 BalanceQueryURL 非空，则用它发一次 GET 并按点分路径取值。
	// URL/Headers 支持 {{apiKey}}、{{baseUrl}}、{{accessToken}}、{{userId}} 占位符替换。
	BalanceQueryURL     string            `json:"balanceQueryURL,omitempty" yaml:"balanceQueryURL,omitempty"`
	BalanceQueryField   string            `json:"balanceQueryField,omitempty" yaml:"balanceQueryField,omitempty"`
	BalanceQueryHeaders map[string]string `json:"balanceQueryHeaders,omitempty" yaml:"balanceQueryHeaders,omitempty"`
	// BalanceProfile 对齐 cc-switch 用量模板：none | general | newapi | token_plan | custom | official。
	// auto 仅为旧配置兼容值：后端按 baseURL / 字段推断；official 走具名官方余额接口。
	BalanceProfile string `json:"balanceProfile,omitempty" yaml:"balanceProfile,omitempty"`
	// New API 等站点的 Web 访问令牌（与渠道 sk 不同）。
	BalanceAccessToken string `json:"balanceAccessToken,omitempty" yaml:"balanceAccessToken,omitempty"`
	// New API 的 New-Api-User 用户 ID。
	BalanceUserID string `json:"balanceUserID,omitempty" yaml:"balanceUserID,omitempty"`
	// Token Plan 显式供应商：kimi | zhipu | zhipu_team | minimax | zenmux | volcengine。
	// 空则按 baseURL 自动检测（zhipu_team 无法自动区分，须显式指定）。
	BalanceCodingPlanProvider string `json:"balanceCodingPlanProvider,omitempty" yaml:"balanceCodingPlanProvider,omitempty"`
	// CompatibilityKind 显式指定 OpenAI 兼容供应商的兼容策略类别（quirk kind）。
	// 非空且合法时优先生效，跳过按 baseURL/模型名的字符串信号自动匹配，避免上游
	// 改 URL 前缀导致误判。合法值：openai、copilot、deepseek、xai、kimi、openrouter、
	// siliconflow、zhipu、qwen、mimo、minimax、stepfun；"openai" 表示强制默认策略。
	// 空（默认，自动匹配）或非法值（记 warning）均回落自动匹配。
	CompatibilityKind string `json:"compatibilityKind,omitempty" yaml:"compatibilityKind,omitempty"`
	// ToolCallMode 控制工具调用协议：native（默认，原生 tool_calls）或
	// xml_prompt（无原生工具调用能力的弱模型/本地模型改用 in-band XML 协议：
	// 工具目录注入系统提示，模型以 <tool_call> 文本块发起调用）。仅 OpenAI
	// chat completions 路径实现；空值等价 native。
	ToolCallMode string `json:"toolCallMode,omitempty" yaml:"toolCallMode,omitempty"`
	// Disabled 停用该渠道：不出现在 Cursor 的模型列表，也不参与请求路由。
	// 由「测试失败自动停用 / 测试成功自动恢复」联动维护，也可在界面手动启停。
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`
}

type RoutingConfig struct {
	Mode   string         `json:"mode" yaml:"mode"`
	Policy routing.Policy `json:"policy,omitempty" yaml:"policy,omitempty"`
}

// ComputerUseConfig 控制 ComputerUse 工具的本地执行后端。
// desktop=操作真实屏幕（Win32 截图+鼠标键盘）；browser=转发到浏览器 MCP server（如 Playwright MCP，适合前端验证）。
type ComputerUseConfig struct {
	Mode            string `json:"mode" yaml:"mode"`                       // "desktop"（默认）/ "browser"
	BrowserStartURL string `json:"browserStartUrl" yaml:"browserStartUrl"` // 浏览器模式初始 URL，默认 about:blank
}

type HomeMetricsConfig struct {
	IncludeCacheWriteInHitRate bool `json:"includeCacheWriteInHitRate" yaml:"includeCacheWriteInHitRate"`
}

// BillingQueryConfig 控制向上游发起余额/计费查询的总开关。
// 只影响对上游计费/余额接口的调用；基于内置价格表的本地成本估算不受影响。
type BillingQueryConfig struct {
	// Enabled 表示是否允许向上游查询余额/计费接口；默认 true。
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// LocalResponseCacheConfig 控制本地（进程内）精确匹配 LLM 响应缓存。
// 默认（零值）为关闭：Enabled 为 false 时 provider 链路与今日完全一致。
type LocalResponseCacheConfig struct {
	// Enabled 表示是否启用本地响应缓存；默认 false（关闭）。
	Enabled bool `json:"enabled" yaml:"enabled"`
	// TTLSeconds 表示缓存条目的存活秒数；<=0 时回退默认值。
	TTLSeconds int `json:"ttlSeconds,omitempty" yaml:"ttlSeconds,omitempty"`
	// MaxEntries 表示缓存最大条目数；<=0 时回退默认值。
	MaxEntries int `json:"maxEntries,omitempty" yaml:"maxEntries,omitempty"`
	// Persist 表示是否把缓存条目持久化到磁盘（跨进程/重启保留）；默认 true。
	Persist bool `json:"persist,omitempty" yaml:"persist,omitempty"`
}

// SkillMCPScanConfig 控制跨工具的 Skills / MCP 自动扫描与注入。
// 默认（Enabled 为 true）开启：扫描主流编码工具（Cursor/Claude/Codex/ZCode/.agents 等）
// 的技能与 MCP 配置，合并进 RequestContext，还原原生 <agent_skills>/<mcp_file_system> 注入。
type SkillMCPScanConfig struct {
	// Enabled 总开关；默认 true。
	Enabled bool `json:"enabled" yaml:"enabled"`
	// SkillSources 按工具分类控制技能扫描来源；key 为分类标签，value 为是否启用。
	// 缺省（nil）表示全部启用。分类：cursor/claude/codex/shared/zcode/zcode-plugin/byok。
	SkillSources map[string]bool `json:"skillSources,omitempty" yaml:"skillSources,omitempty"`
	// MCPSources 按工具分类控制 MCP 配置扫描来源；缺省全部启用。
	MCPSources map[string]bool `json:"mcpSources,omitempty" yaml:"mcpSources,omitempty"`
	// EnabledSkills 显式启用的技能名集合（小写匹配）；缺省表示全部关闭。
	EnabledSkills map[string]bool `json:"enabledSkills,omitempty" yaml:"enabledSkills,omitempty"`
	// DisabledSkills 是旧版黑名单字段，仅保留配置反序列化兼容。
	DisabledSkills map[string]bool `json:"disabledSkills,omitempty" yaml:"disabledSkills,omitempty"`
	// DisabledMCPServers 显式禁用的 MCP server identifier 集合（小写匹配）。
	DisabledMCPServers map[string]bool `json:"disabledMcpServers,omitempty" yaml:"disabledMcpServers,omitempty"`
	// SkillSummaries 用户为技能生成的简介（key 为技能名小写）；仅存配置，不写回 SKILL.md。
	SkillSummaries map[string]string `json:"skillSummaries,omitempty" yaml:"skillSummaries,omitempty"`
	// MCPSummaries 用户为 MCP server 生成的简介（key 为 identifier 小写）。
	MCPSummaries map[string]string `json:"mcpSummaries,omitempty" yaml:"mcpSummaries,omitempty"`
}

// MirrorCaptureConfig 是镜像记录配置。
type MirrorCaptureConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
	// ProtocolFidelity 让镜像记录以 Base64 保留完整协议帧原始字节，并解码 Cursor 协议结构时间线。
	// 默认关闭：关闭时 body 按字符串写入，protobuf 的非 UTF-8 字节会被 JSON 替换成 U+FFFD 而不可还原。
	// 开启会显著增加落盘体积与运行时开销，且原始字节含完整对话内容与工作区上下文。
	ProtocolFidelity bool     `json:"protocolFidelity" yaml:"protocolFidelity"`
	Hosts            []string `json:"hosts,omitempty" yaml:"hosts,omitempty"`
}

// DefaultMirrorHosts 是 Cursor 官方 key 直连模式使用的模型 API 入口；Hosts 为空时回落。
var DefaultMirrorHosts = []string{
	"api.openai.com",
	"api.anthropic.com",
	"generativelanguage.googleapis.com",
}

// CursorRelayMirrorHosts 是 Cursor 客户端与官方后端之间的 relay 入口。
// 只有这些域名承载 BidiAppend / AgentServerMessage 协议帧，因此协议保真记录必须覆盖它们。
var CursorRelayMirrorHosts = []string{
	"api2.cursor.sh",
	"api3.cursor.sh",
	"api4.cursor.sh",
}

// ResolveMirrorCaptureHosts 返回镜像记录实际生效的域名列表（小写去重）。
// 开启协议保真后并入 CursorRelayMirrorHosts：这些域名本就无条件走 MITM 解密
// （isWhitelistedRelayHost），并且在本地服务模式下会先被本地 relay 分流，
// 因此并入只在「官方上游模式 + 镜像记录 + 协议保真」三者同时成立时才产生落盘。
func ResolveMirrorCaptureHosts(mirror MirrorCaptureConfig) []string {
	hosts := mirror.Hosts
	if len(hosts) == 0 {
		hosts = DefaultMirrorHosts
	}
	if mirror.ProtocolFidelity {
		hosts = append(append(make([]string, 0, len(hosts)+len(CursorRelayMirrorHosts)), hosts...), CursorRelayMirrorHosts...)
	}
	resolved := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		resolved = append(resolved, normalized)
	}
	return resolved
}

type Config struct {
	Log bool `json:"log" yaml:"log"`
	// MirrorCapture 控制 MITM 镜像记录：对模型 API 域名解密后记录请求/响应明文并直通官方。
	// 默认关闭；开启后仅记录不阻断，官方链路不受影响。
	MirrorCapture MirrorCaptureConfig `json:"mirrorCapture" yaml:"mirrorCapture"`
	// DebugLogMaxBytes 限制每个 debug jsonl 文件的最大字节数；超过后保留尾部（错误附近）。
	// 0 表示用默认值（50MB），负数表示不限制。热加载即时生效。
	DebugLogMaxBytes                int    `json:"debugLogMaxBytes" yaml:"debugLogMaxBytes"`
	ProviderStreamIdleTimeout       int    `json:"providerStreamIdleTimeout" yaml:"providerStreamIdleTimeout"`
	TurnStaleTimeout                int    `json:"turnStaleTimeout" yaml:"turnStaleTimeout"`
	NativeDelegationProgressTimeout int    `json:"nativeDelegationProgressTimeout" yaml:"nativeDelegationProgressTimeout"`
	AutoMatchContextWindow          bool   `json:"autoMatchContextWindow" yaml:"autoMatchContextWindow"`
	AutoDisableFailedModels         bool   `json:"autoDisableFailedModels" yaml:"autoDisableFailedModels"`
	BackendListenAddr               string `json:"backendListenAddr" yaml:"backendListenAddr"`
	ProxyListenAddr                 string `json:"proxyListenAddr" yaml:"proxyListenAddr"`
	// AllowNonLoopbackListen 显式允许 backend/proxy 绑定非环回地址。
	// 默认 false：本地 MITM/Agent 面未鉴权，绑定 0.0.0.0 会把服务暴露到局域网。
	AllowNonLoopbackListen bool                           `json:"allowNonLoopbackListen" yaml:"allowNonLoopbackListen"`
	ModelAdapters          []ModelAdapterConfig           `json:"modelAdapters" yaml:"modelAdapters"`
	Routing                RoutingConfig                  `json:"routing" yaml:"routing"`
	HomeMetrics            HomeMetricsConfig              `json:"homeMetrics" yaml:"homeMetrics"`
	BillingQuery           BillingQueryConfig             `json:"billingQuery" yaml:"billingQuery"`
	LocalResponseCache     LocalResponseCacheConfig       `json:"localResponseCache" yaml:"localResponseCache"`
	SkillMCPScan           SkillMCPScanConfig             `json:"skillMcpScan" yaml:"skillMcpScan"`
	MCPTrustGrants         []runtimeconfig.MCPTrustRecord `json:"mcpTrustGrants,omitempty" yaml:"mcpTrustGrants,omitempty"`
	Delegation             DelegationConfig               `json:"delegation" yaml:"delegation"`
	ComputerUse            ComputerUseConfig              `json:"computerUse" yaml:"computerUse"`
	LastAgentModelHash     string                         `json:"lastAgentModelHash" yaml:"lastAgentModelHash"`
}

func DefaultConfig() Config {
	return Config{
		Log:                             false,
		MirrorCapture:                   MirrorCaptureConfig{Enabled: false, Hosts: DefaultMirrorHosts},
		ProviderStreamIdleTimeout:       DefaultProviderStreamIdleTimeoutSeconds,
		TurnStaleTimeout:                DefaultTurnStaleTimeoutSeconds,
		NativeDelegationProgressTimeout: DefaultNativeDelegationProgressTimeoutSeconds,
		AutoMatchContextWindow:          DefaultAutoMatchContextWindow,
		AutoDisableFailedModels:         false,
		BackendListenAddr:               DefaultBackendListenAddr,
		ProxyListenAddr:                 DefaultProxyListenAddr,
		ModelAdapters:                   []ModelAdapterConfig{},
		BillingQuery:                    BillingQueryConfig{Enabled: DefaultBillingQueryEnabled},
		Routing: RoutingConfig{
			Mode:   DefaultRoutingMode,
			Policy: routing.DefaultPolicy(),
		},
		SkillMCPScan: SkillMCPScanConfig{
			Enabled: DefaultSkillMCPScanEnabled,
		},
		Delegation: DelegationConfig{
			Enabled:               true,
			MaxConcurrency:        DefaultDelegationMaxConcurrency,
			ExecutorFailoverLimit: DefaultDelegationExecutorFailoverLimit,
			Supervision: DelegationSupervisionConfig{
				Enabled:        false,
				MaxCorrections: DefaultDelegationMaxCorrections,
				MaxRetries:     DefaultDelegationMaxRetries,
				MaxRounds:      DefaultDelegationMaxRounds,
			},
			VisionDelegation: VisionDelegationConfig{
				Enabled: false,
				Mode:    VisionModeAuto,
			},
		},
		ComputerUse: ComputerUseConfig{
			Mode:            "desktop",
			BrowserStartURL: "about:blank",
		},
	}
}

func NormalizeConfig(input Config) (Config, error) {
	output := DefaultConfig()
	output.Log = input.Log
	output.DebugLogMaxBytes = input.DebugLogMaxBytes
	output.ProviderStreamIdleTimeout = normalizeProviderStreamIdleTimeout(input.ProviderStreamIdleTimeout)
	output.TurnStaleTimeout = normalizeTurnStaleTimeout(input.TurnStaleTimeout)
	output.NativeDelegationProgressTimeout = normalizeNativeDelegationProgressTimeout(input.NativeDelegationProgressTimeout)
	output.AutoMatchContextWindow = normalizeAutoMatchContextWindow(input.AutoMatchContextWindow)
	output.AutoDisableFailedModels = input.AutoDisableFailedModels
	output.AllowNonLoopbackListen = input.AllowNonLoopbackListen
	backendListenAddr, err := normalizeListenAddr(input.BackendListenAddr, DefaultBackendListenAddr, "backendListenAddr", input.AllowNonLoopbackListen)
	if err != nil {
		return Config{}, err
	}
	proxyListenAddr, err := normalizeListenAddr(input.ProxyListenAddr, DefaultProxyListenAddr, "proxyListenAddr", input.AllowNonLoopbackListen)
	if err != nil {
		return Config{}, err
	}
	output.BackendListenAddr = backendListenAddr
	output.ProxyListenAddr = proxyListenAddr
	output.HomeMetrics.IncludeCacheWriteInHitRate = input.HomeMetrics.IncludeCacheWriteInHitRate
	output.BillingQuery = input.BillingQuery
	output.LocalResponseCache = normalizeLocalResponseCache(input.LocalResponseCache)
	output.MirrorCapture = input.MirrorCapture
	output.SkillMCPScan = input.SkillMCPScan
	output.MCPTrustGrants = normalizeMCPTrustGrants(input.MCPTrustGrants)
	output.LastAgentModelHash = strings.TrimSpace(input.LastAgentModelHash)
	output.Routing.Mode = normalizeRoutingMode(input.Routing.Mode)
	if output.Routing.Mode == "" {
		output.Routing.Mode = DefaultRoutingMode
	}
	policy, err := routing.NormalizePolicy(input.Routing.Policy)
	if err != nil {
		policy = routing.DefaultPolicy()
	}
	output.Routing.Policy = policy
	output.ComputerUse.Mode = normalizeComputerUseMode(input.ComputerUse.Mode)
	output.ComputerUse.BrowserStartURL = strings.TrimSpace(input.ComputerUse.BrowserStartURL)
	if output.ComputerUse.BrowserStartURL == "" {
		output.ComputerUse.BrowserStartURL = "about:blank"
	}
	adapters, err := NormalizeModelAdapterConfigs(input.ModelAdapters)
	if err != nil {
		return Config{}, err
	}
	output.ModelAdapters = adapters
	output.Delegation, err = normalizeDelegationConfig(input.Delegation, adapters)
	if err != nil {
		return Config{}, err
	}
	return output, nil
}

func normalizeMCPTrustGrants(input []runtimeconfig.MCPTrustRecord) []runtimeconfig.MCPTrustRecord {
	if len(input) == 0 {
		return nil
	}
	unique := make(map[string]runtimeconfig.MCPTrustRecord, len(input))
	for _, grant := range input {
		grant.RuntimeScope = normalizeMCPTrustWorkspaceScope(grant.RuntimeScope)
		grant.Identifier = strings.ToLower(strings.TrimSpace(grant.Identifier))
		grant.Fingerprint = strings.ToLower(strings.TrimSpace(grant.Fingerprint))
		if grant.RuntimeScope == "" || grant.Identifier == "" || !validMCPTrustFingerprint(grant.Fingerprint) {
			continue
		}
		key := grant.RuntimeScope + "\x00" + grant.Identifier + "\x00" + grant.Fingerprint
		unique[key] = grant
	}
	result := make([]runtimeconfig.MCPTrustRecord, 0, len(unique))
	for _, grant := range unique {
		result = append(result, grant)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].RuntimeScope != result[right].RuntimeScope {
			return result[left].RuntimeScope < result[right].RuntimeScope
		}
		if result[left].Identifier != result[right].Identifier {
			return result[left].Identifier < result[right].Identifier
		}
		return result[left].Fingerprint < result[right].Fingerprint
	})
	return result
}

func normalizeMCPTrustWorkspaceScope(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "workspace:") {
		return ""
	}
	workspacePath := strings.TrimSpace(value[len("workspace:"):])
	if workspacePath == "" {
		return ""
	}
	if absolute, err := filepath.Abs(workspacePath); err == nil {
		workspacePath = absolute
	}
	workspacePath = filepath.ToSlash(filepath.Clean(workspacePath))
	if runtime.GOOS == "windows" {
		workspacePath = strings.ToLower(workspacePath)
	}
	return "workspace:" + workspacePath
}

func validMCPTrustFingerprint(value string) bool {
	const prefix = "mcp-trust-v1:sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func NormalizeModelAdapterConfigs(input []ModelAdapterConfig) ([]ModelAdapterConfig, error) {
	if len(input) == 0 {
		return []ModelAdapterConfig{}, nil
	}

	normalized := make([]ModelAdapterConfig, 0, len(input))
	channelIndexByIdentity := make(map[string]int, len(input))
	identityByAdapterID := make(map[string]string, len(input))
	explicitIDByIdentity := make(map[string]string, len(input))
	for _, item := range input {
		source := legacyruntime.NormalizeModelSource(item.Source)
		if source == "" {
			return nil, i18n.NewError("error.model_adapter.source_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 source 仅支持 third_party 或 cursor_account")
		}
		credentialScope := legacyruntime.NormalizeCredentialScope(source, item.CredentialScope)
		if credentialScope == "" {
			return nil, i18n.NewError("error.model_adapter.credential_scope_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 credentialScope 与 source 不匹配")
		}
		if source == legacyruntime.ModelSourceCursorAccount {
			if strings.TrimSpace(item.BaseURL) != "" || strings.TrimSpace(item.APIKey) != "" || item.CustomHeadersEnabled || strings.TrimSpace(item.CustomHeadersJSON) != "" {
				return nil, i18n.NewError("error.model_adapter.cursor_account_credentials", i18n.CodeInvalidModelAdapter, "Cursor 账户模型不能配置第三方接口地址、API Key 或自定义请求头")
			}
			next := ModelAdapterConfig{
				Source:              source,
				CredentialScope:     credentialScope,
				DisplayName:         strings.TrimSpace(item.DisplayName),
				GroupName:           strings.TrimSpace(item.GroupName),
				Type:                legacyruntime.ModelSourceCursorAccount,
				TooltipData:         strings.TrimSpace(item.TooltipData),
				ModelID:             strings.TrimSpace(item.ModelID),
				ContextWindowTokens: modelcontext.Resolve(item.ModelID, normalizeMaxCompletionTokens(item.ContextWindowTokens)),
				MaxCompletionTokens: normalizeMaxCompletionTokens(item.MaxCompletionTokens),
				Pricing:             item.Pricing,
			}
			if next.TooltipData == "" {
				next.TooltipData = "Cursor 账户模型"
			}
			if next.DisplayName == "" || next.ModelID == "" {
				return nil, i18n.NewError("error.model_adapter.cursor_account_identity", i18n.CodeInvalidModelAdapter, "Cursor 账户模型 displayName 和 modelID 不能为空")
			}
			identity := modelAdapterConfigIdentity(next)
			explicitID := strings.TrimSpace(item.ID)
			next.ID = explicitID
			if next.ID == "" {
				next.ID = identity
			}
			if ownerIdentity, exists := identityByAdapterID[next.ID]; exists && ownerIdentity != identity {
				return nil, i18n.NewError("error.model_adapter.id_duplicate", i18n.CodeInvalidModelAdapter, "模型适配器 id 不能被不同模型配置重复使用")
			}
			if existingIndex, exists := channelIndexByIdentity[identity]; exists {
				if explicitID != "" {
					existing := normalized[existingIndex]
					if previousExplicitID := explicitIDByIdentity[identity]; previousExplicitID != "" && previousExplicitID != explicitID {
						return nil, i18n.NewError("error.model_adapter.id_conflict", i18n.CodeInvalidModelAdapter, "相同模型连接不能配置不同的持久化 id")
					}
					if existing.ID != explicitID {
						delete(identityByAdapterID, existing.ID)
						existing.ID = explicitID
						identityByAdapterID[explicitID] = identity
						explicitIDByIdentity[identity] = explicitID
						normalized[existingIndex] = existing
					}
				}
				continue
			}
			channelIndexByIdentity[identity] = len(normalized)
			identityByAdapterID[next.ID] = identity
			if explicitID != "" {
				explicitIDByIdentity[identity] = explicitID
			}
			normalized = append(normalized, next)
			continue
		}
		baseURL, err := modelchannel.NormalizeBaseURL(item.BaseURL)
		if err != nil {
			return nil, err
		}
		nextType := normalizeModelAdapterType(item.Type)
		protocolMode := modelchannel.NormalizeProtocolMode(item.ProtocolMode)
		protocolGroup := modelchannel.ResolveProtocolGroup(protocolMode, nextType, item.ModelID, baseURL, item.OpenAIEndpoint, firstNonEmptyProtocolGroup(item.ProtocolGroup, item.OpenAIRequestGroup))
		openAIEndpoint := modelchannel.NormalizeOpenAIEndpoint(nextType, item.OpenAIEndpoint)
		if nextType == "openai" && openAIEndpoint != modelchannel.OpenAIEndpointCustom {
			openAIEndpoint = modelchannel.OpenAIEndpointForProtocolGroup(protocolGroup, openAIEndpoint)
		}
		next := ModelAdapterConfig{
			Source:          source,
			CredentialScope: credentialScope,
			DisplayName:     strings.TrimSpace(item.DisplayName),
			GroupName:       strings.TrimSpace(item.GroupName),
			Type:            nextType,
			SupplierID:      strings.TrimSpace(item.SupplierID),
			ProtocolMode:    protocolMode,
			ProtocolGroup:   protocolGroup,

			BaseURL:                   baseURL,
			APIKey:                    strings.TrimSpace(item.APIKey),
			APIKeys:                   normalizeAPIKeyPool(item.APIKeys, strings.TrimSpace(item.APIKey)),
			TooltipData:               strings.TrimSpace(item.TooltipData),
			ModelID:                   strings.TrimSpace(item.ModelID),
			ReasoningEffort:           normalizeReasoningEffort(item.ReasoningEffort),
			OpenAIEndpoint:            openAIEndpoint,
			OpenAIRequestGroup:        modelchannel.NormalizeOpenAIRequestGroup(nextType, openAIEndpoint, protocolGroup),
			ContextWindowTokens:       modelcontext.Resolve(item.ModelID, normalizeMaxCompletionTokens(item.ContextWindowTokens)),
			MaxCompletionTokens:       normalizeMaxCompletionTokens(item.MaxCompletionTokens),
			AnthropicAuthMode:         modelchannel.NormalizeAnthropicAuthMode(item.AnthropicAuthMode),
			AnthropicMaxTokens:        normalizeMaxCompletionTokens(item.AnthropicMaxTokens),
			ThinkingBudgetTokens:      normalizeMaxCompletionTokens(item.ThinkingBudgetTokens),
			Pricing:                   item.Pricing,
			FastMode:                  item.FastMode,
			OpenAIServiceTier:         strings.TrimSpace(item.OpenAIServiceTier),
			ModelCatalogURL:           strings.TrimSpace(item.ModelCatalogURL),
			BalanceQueryURL:           strings.TrimSpace(item.BalanceQueryURL),
			BalanceQueryField:         strings.TrimSpace(item.BalanceQueryField),
			BalanceQueryHeaders:       item.BalanceQueryHeaders,
			ToolCallMode:              normalizeToolCallMode(item.ToolCallMode),
			BalanceProfile:            strings.ToLower(strings.TrimSpace(item.BalanceProfile)),
			BalanceAccessToken:        strings.TrimSpace(item.BalanceAccessToken),
			BalanceUserID:             strings.TrimSpace(item.BalanceUserID),
			BalanceCodingPlanProvider: strings.ToLower(strings.TrimSpace(item.BalanceCodingPlanProvider)),
			CompatibilityKind:         strings.ToLower(strings.TrimSpace(item.CompatibilityKind)),
			Disabled:                  item.Disabled,
		}
		if next.Type == "openai" {
			next.OpenAIExtraParamsEnabled = item.OpenAIExtraParamsEnabled
			next.OpenAIExtraParamsJSON = strings.TrimSpace(item.OpenAIExtraParamsJSON)
		} else if next.Type == "anthropic" {
			next.AnthropicThinkingEffort = normalizeAnthropicThinkingEffort(item.AnthropicThinkingEffort)
			next.AnthropicExtraParamsEnabled = item.AnthropicExtraParamsEnabled
			next.AnthropicExtraParamsJSON = strings.TrimSpace(item.AnthropicExtraParamsJSON)
		}
		next.CustomHeadersEnabled = item.CustomHeadersEnabled
		next.CustomHeadersJSON = strings.TrimSpace(item.CustomHeadersJSON)
		switch {
		case next.DisplayName == "":
			return nil, i18n.NewError("error.model_adapter.display_name_required", i18n.CodeInvalidModelAdapter, "模型适配器 displayName 不能为空")
		case next.Type == "":
			return nil, i18n.NewError("error.model_adapter.type_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 type 仅支持 openai、anthropic 或 gemini")
		case next.APIKey == "":
			return nil, i18n.NewError("error.model_adapter.api_key_required", i18n.CodeInvalidModelAdapter, "模型适配器 apiKey 不能为空")
		case next.TooltipData == "":
			return nil, i18n.NewError("error.model_adapter.tooltip_required", i18n.CodeInvalidModelAdapter, "模型适配器 tooltipData 不能为空")
		case next.ModelID == "":
			return nil, i18n.NewError("error.model_adapter.model_id_required", i18n.CodeInvalidModelAdapter, "模型适配器 modelID 不能为空")
		case next.ProtocolMode == "":
			return nil, i18n.NewError("error.model_adapter.protocol_mode_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 protocolMode 仅支持 auto 或 fixed")
		case next.ProtocolGroup == "":
			return nil, i18n.NewError("error.model_adapter.protocol_group_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 protocolGroup 与 provider 不匹配")
		case next.Type == "openai" && next.ReasoningEffort == "":
			return nil, i18n.NewError("error.model_adapter.reasoning_effort_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 reasoningEffort 仅支持 low、medium、high、xhigh、max")
		case next.Type == "gemini" && next.ReasoningEffort == "":
			return nil, i18n.NewError("error.model_adapter.reasoning_effort_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 reasoningEffort 仅支持 low、medium、high、xhigh、max")
		case next.Type == "openai" && next.OpenAIEndpoint == "":
			return nil, i18n.NewError("error.model_adapter.endpoint_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 openAIEndpoint 仅支持 /v1/responses、/v1/chat/completions 或 /custom（自定义路径）")
		case next.Type == "openai" && next.OpenAIRequestGroup == "":
			return nil, i18n.NewError("error.model_adapter.request_group_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 openAIRequestGroup 仅支持 responses、chat_completions、chat_completions_compat")
		case next.Type == "openai" && next.OpenAIExtraParamsEnabled:
			if err := validateJSONMap(next.OpenAIExtraParamsJSON, "openAIExtraParamsJSON"); err != nil {
				return nil, err
			}
		case next.CustomHeadersEnabled:
			if err := validateHeadersJSON(next.CustomHeadersJSON); err != nil {
				return nil, err
			}
		case next.AnthropicAuthMode == "":
			return nil, i18n.NewError("error.model_adapter.anthropic_auth_mode_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 anthropicAuthMode 仅支持 legacy_dual、auto、x_api_key 或 bearer")
		case next.Type == "anthropic" && next.AnthropicExtraParamsEnabled:
			if err := validateJSONMap(next.AnthropicExtraParamsJSON, "anthropicExtraParamsJSON"); err != nil {
				return nil, err
			}
		case next.ToolCallMode != "" && next.ToolCallMode != modeladapter.ToolCallModeNative && next.ToolCallMode != modeladapter.ToolCallModeXMLPrompt:
			return nil, i18n.NewError("error.model_adapter.tool_call_mode_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 toolCallMode 仅支持 native 或 xml_prompt")
		case next.Type == "anthropic" && next.AnthropicThinkingEffort == "":
			return nil, i18n.NewError("error.model_adapter.thinking_effort_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 anthropicThinkingEffort 仅支持 low、medium、high、xhigh、max")
		}
		identity := modelAdapterConfigDedupeIdentity(next)
		explicitID := strings.TrimSpace(item.ID)
		next.ID = explicitID
		if next.ID == "" {
			next.ID = modelAdapterConfigStableID(next)
		}
		if ownerIdentity, exists := identityByAdapterID[next.ID]; exists && ownerIdentity != identity {
			return nil, i18n.NewError("error.model_adapter.id_duplicate", i18n.CodeInvalidModelAdapter, "模型适配器 id 不能被不同模型配置重复使用")
		}
		if existingIndex, exists := channelIndexByIdentity[identity]; exists {
			existing := normalized[existingIndex]
			if explicitID != "" {
				if previousExplicitID := explicitIDByIdentity[identity]; previousExplicitID != "" && previousExplicitID != explicitID {
					return nil, i18n.NewError("error.model_adapter.id_conflict", i18n.CodeInvalidModelAdapter, "相同模型连接不能配置不同的持久化 id")
				}
				if existing.ID != explicitID {
					delete(identityByAdapterID, existing.ID)
					existing.ID = explicitID
					identityByAdapterID[explicitID] = identity
					explicitIDByIdentity[identity] = explicitID
				}
			}
			if existing.GroupName == "" {
				existing.GroupName = next.GroupName
			}
			if existing.TooltipData == "" {
				existing.TooltipData = next.TooltipData
			}
			if existing.ContextWindowTokens <= 0 {
				existing.ContextWindowTokens = next.ContextWindowTokens
			}
			if existing.Pricing == nil {
				existing.Pricing = next.Pricing
			}
			if existing.ModelCatalogURL == "" {
				existing.ModelCatalogURL = next.ModelCatalogURL
			}
			if existing.SupplierID == "" {
				existing.SupplierID = next.SupplierID
			}
			if existing.BalanceQueryURL == "" {
				existing.BalanceQueryURL = next.BalanceQueryURL
			}
			if existing.BalanceQueryField == "" {
				existing.BalanceQueryField = next.BalanceQueryField
			}
			if len(existing.BalanceQueryHeaders) == 0 {
				existing.BalanceQueryHeaders = next.BalanceQueryHeaders
			}
			if existing.BalanceProfile == "" {
				existing.BalanceProfile = next.BalanceProfile
			}
			if existing.BalanceAccessToken == "" {
				existing.BalanceAccessToken = next.BalanceAccessToken
			}
			if existing.BalanceUserID == "" {
				existing.BalanceUserID = next.BalanceUserID
			}
			if existing.BalanceCodingPlanProvider == "" {
				existing.BalanceCodingPlanProvider = next.BalanceCodingPlanProvider
			}
			if existing.CompatibilityKind == "" {
				existing.CompatibilityKind = next.CompatibilityKind
			}
			if len(existing.APIKeys) == 0 {
				existing.APIKeys = next.APIKeys
			}
			normalized[existingIndex] = existing
			continue
		}
		channelIndexByIdentity[identity] = len(normalized)
		identityByAdapterID[next.ID] = identity
		if explicitID != "" {
			explicitIDByIdentity[identity] = explicitID
		}
		normalized = append(normalized, next)
	}
	return normalized, nil
}

func modelAdapterConfigStableID(adapter ModelAdapterConfig) string {
	channelID := modelchannel.BuildSourcedChannelID(
		adapter.Source,
		adapter.BaseURL,
		adapter.ModelID,
		adapter.APIKey,
		adapter.DisplayName,
		adapter.OpenAIEndpoint,
	) + "\n" + strings.TrimSpace(adapter.GroupName)
	if mode := modelchannel.NormalizeAnthropicAuthMode(adapter.AnthropicAuthMode); mode != "" && mode != modelchannel.AnthropicAuthModeLegacyDual {
		return channelID + "-auth-" + mode
	}
	return channelID
}

// modelAdapterConfigIdentity 是账户模型（cursor_account）流程的渠道身份：
// 不含协议族等第三方专属字段，仅由来源、身份字段与分组构成，用于拒绝
// 不同来源或不同连接复用同一渠道 ID。
func modelAdapterConfigIdentity(adapter ModelAdapterConfig) string {
	channelID := modelchannel.BuildSourcedChannelID(
		adapter.Source,
		adapter.BaseURL,
		adapter.ModelID,
		adapter.APIKey,
		adapter.DisplayName,
		adapter.OpenAIEndpoint,
	)
	return channelID + "\n" + strings.TrimSpace(adapter.GroupName)
}

func modelAdapterConfigDedupeIdentity(adapter ModelAdapterConfig) string {
	return strings.Join([]string{
		modelAdapterConfigStableID(adapter),
		strings.TrimSpace(adapter.GroupName),
		strings.TrimSpace(adapter.ProtocolMode),
		strings.TrimSpace(adapter.ProtocolGroup),
		strings.TrimSpace(adapter.OpenAIRequestGroup),
		strings.TrimSpace(adapter.AnthropicAuthMode),
		strconv.FormatBool(adapter.CustomHeadersEnabled),
		strings.TrimSpace(adapter.ToolCallMode),
		strings.TrimSpace(adapter.CompatibilityKind),
		strings.TrimSpace(adapter.CustomHeadersJSON),
		// 密钥池参与去重：同一连接不同备用密钥组合是不同的渠道配置，
		// 但不进 StableID，避免增删密钥导致既有渠道 ID 与测试记录漂移。
		strings.Join(adapter.APIKeys, "\x1f"),
	}, "\n")
}

// normalizeAPIKeyPool 规范化备用密钥池：去空白、去空串、去重（含与主 apiKey 重复项）。
func normalizeAPIKeyPool(values []string, primaryKey string) []string {
	if len(values) == 0 {
		return nil
	}
	pool := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values)+1)
	if primaryKey != "" {
		seen[primaryKey] = struct{}{}
	}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		pool = append(pool, trimmed)
	}
	if len(pool) == 0 {
		return nil
	}
	return pool
}

// normalizeToolCallMode 规范化工具调用协议模式：大小写不敏感；空值保持空
// （等价 native 默认）；非法值返回原样小写，由 NormalizeModelAdapterConfigs
// 的校验分支报错，避免拼写错误被静默吞掉。
func normalizeToolCallMode(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmptyProtocolGroup(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func validateJSONMap(value string, fieldName string) error {
	text := strings.TrimSpace(value)
	if text == "" {
		return i18n.NewError("error.model_adapter.json_required", i18n.CodeInvalidModelAdapter, fmt.Sprintf("模型适配器 %s 不能为空", fieldName))
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return i18n.NewError("error.model_adapter.json_required", i18n.CodeInvalidModelAdapter, fmt.Sprintf("模型适配器 %s 必须是合法 JSON 对象", fieldName))
	}
	if parsed == nil {
		return i18n.NewError("error.model_adapter.json_required", i18n.CodeInvalidModelAdapter, fmt.Sprintf("模型适配器 %s 必须是 JSON 对象", fieldName))
	}
	return nil
}

func validateHeadersJSON(value string) error {
	text := strings.TrimSpace(value)
	if err := validateJSONMap(text, "customHeadersJSON"); err != nil {
		return err
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return i18n.NewError("error.model_adapter.headers_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 customHeadersJSON 的值必须是字符串")
	}
	for key := range parsed {
		if strings.TrimSpace(key) == "" {
			return i18n.NewError("error.model_adapter.headers_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 customHeadersJSON 的请求头名称不能为空")
		}
	}
	return nil
}

func normalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "medium":
		return "medium"
	case "low", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeAnthropicThinkingEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "xhigh":
		return "xhigh"
	case "low", "medium", "high", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeListenAddr(value string, defaultValue string, fieldName string, allowNonLoopback bool) (string, error) {
	addr := strings.TrimSpace(value)
	if addr == "" {
		addr = defaultValue
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%s 必须是 host:port 格式", fieldName)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("%s host 不能为空", fieldName)
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return "", fmt.Errorf("%s port 必须在 1-65535 之间", fieldName)
	}
	if !allowNonLoopback && !isLoopbackListenHost(host) {
		return "", fmt.Errorf("%s 仅允许环回地址（127.0.0.1 或 ::1）；绑定非环回地址需设置 allowNonLoopbackListen: true", fieldName)
	}
	return net.JoinHostPort(host, strconv.Itoa(parsedPort)), nil
}

func isLoopbackListenHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost":
		return true
	default:
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
}

// normalizeLocalResponseCache 归一化本地响应缓存配置：默认关闭，只有用户显式
// 启用时才改变 provider 请求路径；TTL/MaxEntries 仍保留可用的默认值。
func normalizeLocalResponseCache(input LocalResponseCacheConfig) LocalResponseCacheConfig {
	output := input
	if output.TTLSeconds <= 0 {
		output.TTLSeconds = DefaultLocalResponseCacheTTLSeconds
	}
	if output.MaxEntries <= 0 {
		output.MaxEntries = DefaultLocalResponseCacheMaxEntries
	}
	return output
}

func normalizeProviderStreamIdleTimeout(value int) int {
	if value <= 0 {
		return DefaultProviderStreamIdleTimeoutSeconds
	}
	if value < MinProviderStreamIdleTimeoutSeconds {
		return MinProviderStreamIdleTimeoutSeconds
	}
	return value
}

// normalizeTurnStaleTimeout 把 turn-staleness 看门狗阈值约束到合法区间（单位秒）。
// <=0 回退默认值；过小回退最小值，避免过短阈值把正常的长工具调用误判为卡死。
func normalizeTurnStaleTimeout(value int) int {
	if value <= 0 {
		return DefaultTurnStaleTimeoutSeconds
	}
	if value < MinTurnStaleTimeoutSeconds {
		return MinTurnStaleTimeoutSeconds
	}
	return value
}

// normalizeNativeDelegationProgressTimeout 把 native 子代理「无有效进展」看门狗阈值
// 约束到合法区间（单位秒）：<=0 回退默认值，过小回退最小值，避免正常的长任务被误判超时。
func normalizeNativeDelegationProgressTimeout(value int) int {
	if value <= 0 {
		return DefaultNativeDelegationProgressTimeoutSeconds
	}
	if value < MinNativeDelegationProgressTimeoutSeconds {
		return MinNativeDelegationProgressTimeoutSeconds
	}
	return value
}

// normalizeAutoMatchContextWindow 把「自动配对上下文窗口」开关归一为显式 bool。
// YAML 中未出现该键时，Load 得到的字段为零值 false；但语义上「未配置」应回退到默认开启，
// 因此这里无法仅凭 false 区分「显式关闭」与「未配置」。当前实现：直接透传用户值；
// 默认开启由 DefaultConfig 保证（仅全新配置文件会落到默认 true）。
func normalizeAutoMatchContextWindow(value bool) bool {
	return value
}

func normalizeMaxCompletionTokens(value int) int {
	if value <= 0 {
		return 0
	}
	return value
}

func normalizeRoutingMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "local":
		return "local"
	case "upstream":
		return "upstream"
	default:
		return ""
	}
}

// normalizeComputerUseMode 归一化 ComputerUse 执行模式：非法值回退 desktop。
func normalizeComputerUseMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "browser":
		return "browser"
	default:
		return "desktop"
	}
}

func normalizeModelAdapterType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "openai":
		return "openai"
	case "anthropic":
		return "anthropic"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}
