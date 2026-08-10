package forwarder

import (
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

// TestDeferNativeSubagentExecWatchdogSemantics 验证 exec 看门狗对 native 子代理的延期决策：
// 仅当运行态仍在且延期次数未达上限时返回 true 并原子递增；无运行态、已终态或已达上限均返回 false。
func TestDeferNativeSubagentExecWatchdogSemantics(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	now := time.Now().UTC()

	execs := map[string]*nativeDelegationRuntime{
		"running": {
			ID:                      "running",
			Status:                  delegation.TaskRunning,
			LastEffectiveProgressAt: now.Add(-30 * time.Minute),
		},
		"completed": {
			ID:                      "completed",
			Status:                  delegation.TaskCompleted,
			LastEffectiveProgressAt: now,
		},
	}

	t.Run("no runtime returns false", func(t *testing.T) {
		service.nativeDelegations = map[string]*nativeDelegationRuntime{}
		if service.deferNativeSubagentExecWatchdog("missing-exec") {
			t.Fatal("deferNativeSubagentExecWatchdog() = true for missing runtime, want false")
		}
	})

	t.Run("terminal runtime returns false", func(t *testing.T) {
		service.nativeDelegations = map[string]*nativeDelegationRuntime{"completed": execs["completed"]}
		if service.deferNativeSubagentExecWatchdog("completed") {
			t.Fatal("deferNativeSubagentExecWatchdog() = true for terminal runtime, want false")
		}
	})

	t.Run("first fire defers and increments", func(t *testing.T) {
		service.nativeDelegations = map[string]*nativeDelegationRuntime{"running": execs["running"]}
		if !service.deferNativeSubagentExecWatchdog("running") {
			t.Fatal("deferNativeSubagentExecWatchdog() = false on first fire, want true")
		}
		item, ok := service.nativeDelegationTask("running")
		if !ok {
			t.Fatal("native runtime missing after deferral")
		}
		if item.ExecWatchdogDeferrals != 1 {
			t.Fatalf("ExecWatchdogDeferrals = %d, want 1", item.ExecWatchdogDeferrals)
		}
		if item.Status != delegation.TaskRunning {
			t.Fatalf("status = %q, want running (deferral must not terminalize)", item.Status)
		}
	})

	t.Run("second fire at cap returns false", func(t *testing.T) {
		if service.deferNativeSubagentExecWatchdog("running") {
			t.Fatal("deferNativeSubagentExecWatchdog() = true at deferral cap, want false")
		}
		item, ok := service.nativeDelegationTask("running")
		if !ok {
			t.Fatal("native runtime missing")
		}
		if item.ExecWatchdogDeferrals != 1 {
			t.Fatalf("ExecWatchdogDeferrals = %d, want 1 (cap must not over-increment)", item.ExecWatchdogDeferrals)
		}
	})
}

// TestRecoverStaleExecWithoutTerminalDefersNativeSubagent 验证 exec 看门狗到期时，
// 对仍在客户端运行的 native 子代理先延期而非强杀：exec 保持 pending、运行态保持 running、
// 延期计数 +1；直到达到延期上限才按超时收口。
func TestRecoverStaleExecWithoutTerminalDefersNativeSubagent(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation(nil)
	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "delegate work"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}, broker)
	stream, err := broker.OpenStream(
		"request-1",
		persisted.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"delegate work",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)

	execID := "exec-subagent-defer"
	toolCallID := "call-defer"
	stream.mu.Lock()
	stream.PendingExecs[execID] = runtimecore.PendingExec{
		MessageID:   7,
		ExecID:      execID,
		ExecKind:    "subagent",
		ToolCallID:  toolCallID,
		ModelCallID: "model-call-defer",
		ArgsJSON:    []byte(`{"description":"delegate work"}`),
		OpenedAt:    time.Now().Add(-30 * time.Minute),
	}
	stream.mu.Unlock()
	service.nativeDelegations = map[string]*nativeDelegationRuntime{
		execID: {
			ID:                      execID,
			ParentRequestID:         stream.RequestID,
			ConversationID:          stream.ConversationID,
			ToolCallID:              toolCallID,
			Status:                  delegation.TaskRunning,
			LastEffectiveProgressAt: time.Now().Add(-30 * time.Minute),
		},
	}

	// 第一次看门狗到期：应延期，而不是把完成中的子代理强杀成超时。
	if err := service.recoverStaleExecWithoutTerminal(stream, execID, 7, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverStaleExecWithoutTerminal() error = %v", err)
	}
	stream.mu.Lock()
	_, stillPending := stream.PendingExecs[execID]
	stream.mu.Unlock()
	if !stillPending {
		t.Fatal("exec was force-recovered on first watchdog fire, want deferral (exec must stay pending)")
	}
	item, ok := service.nativeDelegationTask(execID)
	if !ok {
		t.Fatal("native runtime missing")
	}
	if item.Status != delegation.TaskRunning {
		t.Fatalf("runtime status = %q, want running (deferral must not terminalize)", item.Status)
	}
	if item.ExecWatchdogDeferrals != 1 {
		t.Fatalf("ExecWatchdogDeferrals = %d, want 1", item.ExecWatchdogDeferrals)
	}

	// 达到延期上限后的下一次到期：不再延期，按超时收口（exec 被移除、运行态转 timed_out）。
	if err := service.recoverStaleExecWithoutTerminal(stream, execID, 7, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverStaleExecWithoutTerminal() (recovery) error = %v", err)
	}
	stream.mu.Lock()
	_, stillPending = stream.PendingExecs[execID]
	stream.mu.Unlock()
	if stillPending {
		t.Fatal("exec still pending after deferral cap, want timeout recovery")
	}
	item, ok = service.nativeDelegationTask(execID)
	if !ok {
		t.Fatal("native runtime missing after recovery")
	}
	if item.Status != delegation.TaskTimedOut {
		t.Fatalf("runtime status = %q, want %q after deferral cap", item.Status, delegation.TaskTimedOut)
	}
	if !strings.Contains(item.Error, "exec_watchdog_timeout") {
		t.Fatalf("runtime error = %q, want exec_watchdog_timeout reason", item.Error)
	}
}
