// asset_enrichment.go 在写历史前，把磁盘扫描到的 MCP server 合并进 intent.RequestContext。
//
// 扫描技能只走编译器的稀疏激活系统提示路径；客户端显式传入的 SkillOptions 保留原生
// request_context 回放。MCP 仍复用 request_context → projector → engine.go 注入链路。
package forwarder

import (
	"strings"

	"cursor/gen/agentv1"
)

// skillMCPScanConfigProvider 提供 Skills/MCP 扫描配置（总开关 + 按分类/逐项禁用）。
// 由 *serverconfig.Manager 实现（在 NewService 中通过类型断言注入）。
// 用原始类型字段避免 forwarder ↔ serverconfig 循环依赖。
type skillMCPScanConfigProvider interface {
	SkillMCPScanEnabled() bool
	SkillMCPScanSkillSources() map[string]bool
	SkillMCPScanMCPSources() map[string]bool
	SkillMCPScanDisabledSkills() map[string]bool
	SkillMCPScanDisabledMCPServers() map[string]bool
}

// SkillMCPScanSettings 是 forwarder 依赖的扫描配置快照。
type SkillMCPScanSettings struct {
	Enabled            bool
	SkillSources       map[string]bool
	MCPSources         map[string]bool
	DisabledSkills     map[string]bool
	DisabledMCPServers map[string]bool
}

// readSkillMCPScanSettings 从 provider 读取配置快照。
func readSkillMCPScanSettings(provider skillMCPScanConfigProvider) SkillMCPScanSettings {
	if provider == nil {
		return SkillMCPScanSettings{Enabled: true}
	}
	return SkillMCPScanSettings{
		Enabled:            provider.SkillMCPScanEnabled(),
		SkillSources:       provider.SkillMCPScanSkillSources(),
		MCPSources:         provider.SkillMCPScanMCPSources(),
		DisabledSkills:     provider.SkillMCPScanDisabledSkills(),
		DisabledMCPServers: provider.SkillMCPScanDisabledMCPServers(),
	}
}

// enrichRequestContextWithScannedAssets 同步技能扫描设置并合并扫描到的 MCP descriptor。
//
// 合并语义（不覆盖客户端已发内容）：
//   - MCP：追加进 McpFileSystemOptions.McpDescriptors，按 ServerIdentifier 去重；并标记 Enabled。
//
// 幂等：扫描结果有 mtime 缓存，重复调用代价低；非 turn 1 时由 normalizeRealtimeRequestContextForStorage
// 丢弃 model-visible 静态资产，仅保留 workspace scope marker，因此无需在此判断 turnSeq。
func (service *Service) enrichRequestContextWithScannedAssets(intent *InboundIntent) {
	if service == nil || intent == nil {
		return
	}
	settings := readSkillMCPScanSettings(service.scanConfig)
	workspaceRoot := resolveWorkspaceRootFromIntent(intent)
	preserveRequestContextWorkspaceRoot(intent, workspaceRoot)

	// Keep compiler-side scan settings synchronized even when scanning is disabled.
	if service.skillsStore() != nil {
		service.skillsStore().SetScanSettings(settings.Enabled, settings.SkillSources, settings.DisabledSkills)
	}
	if !settings.Enabled {
		if service.mcpRuntime != nil {
			SyncMCPRuntimeForWorkspace(service.mcpRuntime, workspaceRoot, nil)
		}
		return
	}

	mcpConfigs := ScanMCPServerConfigs(workspaceRoot, settings)
	if service.mcpRuntime != nil {
		SyncMCPRuntimeForWorkspace(service.mcpRuntime, workspaceRoot, enabledMCPServerConfigs(mcpConfigs))
	}
	mcpServers := mcpDescriptorsWithRuntime(mcpConfigs, service.mcpRuntime)

	if len(mcpServers) == 0 {
		return
	}

	if intent.RequestContext == nil {
		intent.RequestContext = &agentv1.RequestContext{}
	}
	rc := intent.RequestContext

	mergeScannedMCPDescriptors(rc, mcpServers, workspaceRoot)
	// 捕获点：MCP schema 缺失。扫描注入的 descriptor 不含 tool schema（磁盘配置无 input_schema），
	// 模型仅知 server 名、不知具体工具/参数 -> 调用易失败。记录便于后续针对性补 schema。
	service.captureMCPSchemaGap(intent.RequestID, intent.ConversationID, mcpServers)
}

func preserveRequestContextWorkspaceRoot(intent *InboundIntent, workspaceRoot string) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if intent == nil || workspaceRoot == "" {
		return
	}
	if intent.RequestContext == nil {
		intent.RequestContext = &agentv1.RequestContext{}
	}
	if intent.RequestContext.McpFileSystemOptions == nil {
		intent.RequestContext.McpFileSystemOptions = &agentv1.McpFileSystemOptions{}
	}
	intent.RequestContext.McpFileSystemOptions.WorkspaceProjectDir = workspaceRoot
}

