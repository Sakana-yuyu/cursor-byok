package forwarder

import "testing"

// Anthropic refusal 与 content_filter 同属策略拦截：不续写、不缓存（leookun/cursor-byok#500/#423）。
func TestPolicyBlockedFinishReasonIncludesRefusal(t *testing.T) {
	for _, reason := range []string{"content_filter", "refusal", " Refusal "} {
		if !isPolicyBlockedFinishReason(reason) {
			t.Fatalf("isPolicyBlockedFinishReason(%q) = false, want true", reason)
		}
		if !isTruncationFinishReason(stringsTrimLower(reason)) {
			t.Fatalf("isTruncationFinishReason(%q) = false, want true (must not be cached)", reason)
		}
	}
	for _, reason := range []string{"stop", "tool_calls", "end_turn", ""} {
		if isPolicyBlockedFinishReason(reason) {
			t.Fatalf("isPolicyBlockedFinishReason(%q) = true, want false", reason)
		}
	}
}

func stringsTrimLower(value string) string {
	out := []rune{}
	for _, r := range value {
		if r == ' ' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

// provider 未回报 usage 时用请求前估算兜底上下文用量，且不写入真实锚点（leookun/cursor-byok#460）。
func TestUpdateConversationTokenStateFallsBackToEstimate(t *testing.T) {
	service, store, stream, conversationID := openRetryTestStream(t)
	stream.ProviderEstimatedPromptTokens = 12345
	stream.PromptAnchorMessageCount = 3
	if err := service.updateConversationTokenState(stream, conversationID, turnUsageSnapshot{}, "call-retry", false); err != nil {
		t.Fatalf("updateConversationTokenState() error = %v", err)
	}
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil || conversation == nil {
		t.Fatalf("snapshotCheckpointConversation() = %v, %v", conversation, err)
	}
	if conversation.TokenDetailsUsedTokens != 12345 {
		t.Fatalf("TokenDetailsUsedTokens = %d, want 12345", conversation.TokenDetailsUsedTokens)
	}
	if conversation.UsageAnchorTokens != 0 || conversation.UsageAnchorMessageCount != 0 {
		t.Fatalf("estimate must not become a usage anchor: tokens=%d count=%d", conversation.UsageAnchorTokens, conversation.UsageAnchorMessageCount)
	}
	_ = store
}

// 真实 usage 存在时不使用估算。
func TestUpdateConversationTokenStatePrefersReportedUsage(t *testing.T) {
	service, _, stream, conversationID := openRetryTestStream(t)
	stream.ProviderEstimatedPromptTokens = 99999
	if err := service.updateConversationTokenState(stream, conversationID, turnUsageSnapshot{InputTokens: 500, UsagePresent: true}, "call-retry", false); err != nil {
		t.Fatalf("updateConversationTokenState() error = %v", err)
	}
	conversation, _, _, _ := service.snapshotCheckpointConversation(stream)
	if conversation.TokenDetailsUsedTokens != 500 {
		t.Fatalf("TokenDetailsUsedTokens = %d, want 500", conversation.TokenDetailsUsedTokens)
	}
}

// 后台化任务在后续回合运行期间跑完：下一次 provider pass 前补进当前回合，且只补一次（leookun/cursor-byok#487）。
func TestBackgroundedResultInjectedIntoActiveTurnOnce(t *testing.T) {
	service, store, stream, conversationID := openRetryTestStream(t)
	if err := service.flushCheckpointPersistSync(stream, conversationID); err != nil {
		t.Fatalf("flushCheckpointPersistSync() error = %v", err)
	}
	service.deliverBackgroundedDelegationResult(backgroundedDelegationExec{
		ExecID: "exec-1", ExecKind: "subagent", ToolCallID: "task-1", Description: "探索代码",
		ConversationID: conversationID, ParentRequestID: "request-old", TurnSeq: 1,
	}, "completed", "子代理结论")

	for range 2 {
		if err := service.injectBackgroundedResultsIntoActiveTurn(stream, conversationID, stream.RequestID); err != nil {
			t.Fatalf("injectBackgroundedResultsIntoActiveTurn() error = %v", err)
		}
	}
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		t.Fatalf("snapshotCheckpointConversation() error = %v", err)
	}
	count := 0
	for _, entry := range conversation.Entries {
		if _, ok := backgroundedReplayContentHash(entry); ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("backgrounded replay entries = %d, want 1", count)
	}
	// 落盘后，下一回合开头不应再补放同一结果（磁盘版本同时含 metadata 与已回放的 prompt_context）。
	if err := service.flushCheckpointPersistSync(stream, conversationID); err != nil {
		t.Fatalf("flushCheckpointPersistSync() error = %v", err)
	}
	persisted, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if replay := service.pendingBackgroundedDelegationEntries(persisted, "request-next", 2); len(replay) != 0 {
		t.Fatalf("next turn would replay %d already-injected results, want 0", len(replay))
	}
}
