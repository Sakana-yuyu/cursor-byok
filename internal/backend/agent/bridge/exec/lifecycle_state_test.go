package execbridge

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestClassifyExecLifecycleKeepsAwaitStillRunningOpen(t *testing.T) {
	state := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_SubagentAwaitResult{
			SubagentAwaitResult: &agentv1.SubagentAwaitResult{
				Result: &agentv1.SubagentAwaitResult_StillRunning{
					StillRunning: &agentv1.SubagentAwaitStillRunning{},
				},
			},
		},
	}, runtimecore.PendingExec{ExecKind: "subagent_await"})
	if state.Phase != "await_still_running" || state.Terminal {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestClassifyExecLifecycleClosesAwaitComplete(t *testing.T) {
	state := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_SubagentAwaitResult{
			SubagentAwaitResult: &agentv1.SubagentAwaitResult{
				Result: &agentv1.SubagentAwaitResult_Complete{
					Complete: &agentv1.SubagentAwaitComplete{},
				},
			},
		},
	}, runtimecore.PendingExec{ExecKind: "subagent_await"})
	if state.Phase != "await_complete" || !state.Terminal {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestClassifyExecLifecycleProjectsBackgroundStatus(t *testing.T) {
	accepted := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_ForceBackgroundSubagentResult{
			ForceBackgroundSubagentResult: &agentv1.ForceBackgroundSubagentResult{
				Status: agentv1.ForceBackgroundSubagentStatus_FORCE_BACKGROUND_SUBAGENT_STATUS_ACCEPTED,
			},
		},
	}, runtimecore.PendingExec{ExecKind: "force_background_subagent"})
	if accepted.Phase != "background_accepted" || !accepted.Terminal {
		t.Fatalf("accepted state: %#v", accepted)
	}

	notFound := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_ForceBackgroundSubagentResult{
			ForceBackgroundSubagentResult: &agentv1.ForceBackgroundSubagentResult{
				Status: agentv1.ForceBackgroundSubagentStatus_FORCE_BACKGROUND_SUBAGENT_STATUS_NOT_FOUND,
			},
		},
	}, runtimecore.PendingExec{ExecKind: "force_background_subagent"})
	if notFound.Phase != "background_not_found" || !notFound.Terminal {
		t.Fatalf("not found state: %#v", notFound)
	}
}

func TestClassifyExecLifecycleProjectsAllowlistDecision(t *testing.T) {
	allowed := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_ShellAllowlistPrecheckResult{
			ShellAllowlistPrecheckResult: &agentv1.ShellAllowlistPrecheckResult{Allowlisted: true},
		},
	}, runtimecore.PendingExec{ExecKind: "shell"})
	if allowed.Phase != "allowlist_allowed" || allowed.Terminal {
		t.Fatalf("allowed state: %#v", allowed)
	}

	denied := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_McpAllowlistPrecheckResult{
			McpAllowlistPrecheckResult: &agentv1.McpAllowlistPrecheckResult{Allowlisted: false},
		},
	}, runtimecore.PendingExec{ExecKind: "mcp"})
	if denied.Phase != "allowlist_denied" || !denied.Terminal {
		t.Fatalf("denied state: %#v", denied)
	}
}

func TestClassifyExecLifecycleKeepsObservedUnknownResultsTerminal(t *testing.T) {
	background := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_ForceBackgroundSubagentResult{
			ForceBackgroundSubagentResult: &agentv1.ForceBackgroundSubagentResult{},
		},
	}, runtimecore.PendingExec{ExecKind: "force_background_subagent"})
	if background.Phase != "not_observed" || !background.Terminal {
		t.Fatalf("background state: %#v", background)
	}

	await := ClassifyExecLifecycle(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_SubagentAwaitResult{
			SubagentAwaitResult: &agentv1.SubagentAwaitResult{},
		},
	}, runtimecore.PendingExec{ExecKind: "subagent_await"})
	if await.Phase != "not_observed" || !await.Terminal {
		t.Fatalf("await state: %#v", await)
	}
}