// filterScannedSkills 按分类来源开关与逐项禁用列表过滤技能。
func filterScannedSkills(skills []SourcedGlobalSkill, settings SkillMCPScanSettings) []SourcedGlobalSkill {
	if len(skills) == 0 {
		return skills
	}
	out := make([]SourcedGlobalSkill, 0, len(skills))
	for _, sk := range skills {
		if !sourceEnabled(settings.SkillSources, string(sk.Source)) {
			continue
		}
		if settings.DisabledSkills != nil && settings.DisabledSkills[strings.ToLower(strings.TrimSpace(sk.Name))] {
			continue
		}
		out = append(out, sk)
	}
	return out
}

// sourceEnabled 判断某分类来源是否启用。nil map 表示全部启用。
func sourceEnabled(sources map[string]bool, source string) bool {
	if sources == nil {
		return true
	}
	enabled, ok := sources[strings.ToLower(strings.TrimSpace(source))]
	if !ok {
		return true // 未配置的分类默认启用
	}
	return enabled
}

// resolveWorkspaceRootFromIntent 从 intent 的 RequestContext.Env 推导工作区根目录。
// 优先 ProjectFolder，其次第一个 WorkspacePaths，缺省回退空（仅扫用户级目录）。
func resolveWorkspaceRootFromIntent(intent *InboundIntent) string {
	if intent == nil || intent.RequestContext == nil {
		return ""
	}
	if workspaceRoot := resolveWorkspaceRootFromEnv(intent.RequestContext.GetEnv()); workspaceRoot != "" {
		return workspaceRoot
	}
	return resolveWorkspaceRootFromRequestContext(intent.RequestContext)
}

func resolveWorkspaceRootFromRequestContext(requestContext *agentv1.RequestContext) string {
	if requestContext == nil {
		return ""
	}
	if workspaceRoot := strings.TrimSpace(requestContext.GetMcpFileSystemOptions().GetWorkspaceProjectDir()); workspaceRoot != "" {
		return workspaceRoot
	}
	return resolveWorkspaceRootFromEnv(requestContext.GetEnv())
}

func resolveWorkspaceRootFromEnv(env *agentv1.RequestContextEnv) string {
	if env == nil {
		return ""
	}
	if folder := strings.TrimSpace(env.GetProjectFolder()); folder != "" {
		return folder
	}
	for _, p := range env.GetWorkspacePaths() {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// mergeScannedMCPDescriptors 把扫描到的 MCP server 追加进 McpFileSystemOptions，
// 按 ServerIdentifier 去重，并标记 Enabled 与 WorkspaceProjectDir（供 engine.go 渲染 mcpRoot）。
func mergeScannedMCPDescriptors(rc *agentv1.RequestContext, servers []*agentv1.McpDescriptor, workspaceRoot string) {
	existing := make(map[string]struct{}, len(rc.GetMcpFileSystemOptions().GetMcpDescriptors()))
	for _, d := range rc.GetMcpFileSystemOptions().GetMcpDescriptors() {
		if d == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(d.GetServerIdentifier()))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(d.GetServerName()))
		}
		if key != "" {
			existing[key] = struct{}{}
		}
	}
	appended := make([]*agentv1.McpDescriptor, 0, len(servers))
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(srv.GetServerIdentifier()))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(srv.GetServerName()))
		}
		if key == "" {
			continue
		}
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		appended = append(appended, srv)
	}
	if len(appended) == 0 {
		return
	}
	if rc.McpFileSystemOptions == nil {
		rc.McpFileSystemOptions = &agentv1.McpFileSystemOptions{}
	}
	rc.McpFileSystemOptions.Enabled = true
	if workspaceRoot != "" && strings.TrimSpace(rc.McpFileSystemOptions.WorkspaceProjectDir) == "" {
		rc.McpFileSystemOptions.WorkspaceProjectDir = workspaceRoot
	}
	rc.McpFileSystemOptions.McpDescriptors = append(rc.McpFileSystemOptions.McpDescriptors, appended...)
}

// skillsStore 返回 Service 持有的 SkillStore（若编译器是 DefaultPromptCompiler 则可取到）。
// 用于在 enrich 时同步扫描设置。取不到时返回 nil，不影响扫描本身。
func (service *Service) skillsStore() *SkillStore {
	if service == nil {
		return nil
	}
	if dc, ok := service.compiler.(*DefaultPromptCompiler); ok && dc != nil {
		return dc.skills
	}
	return nil
}
