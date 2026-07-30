// asset_enrichment.go 在写历史前，把磁盘扫描到的技能/MCP server 合并进 intent.RequestContext。
//
// 这是 BYOK 还原原生用法的核心接入点：原生 Cursor 由客户端在 RequestContext 里带上
// SkillOptions / McpFileSystemOptions；BYOK 客户端往往不填，导致 <agent_skills> 和
// <mcp_file_system> 为空、模型无从得知可用技能/MCP。本函数用磁盘扫描结果补齐，
// 复用现有 request_context → projector → engine.go 的原生 user-message 注入链路，
// 不在系统提示里另开第二条注入路径（避免与 user-message 注入分叉、破坏 prefix-cache）。
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

// enrichRequestContextWithScannedAssets 把扫描到的技能/MCP descriptor 合并进 intent.RequestContext。
//
// 合并语义（不覆盖客户端已发内容）：
//   - Skills：转成 SkillDescriptor，按 ReadmeFilePath 去重后追加进 SkillOptions.SkillDescriptors。
//   - MCP：追加进 McpFileSystemOptions.McpDescriptors，按 ServerIdentifier 去重；并标记 Enabled。
//
// 幂等：扫描结果有 mtime 缓存，重复调用代价低；非 turn 1 时由 normalizeRealtimeRequestContextForStorage
// 自动丢弃静态部分，因此无需在此判断 turnSeq。
func (service *Service) enrichRequestContextWithScannedAssets(intent *InboundIntent) {
	if service == nil || intent == nil {
		return
	}
	settings := readSkillMCPScanSettings(service.scanConfig)
	workspaceRoot := resolveWorkspaceRootFromIntent(intent)

	// Keep the compiler-side sparse skill store synchronized even when scanning is disabled.
	if service.skillsStore() != nil {
		service.skillsStore().SetWorkspaceRoot(workspaceRoot)
		service.skillsStore().SetScanSettings(settings.Enabled, settings.SkillSources, settings.DisabledSkills)
	}
	if !settings.Enabled {
		if service.mcpRuntime != nil {
			SyncMCPRuntimeForWorkspace(service.mcpRuntime, workspaceRoot, nil)
		}
		return
	}

	skills := ScanAllSkills(workspaceRoot)
	mcpConfigs := ScanMCPServerConfigs(workspaceRoot, settings)
	if service.mcpRuntime != nil {
		SyncMCPRuntimeForWorkspace(service.mcpRuntime, workspaceRoot, enabledMCPServerConfigs(mcpConfigs))
	}
	mcpServers := mcpDescriptorsWithRuntime(mcpConfigs, service.mcpRuntime)

	// 应用配置过滤：按分类来源 + 逐项禁用。
	skills = filterScannedSkills(skills, settings)

	if len(skills) == 0 && len(mcpServers) == 0 {
		return
	}

	if intent.RequestContext == nil {
		intent.RequestContext = &agentv1.RequestContext{}
	}
	rc := intent.RequestContext

	if len(skills) > 0 {
		mergeScannedSkillDescriptors(rc, skills)
	}
	if len(mcpServers) > 0 {
		mergeScannedMCPDescriptors(rc, mcpServers, workspaceRoot)
		// 捕获点：MCP schema 缺失。扫描注入的 descriptor 不含 tool schema（磁盘配置无 input_schema），
		// 模型仅知 server 名、不知具体工具/参数 -> 调用易失败。记录便于后续针对性补 schema。
		service.captureMCPSchemaGap(intent.RequestID, intent.ConversationID, mcpServers)
	}
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
	env := intent.RequestContext.GetEnv()
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

// mergeScannedSkillDescriptors 把扫描技能追加进 SkillOptions，按 ReadmeFilePath 去重。
func mergeScannedSkillDescriptors(rc *agentv1.RequestContext, skills []SourcedGlobalSkill) {
	existing := make(map[string]struct{}, len(rc.GetSkillOptions().GetSkillDescriptors()))
	for _, d := range rc.GetSkillOptions().GetSkillDescriptors() {
		if d == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(d.GetReadmeFilePath()))
		if key != "" {
			existing[key] = struct{}{}
		}
	}
	appended := make([]*agentv1.SkillDescriptor, 0, len(skills))
	for _, sk := range skills {
		fullPath := strings.TrimSpace(sk.FullPath)
		if fullPath == "" {
			continue
		}
		key := strings.ToLower(fullPath)
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		appended = append(appended, &agentv1.SkillDescriptor{
			Name:           strings.TrimSpace(sk.Name),
			Description:    strings.TrimSpace(sk.Description),
			ReadmeFilePath: fullPath,
			Enabled:        true,
		})
	}
	if len(appended) == 0 {
		return
	}
	if rc.SkillOptions == nil {
		rc.SkillOptions = &agentv1.SkillOptions{}
	}
	rc.SkillOptions.SkillDescriptors = append(rc.SkillOptions.SkillDescriptors, appended...)
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
// 用于在 enrich 时同步 workspaceRoot。取不到时返回 nil，不影响扫描本身。
func (service *Service) skillsStore() *SkillStore {
	if service == nil {
		return nil
	}
	if dc, ok := service.compiler.(*DefaultPromptCompiler); ok && dc != nil {
		return dc.skills
	}
	return nil
}
