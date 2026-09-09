package forwarder

import (
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

func buildAnchoredCompiled(messages ...string) CompiledConversation {
	compiled := CompiledConversation{}
	for _, content := range messages {
		compiled.Messages = append(compiled.Messages, modeladapter.Message{Role: "user", Content: content})
	}
	return compiled
}

// TestEstimateCompiledPromptTokensAnchored_NoAnchor 验证无锚点/无效锚点时回退全量启发式。
func TestEstimateCompiledPromptTokensAnchored_NoAnchor(t *testing.T) {
	compiled := buildAnchoredCompiled("hello world", "another message")
	want := estimateCompiledPromptTokens(compiled)

	if got := estimateCompiledPromptTokensAnchored(nil, compiled); got != want {
		t.Fatalf("nil conversation should fall back to heuristic: got=%d want=%d", got, want)
	}
	conversation := &ConversationFile{}
	if got := estimateCompiledPromptTokensAnchored(conversation, compiled); got != want {
		t.Fatalf("zero anchor should fall back to heuristic: got=%d want=%d", got, want)
	}
	// 锚点消息数超出当前消息数（历史被压缩/回退后）必须失效。
	conversation.UsageAnchorTokens = 12345
	conversation.UsageAnchorMessageCount = len(compiled.Messages) + 1
	if got := estimateCompiledPromptTokensAnchored(conversation, compiled); got != want {
		t.Fatalf("anchor beyond message count should fall back to heuristic: got=%d want=%d", got, want)
	}
	conversation.UsageAnchorTokens = 12345
	conversation.UsageAnchorMessageCount = -1
	if got := estimateCompiledPromptTokensAnchored(conversation, compiled); got != want {
		t.Fatalf("negative anchor count should fall back to heuristic: got=%d want=%d", got, want)
	}
}

// TestEstimateCompiledPromptTokensAnchored_Exact 验证无新增消息时锚点即真实值。
func TestEstimateCompiledPromptTokensAnchored_Exact(t *testing.T) {
	compiled := buildAnchoredCompiled("a", "b", "c")
	conversation := &ConversationFile{
		UsageAnchorTokens:       4242,
		UsageAnchorMessageCount: len(compiled.Messages),
	}
	if got := estimateCompiledPromptTokensAnchored(conversation, compiled); got != 4242 {
		t.Fatalf("anchor with no new messages should return anchor tokens: got=%d want=4242", got)
	}
}

// TestEstimateCompiledPromptTokensAnchored_Delta 验证锚点 + 增量估算。
func TestEstimateCompiledPromptTokensAnchored_Delta(t *testing.T) {
	base := []string{"a", "b", "c"}
	compiled := buildAnchoredCompiled(append(append([]string{}, base...), "d", "e")...)
	conversation := &ConversationFile{
		UsageAnchorTokens:       100000,
		UsageAnchorMessageCount: len(base),
	}
	wantDelta := estimateModelMessagesTokens(compiled.Messages[len(base):])
	want := int64(100000) + wantDelta
	if got := estimateCompiledPromptTokensAnchored(conversation, compiled); got != want {
		t.Fatalf("anchored estimate mismatch: got=%d want=%d (delta=%d)", got, want, wantDelta)
	}
	// 纠正高估：全量启发式应高于锚定值（当启发式对前缀高估时）。
	heuristic := estimateCompiledPromptTokens(compiled)
	if heuristic <= want {
		t.Logf("heuristic=%d anchored=%d (anchor not correcting overestimate in this fixture; acceptable)", heuristic, want)
	}
}

// TestUsageAnchorApplicable 验证锚点适用性判定的边界条件。
func TestUsageAnchorApplicable(t *testing.T) {
	compiled := buildAnchoredCompiled("a", "b", "c")
	if usageAnchorApplicable(nil, compiled) {
		t.Fatal("nil conversation must not be anchorable")
	}
	if usageAnchorApplicable(&ConversationFile{}, compiled) {
		t.Fatal("zero anchor must not be anchorable")
	}
	conversation := &ConversationFile{UsageAnchorTokens: 100, UsageAnchorMessageCount: 3}
	if !usageAnchorApplicable(conversation, compiled) {
		t.Fatal("exact anchor count must be anchorable")
	}
	conversation.UsageAnchorMessageCount = 4
	if usageAnchorApplicable(conversation, compiled) {
		t.Fatal("anchor beyond message count must not be anchorable")
	}
}

// TestResolveContextPressureTokens_AnchorOverridesHeuristic 验证压缩压力判定采用锚定估算，
// 同时保留 TokenDetailsUsedTokens 与 AutoCompactionPending 的下限保护。
func TestResolveContextPressureTokens_AnchorOverridesHeuristic(t *testing.T) {
	compiled := buildAnchoredCompiled("a", "b", "c", "d")
	heuristic := estimateCompiledPromptTokens(compiled)
	// 模拟「真实 token 低于启发式估算」：前 3 条消息的实报值只取启发式的一半，
	// 锚定估算应纠正全量启发式的系统性高估，压缩因此不会过早触发。
	conversation := &ConversationFile{
		UsageAnchorTokens:       clampInt64ToUint32(estimateModelMessagesTokens(compiled.Messages[:3]) / 2),
		UsageAnchorMessageCount: 3, // 覆盖 a,b,c；d 为增量
	}
	anchored := estimateCompiledPromptTokensAnchored(conversation, compiled)
	if anchored >= heuristic {
		t.Fatalf("fixture expects anchor to correct overestimate: anchored=%d heuristic=%d", anchored, heuristic)
	}
	if got := resolveContextPressureTokens(conversation, compiled); got != anchored {
		t.Fatalf("pressure tokens should use anchored estimate: got=%d want=%d", got, anchored)
	}
	// 下限保护仍然生效：真实用量高于锚定值时取真实值。
	conversation.TokenDetailsUsedTokens = 9000
	if got := resolveContextPressureTokens(conversation, compiled); got != 9000 {
		t.Fatalf("real-usage floor must still apply: got=%d want=9000", got)
	}
	// 自动压缩挂起标记同样保持下限。
	conversation.TokenDetailsUsedTokens = 0
	conversation.AutoCompactionPending = true
	conversation.AutoCompactionPromptTokens = 8000
	if got := resolveContextPressureTokens(conversation, compiled); got != 8000 {
		t.Fatalf("pending-compaction floor must still apply: got=%d want=8000", got)
	}
}
