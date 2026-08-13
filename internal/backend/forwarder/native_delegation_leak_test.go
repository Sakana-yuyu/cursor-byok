package forwarder

import (
	"context"
	"fmt"
	"testing"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
)

// fastNativeProgressResolver 把「无有效进展」阈值压到毫秒级，让看门狗在测试里可观测。
type fastNativeProgressResolver struct{ nilResolver }

func (fastNativeProgressResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 200 * time.Millisecond
}

// TestBackgroundedNativeDelegationReleasesSlotWhenChildStreamIsGone 锁住「父流已终态 +
// 子流也断了」组合下的并发槽位释放。原生子代理默认并发上限只有 4，四个卡住的后台化子
// 代理就能彻底堵死这条路。
func TestBackgroundedNativeDelegationReleasesSlotWhenChildStreamIsGone(t *testing.T) {
	service := NewService(t.TempDir(), fastNativeProgressResolver{})
	defer service.multitaskDelegation.Close()

	parentRequestID := "req-parent-bg-slot"
	parentConversationID := "conv-parent-bg-slot"
	registerParentTaskDelegation(t, service, parentRequestID, parentConversationID, "call-parent-bg-slot")

	// follow-up 取消把子代理转入后台：nativeDelegations 记录仍是非终态，槽位仍被占。
	service.rememberBackgroundedDelegation(backgroundedDelegationExec{
		ExecID:          "exec-parent-task",
		ExecKind:        "subagent",
		StreamState:     execStreamStateBackgrounded,
		ToolCallID:      "call-parent-bg-slot",
		ConversationID:  parentConversationID,
		ParentRequestID: parentRequestID,
		TurnSeq:         1,
		BackgroundedAt:  time.Now().UTC(),
	})
	_ = service.broker.Cancel(parentRequestID, "[canceled] replaced by new turn")

	// 父会话随后一直在跑新回合：这正是「父会话活动被误当成后台子代理的进展」的现场。
	stopRefresh := make(chan struct{})
	defer close(stopRefresh)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopRefresh:
				return
			case <-ticker.C:
				service.markConversationActivity(parentConversationID)
			}
		}
	}()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if service.nativeDelegationLimiter.activeCount() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if active := service.nativeDelegationLimiter.activeCount(); active != 0 {
		t.Fatalf("native delegation slots still held = %d, want 0", active)
	}

	// 槽位真正回到池子里：能重新占满到上限，再多一个才被拒。
	stream, err := service.broker.OpenStream("req-parent-refill", "conv-parent-refill", 1, "model-id", "Model Name", 1, "refill")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	for index := 0; index < defaultNativeDelegationConcurrencyLimit; index++ {
		pending := runtimecore.PendingExec{
			ExecID:     fmt.Sprintf("exec-refill-%d", index),
			ExecKind:   "subagent",
			ToolCallID: fmt.Sprintf("call-refill-%d", index),
			ArgsJSON:   []byte(`{"description":"refill"}`),
		}
		if !service.registerNativeDelegation(stream, pending, nil) {
			t.Fatalf("registerNativeDelegation(%d) = false, want true after the leaked slot was released", index)
		}
	}
	overflow := runtimecore.PendingExec{
		ExecID:     "exec-refill-overflow",
		ExecKind:   "subagent",
		ToolCallID: "call-refill-overflow",
		ArgsJSON:   []byte(`{"description":"overflow"}`),
	}
	if service.registerNativeDelegation(stream, overflow, nil) {
		t.Fatal("registerNativeDelegation beyond the limit = true, want false")
	}
}

func TestBackgroundedDelegationRecordsPruneAfterRetention(t *testing.T) {
	service := &Service{}
	service.rememberBackgroundedDelegation(backgroundedDelegationExec{
		ExecID:         "exec-stale",
		ExecKind:       "subagent",
		BackgroundedAt: time.Now().UTC().Add(-2 * backgroundedDelegationRetention),
	})
	service.rememberBackgroundedDelegation(backgroundedDelegationExec{
		ExecID:         "exec-fresh",
		ExecKind:       "subagent",
		BackgroundedAt: time.Now().UTC(),
	})

	if _, ok := service.backgroundedDelegationRecord("exec-stale"); ok {
		t.Fatal("stale backgrounded delegation record survived; the map only ever grows")
	}
	if _, ok := service.backgroundedDelegationRecord("exec-fresh"); !ok {
		t.Fatal("fresh backgrounded delegation record was pruned")
	}
}
