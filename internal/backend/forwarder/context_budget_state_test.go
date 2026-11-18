package forwarder

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	"cursor/internal/apperror"
	modeladapter "cursor/internal/backend/agent/model"
)

func TestContextBudgetWindowChangeInvalidatesProjectionAndAnchor(t *testing.T) {
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = 64000
	conversation.TokenDetailsUsedTokens = 42000
	conversation.TokenDetailsUsageView = "old-provider-view"
	conversation.UsageAnchorTokens = 42000
	conversation.UsageAnchorMessageCount = 8
	conversation.AutoCompactionPending = true
	before := cloneConversationFile(conversation)
	projection, err := newContextProjectionState(conversation, "model-a",
		firstEntrySeqForTurn(conversation.Entries, 2), lastEntrySeqForTurn(conversation.Entries, 3), "summary")
	if err != nil {
		t.Fatal(err)
	}
	if valid, reason := validateContextProjectionState(projection, conversation, "model-a"); !valid {
		t.Fatalf("initial projection invalid: %s", reason)
	}
	applyConversationContextBudget(conversation, 32000)
	if conversation.TokenDetailsMaxTokens != 32000 || conversation.UsageAnchorTokens != 0 ||
		conversation.UsageAnchorMessageCount != 0 || conversation.TokenDetailsUsageView != "" || conversation.AutoCompactionPending {
		t.Fatalf("old budget state survived: %+v", conversation)
	}
	if valid, reason := validateContextProjectionState(projection, conversation, "model-a"); valid || reason != "context_window_changed" {
		t.Fatalf("smaller window reused projection: valid=%v reason=%s", valid, reason)
	}
	if !reflect.DeepEqual(before.Entries, conversation.Entries) {
		t.Fatal("changing context selection altered canonical history")
	}
}

func TestContextBudgetCacheSeparatesWindowsAndExactOutputCaps(t *testing.T) {
	base := ProviderRequest{ConversationID: "cache-budget", ModelID: "model-a", MaxTokens: 4096,
		RequestedContextWindowTokens: 64000, ContextWindowTokens: 64000,
		Messages: []modeladapter.Message{{Role: "user", Content: "same request"}}}
	key := providerCacheKey(base)
	for name, change := range map[string]func(*ProviderRequest){
		"selected_window":           func(req *ProviderRequest) { req.RequestedContextWindowTokens = 32000 },
		"channel_ceiling":           func(req *ProviderRequest) { req.ContextWindowTokens = 32000 },
		"output_in_same_old_bucket": func(req *ProviderRequest) { req.MaxTokens = 4000 },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			change(&request)
			if got := providerCacheKey(request); got == "" || got == key {
				t.Fatal("cache can replay a larger budget into the smaller request")
			}
		})
	}
	legacy := base
	legacy.RequestedContextWindowTokens, legacy.ContextWindowTokens = 0, 0
	legacyKey := providerCacheKey(legacy)
	legacy.MaxTokens = 4000
	if providerCacheKey(legacy) != legacyKey {
		t.Fatal("legacy requests lost their existing output bucket behavior")
	}
}

func TestContextBudgetActiveRequestDoesNotSilentlyIgnoreChangedSelection(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("active-budget", "conversation-budget", 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "question")
	if err != nil {
		t.Fatal(err)
	}
	stream.RequestedContextWindowTokens = 64000
	stream.Phase = TurnPhaseProviderRunning
	service := &Service{broker: broker, runQueue: newRunQueue()}
	intent := InboundIntent{Kind: "run", RequestID: stream.RequestID, ConversationID: stream.ConversationID, RequestedContextWindowTokens: 64000}
	service.runQueue.Submit(intent)
	intent.RequestedContextWindowTokens = 32000
	if err := service.dispatchInboundIntent(intent); err == nil || !strings.Contains(err.Error(), "Context") {
		t.Fatalf("changed active selection was silently ignored: %v", err)
	}
	if stream.RequestedContextWindowTokens != 64000 {
		t.Fatal("mutated a window after its provider request had started")
	}
	intent.RequestedContextWindowTokens = 64000
	if err := service.dispatchInboundIntent(intent); err != nil {
		t.Fatalf("identical reconnect should remain idempotent: %v", err)
	}
}

