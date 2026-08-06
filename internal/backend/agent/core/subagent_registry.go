// subagent_registry.go 提供轻量子代理注册表（C1：对齐 opencode 声明式 Agent.Info）。
// 注册表条目把「角色约束」从散落的字符串分支收敛为按 subagent_type 查表；
// 缺省类型（未命中）完全走现有逻辑，保持向后兼容。当前为代码声明式内置，
// 后续可扩展为配置驱动（config 合并优先级：显式配置 > 内置）。
package runtimecore

import "strings"

// SubagentProfile 描述子代理注册表条目。
type SubagentProfile struct {
	// PromptFragment 附加到 Task prompt 之后的角色/约束片段；空表示不注入。
	PromptFragment string
	// DefaultModelID 可选模型覆盖：命中注册表且运行期 overrides 未覆盖时作为兜底。
	// 预留：当前模型覆盖仍走 LookupSubagentModelOverride，未强制此字段。
	DefaultModelID string
	// MaxSteps 可选最大步数上限（0 = 不限制）。预留：当前未强制。
	MaxSteps int
	// ToolWhitelist 可选工具白名单（空 = 不限制）。预留：当前未强制。
	ToolWhitelist []string
}

// builtinSubagentProfiles 内置注册表。键名对齐 LookupSubagentModelOverride 的别名体系
// （browserUse↔browser-use 通过别名命中；generalPurpose 与 explore 各自独立注册，
// 由于两者互为别名键，LookupSubagentProfile 总是先精确命中自身，不会交叉注入）。
var builtinSubagentProfiles = map[string]SubagentProfile{
	"explore": {
		PromptFragment: "你是代码库快速探索助手：优先用 Grep/Glob/Read 精确定位，避免无谓的大范围列举；只报告与任务直接相关的发现，不修改任何文件。",
	},
	"generalPurpose": {
		PromptFragment: "你是通用编码助手，独立完成委派任务：先梳理问题与验收点再动手，遵循仓库既有约定，完成后用简洁中文汇报结果与验证方式。",
	},
	"browserUse": {
		PromptFragment: "你是浏览器自动化助手：优先通过截图观察页面当前状态，验证行为时一次只改一个变量，失败时先查看页面再决定下一步。",
	},
}

// LookupSubagentProfile 按 subagent_type 查找注册表条目（含别名兼容，同 LookupSubagentModelOverride）。
func LookupSubagentProfile(subagentType string) (SubagentProfile, bool) {
	trimmed := strings.TrimSpace(subagentType)
	if trimmed == "" {
		return SubagentProfile{}, false
	}
	for _, key := range subagentModelOverrideLookupKeys(trimmed) {
		if profile, ok := builtinSubagentProfiles[key]; ok {
			return profile, true
		}
	}
	return SubagentProfile{}, false
}

// ApplySubagentPromptFragment 把角色片段拼接到 Task prompt 之后。
// overrides 为运行时子代理角色覆盖（subagentType → 片段，来自委派配置）：
// 覆盖优先于内置注册表；覆盖片段为空串表示对该类型禁用注入（区别于未配置）。
// 无片段或未命中时原样返回（向后兼容）。
func ApplySubagentPromptFragment(subagentType string, prompt string, overrides map[string]string) string {
	trimmedType := strings.TrimSpace(subagentType)
	fragment := ""
	if overrideFragment, configured := overrides[trimmedType]; configured {
		fragment = strings.TrimSpace(overrideFragment)
	} else if profile, ok := LookupSubagentProfile(trimmedType); ok {
		fragment = strings.TrimSpace(profile.PromptFragment)
	}
	if fragment == "" {
		return prompt
	}
	base := strings.TrimSpace(prompt)
	if base == "" {
		return fragment
	}
	return base + "\n\n" + fragment
}