// context_projection_orphan_test.go 验证「孤儿工具结果」的历史宽容处理：
// 历史 turn（turnSeq < currentTurnSeq）因中断/溢出收口产生的孤儿工具结果
// 必须摘要化继续（与 incomplete tool chain 同等待遇），而不是让整个会话
// 的所有后续请求永久失败（cannot summarize turn）。
package forwarder

import (
	"fmt"
	"testing"
)

func TestIsHistoricalInterruptedToolChainAcceptsOrphanToolResult(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		reason         string
		turnSeq        int64
		currentTurnSeq int64
		want           bool
	}{
		{
			name:           "historical orphan tool result is tolerated",
			reason:         `orphan tool result for call "call-1"`,
			turnSeq:        5,
			currentTurnSeq: 8,
			want:           true,
		},
		{
			name:           "orphan tool result in current turn stays fatal",
			reason:         `orphan tool result for call "call-1"`,
			turnSeq:        8,
			currentTurnSeq: 8,
			want:           false,
		},
		{
			name:           "incomplete tool chain in historical turn stays tolerated",
			reason:         `incomplete tool chain for call "call-1"`,
			turnSeq:        5,
			currentTurnSeq: 8,
			want:           true,
		},
		{
			name:           "duplicate tool call id in historical turn stays fatal",
			reason:         `duplicate tool call id "call-1"`,
			turnSeq:        5,
			currentTurnSeq: 8,
			want:           false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isHistoricalInterruptedToolChain(tc.reason, tc.turnSeq, tc.currentTurnSeq)
			if got != tc.want {
				t.Fatalf("isHistoricalInterruptedToolChain(%q, %d, %d) = %t, want %t", tc.reason, tc.turnSeq, tc.currentTurnSeq, got, tc.want)
			}
		})
	}
}

// TestBuildContextProjectionSummaryPlanToleratesHistoricalOrphanToolResult
// 复现线上故障：会话 3c9d78b3 的 turn 5 因溢出失败收口产生孤儿工具结果后，
// 所有后续请求在投影阶段立即失败（cannot summarize turn 5）。修复后该会话
// 应能继续压缩并生成投影计划，而不是永久毒死。
func TestBuildContextProjectionSummaryPlanToleratesHistoricalOrphanToolResult(t *testing.T) {
	entries := make([]HistoryEntry, 0, 16)
	for turn := int64(1); turn <= 8; turn++ {
		requestID := "request-" + fmt.Sprint(turn)
		entries = append(entries, testUserMessageEntry(t, turn, requestID, "question "+fmt.Sprint(turn)))
		if turn == 5 {
			// 无对应 tool_call、无 ToolCall 载荷的孤儿工具结果（失败收口产物）
			entries = append(entries, newToolResultEntry(turn, requestID, "call-orphan-5", "Read", `{"path":"x"}`, "orphan result", "", nil))
			continue
		}
		entries = append(entries, newAssistantTextEntry(turn, requestID, "answer "+fmt.Sprint(turn), "", ""))
	}
	conversation := testConversation(entries)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v (historical orphan tool result must not poison the conversation)", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want projection plan")
	}
	if len(plan.CompactedTurns) == 0 {
		t.Fatal("projection plan compacted no turns")
	}
}