func TestContextBudgetQueuedSelectionIsCheckedBeforePromotion(t *testing.T) {
	queue := newRunQueue()
	owner := InboundIntent{RequestID: "owner", ConversationID: "queued-budget", RequestedContextWindowTokens: 64000}
	queued := InboundIntent{RequestID: "queued", ConversationID: owner.ConversationID, RequestedContextWindowTokens: 64000}
	queue.Submit(owner)
	queue.Submit(queued)
	changed := queued
	changed.RequestedContextWindowTokens = 32000
	if _, _, _, err := queue.SubmitChecked(changed, validateDuplicateContextSelection); err == nil {
		t.Fatal("queued duplicate silently lost the new context selection")
	}
	next, ok := queue.Finish(owner.ConversationID, owner.RequestID)
	if !ok || next.RequestedContextWindowTokens != 64000 {
		t.Fatal("failed duplicate modified queue ownership or its admitted budget")
	}
	if _, _, _, err := queue.SubmitChecked(changed, validateDuplicateContextSelection); err == nil {
		t.Fatal("promoted owner did not retain its context-selection identity")
	}
	if _, _, _, err := queue.SubmitChecked(queued, validateDuplicateContextSelection); err != nil {
		t.Fatalf("identical queued reconnect is no longer idempotent: %v", err)
	}
}

func TestContextBudgetUsageViewAndAnchorSurviveDiskRoundTrip(t *testing.T) {
	directory := t.TempDir()
	service := &Service{store: NewConversationFileStore(directory)}
	conversation, err := newRuntimeConversation("budget-disk-round-trip", agentv1.AgentMode_AGENT_MODE_AGENT)
	if err != nil {
		t.Fatal(err)
	}
	conversation.TokenDetailsMaxTokens = 64000
	conversation.TokenDetailsUsedTokens = 8293
	conversation.TokenDetailsUsageView = "provider-view-a"
	conversation.UsageAnchorTokens, conversation.UsageAnchorMessageCount = 8293, 4
	if err := service.syncConversationRecord(conversation.ConversationID, conversation); err != nil {
		t.Fatal(err)
	}
	loadFresh := func() *ConversationFile {
		t.Helper()
		stored, err := NewConversationFileStore(directory).LoadConversation(conversation.ConversationID)
		if err != nil || stored == nil {
			t.Fatalf("read persisted budget metadata: %v", err)
		}
		return stored
	}
	if stored := loadFresh(); stored.TokenDetailsUsageView != "provider-view-a" || stored.UsageAnchorTokens != 8293 || stored.UsageAnchorMessageCount != 4 {
		t.Fatalf("provider-view identity or anchor was dropped: %+v", stored)
	}
	applyConversationContextBudget(conversation, 32000)
	if err := service.syncConversationRecord(conversation.ConversationID, conversation); err != nil {
		t.Fatal(err)
	}
	if stored := loadFresh(); stored.TokenDetailsMaxTokens != 32000 || stored.TokenDetailsUsageView != "" || stored.UsageAnchorTokens != 0 || stored.UsageAnchorMessageCount != 0 {
		t.Fatalf("smaller window resurrected the previous usage view: %+v", stored)
	}
}

func TestContextBudgetStaleProviderCannotAcceptUsage(t *testing.T) {
	stream := &ActiveStream{CurrentProviderToken: 2}
	service := &Service{}
	accepted := 0
	if err := service.handleProviderEvent(stream, &streamProviderEvent{Token: 1, OnAccepted: func() { accepted++ }}); err != nil {
		t.Fatal(err)
	}
	if accepted != 0 {
		t.Fatal("stale provider callback mutated the current context usage")
	}
	if err := service.handleProviderEvent(stream, &streamProviderEvent{Token: 2, OnAccepted: func() { accepted++ }}); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 {
		t.Fatal("current provider callback was not accepted by its actor")
	}
}

