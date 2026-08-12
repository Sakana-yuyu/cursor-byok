package execbridge

import (
	"strings"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// LifecycleState 是仅供状态投影使用的最小执行生命周期快照。
// 它不包含执行标识、参数、错误正文或任何转录内容。
type LifecycleState struct {
	Kind     string
	Phase    string
	Terminal bool
}

// ClassifyExecLifecycle 将已知的子代理和 allowlist oneof 映射为稳定状态。
// 未观察到或未知分支保持非终态，交由既有执行桥和 watchdog 继续收口。
func ClassifyExecLifecycle(msg *agentv1.ExecClientMessage, pending runtimecore.PendingExec) LifecycleState {
	state := LifecycleState{Kind: strings.TrimSpace(pending.ExecKind), Phase: "not_observed"}
	if msg == nil {
		return state
	}

	if precheck := msg.GetShellAllowlistPrecheckResult(); precheck != nil {
		return allowlistLifecycleState(state.Kind, precheck.GetAllowlisted())
	}
	if precheck := msg.GetMcpAllowlistPrecheckResult(); precheck != nil {
		return allowlistLifecycleState(state.Kind, precheck.GetAllowlisted())
	}
	if precheck := msg.GetWebFetchAllowlistPrecheckResult(); precheck != nil {
		return allowlistLifecycleState(state.Kind, precheck.GetAllowlisted())
	}

	switch strings.TrimSpace(pending.ExecKind) {
	case "force_background_subagent":
		result := msg.GetForceBackgroundSubagentResult()
		if result == nil {
			return state
		}
		switch result.GetStatus() {
		case agentv1.ForceBackgroundSubagentStatus_FORCE_BACKGROUND_SUBAGENT_STATUS_ACCEPTED:
			return LifecycleState{Kind: state.Kind, Phase: "background_accepted", Terminal: true}
		case agentv1.ForceBackgroundSubagentStatus_FORCE_BACKGROUND_SUBAGENT_STATUS_NOT_FOUND:
			return LifecycleState{Kind: state.Kind, Phase: "background_not_found", Terminal: true}
		default:
			return LifecycleState{Kind: state.Kind, Phase: "not_observed", Terminal: true}
		}
	case "subagent_await":
		switch result := msg.GetSubagentAwaitResult(); {
		case result == nil:
			return state
		case result.GetStillRunning() != nil:
			return LifecycleState{Kind: state.Kind, Phase: "await_still_running"}
		case result.GetComplete() != nil:
			return LifecycleState{Kind: state.Kind, Phase: "await_complete", Terminal: true}
		case result.GetNotFound() != nil:
			return LifecycleState{Kind: state.Kind, Phase: "await_not_found", Terminal: true}
		case result.GetError() != nil:
			return LifecycleState{Kind: state.Kind, Phase: "await_error", Terminal: true}
		default:
			return LifecycleState{Kind: state.Kind, Phase: "not_observed", Terminal: true}
		}
	}
	return state
}

func allowlistLifecycleState(kind string, allowlisted bool) LifecycleState {
	if allowlisted {
		return LifecycleState{Kind: kind, Phase: "allowlist_allowed"}
	}
	return LifecycleState{Kind: kind, Phase: "allowlist_denied", Terminal: true}
}