func TestContextBudgetCheckpointUsageBelongsToItsProjection(t *testing.T) {
	conversation := &ConversationFile{TokenDetailsUsedTokens: 8293, TokenDetailsUsageView: "view-a"}
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: "short projection"}}, ContextUsageView: "view-a"}
	if got := (&Service{}).checkpointDisplayUsedTokens(conversation, nil, compiled, true, true); got != 8293 {
		t.Fatalf("provider usage was replaced by heuristic: %d", got)
	}
	compiled.ContextUsageView = "view-b"
	if got := (&Service{}).checkpointDisplayUsedTokens(conversation, nil, compiled, true, true); got != estimateCompiledPromptTokens(compiled) {
		t.Fatalf("different projection inherited stale provider usage: %d", got)
	}
	conversation.TokenDetailsUsageView = ""
	if got := (&Service{}).checkpointDisplayUsedTokens(conversation, nil, compiled, true, true); got != estimateCompiledPromptTokens(compiled) {
		t.Fatalf("legacy usage without view identity was treated as matching: %d", got)
	}
}

func TestContextBudgetFailuresExplainHowToProceed(t *testing.T) {
	for name, cause := range map[string]error{
		"uncompressible": compactionTerminalError{code: compactionOverflowTerminalCode, message: "input cannot fit"},
		"wire_overflow":  &modeladapter.ContextBudgetExceededError{ContextWindowTokens: 32000, EstimatedInputTokens: 33000, MaxOutputTokens: 4096},
		"invalid_config": &modeladapter.ContextBudgetValidationError{},
	} {
		t.Run(name, func(t *testing.T) {
			failure := normalizeStreamFailure(nil, cause)
			if failure.Code != apperror.CodeConfiguration || failure.Disposition != apperror.DispositionBlocked ||
				!strings.Contains(failure.UserMessage, "上下文") {
				t.Fatalf("unclear budget failure: %+v", failure)
			}
		})
	}
}

func TestContextBudgetRecoveryNeverReplaysVisibleOutput(t *testing.T) {
	for name, prepare := range map[string]func(*ActiveStream, *modeladapter.ContextBudgetExceededError) error{
		"text_delta": func(stream *ActiveStream, cause *modeladapter.ContextBudgetExceededError) error {
			stream.ProviderTextDeltaCount = 1
			return cause
		},
		"thinking_delta": func(stream *ActiveStream, cause *modeladapter.ContextBudgetExceededError) error {
			stream.ProviderThinkingDeltaCount = 1
			return cause
		},
		"tool": func(stream *ActiveStream, cause *modeladapter.ContextBudgetExceededError) error {
			stream.ToolInvocationCount = 1
			return cause
		},
		"output_started": func(_ *ActiveStream, cause *modeladapter.ContextBudgetExceededError) error {
			cause.OutputStarted = true
			return cause
		},
		"interrupted": func(_ *ActiveStream, cause *modeladapter.ContextBudgetExceededError) error {
			return errors.Join(cause, modeladapter.ErrMidStreamInterrupted)
		},
	} {
		t.Run(name, func(t *testing.T) {
			stream := &ActiveStream{}
			cause := prepare(stream, &modeladapter.ContextBudgetExceededError{ContextWindowTokens: 32000, EstimatedInputTokens: 33000, MaxOutputTokens: 4096})
			recovered, err := (&Service{}).recoverFromContextBudgetExceeded(stream, cause)
			if err != nil || recovered || stream.ContextBudgetRetryCount != 0 {
				t.Fatalf("replayed after visible output: recovered=%v err=%v attempts=%d", recovered, err, stream.ContextBudgetRetryCount)
			}
		})
	}
}

func TestContextBudgetSmallWindowsKeepUsableReserve(t *testing.T) {
	for _, window := range []uint32{8000, 32000, 64000, 200000} {
		reserve := (&Service{}).streamCompactionReserveTokens(nil, &ConversationFile{TokenDetailsMaxTokens: window})
		if reserve <= 0 || reserve > int64(window)/4 || int64(float64(window)*contextProjectionHardRatio)-reserve <= 0 {
			t.Fatalf("window=%d has unusable reserve=%d", window, reserve)
		}
	}
}
