package forwarder

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

// preflightOverflowCompiler 返回多条巨大的消息，使 guardCompiledConversationForProvider
// 截断后（每消息 120000 字符）估算 token 数仍远超窗口（64000），从而在 driveProvider
// 中稳定触发 auto force-retry 穷尽 / preflight 超限路径。
type preflightOverflowCompiler struct{}

func (preflightOverflowCompiler) Compile(_ *ConversationFile, mode agentv1.AgentMode, _ string, _ string, _ string, _ bool) (CompiledConversation, error) {
	big := strings.Repeat("x", 600_000)
	return CompiledConversation{
		Mode: mode,
		Messages: []modeladapter.Message{
			{Role: "user", Content: big},
			{Role: "user", Content: big},
			{Role: "user", Content: big},
			{Role: "user", Content: big},
		},
	}, nil
}

func (preflightOverflowCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

func TestBuildForcedPreflightCompactionPlanCompactsAllPriorTurns(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 4) // turns 1..4, request-1..request-4
	before := cloneConversationFile(conversation)
	stream := &ActiveStream{
		ConversationID: conversation.ConversationID,
		ModelID:        "model-a",
		ModelName:      "model-a",
		TurnSeq:        4,
		RequestID:      "request-4",
		LatestUserText: "question 4",
	}
	compiled := CompiledConversation{
		Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 600_000)}},
	}

	plan, err := service.buildForcedPreflightCompactionPlan(stream, conversation, compiled)
	if err != nil {
		t.Fatalf("buildForcedPreflightCompactionPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildForcedPreflightCompactionPlan() = nil, want plan")
	}
	if got, want := plan.Trigger, compactionPreflightForcedTrigger; got != want {
		t.Fatalf("plan.Trigger = %q, want %q", got, want)
	}
	if !plan.PreserveCurrentTurnInputs {
		t.Fatal("plan.PreserveCurrentTurnInputs = false, want true (current turn must survive compaction)")
	}
	if got, want := plan.CompactTurnCount, int32(3); got != want {
		t.Fatalf("plan.CompactTurnCount = %d, want %d (all prior turns)", got, want)
	}
	if got, want := plan.RequestSource, compactionRequestSourcePromptAsset; got != want {
		t.Fatalf("plan.RequestSource = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(conversation.Entries, before.Entries) {
		t.Fatal("buildForcedPreflightCompactionPlan() mutated canonical conversation entries")
	}
}

func TestBuildForcedPreflightCompactionPlanNilWhenNothingToCompact(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 1) // only current turn
	conversation.CurrentTurnSeq = 1
	conversation.CurrentRequestID = "request-1"
	stream := &ActiveStream{
		ConversationID: conversation.ConversationID,
		ModelID:        "model-a",
		ModelName:      "model-a",
		TurnSeq:        1,
		RequestID:      "request-1",
		LatestUserText: "question 1",
	}
	compiled := CompiledConversation{
		Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 600_000)}},
	}
	plan, err := service.buildForcedPreflightCompactionPlan(stream, conversation, compiled)
	if err != nil {
		t.Fatalf("buildForcedPreflightCompactionPlan() error = %v", err)
	}
	if plan != nil {
		t.Fatalf("buildForcedPreflightCompactionPlan() = non-nil plan, want nil (nothing to compact): %#v", plan)
	}
}

func TestEscalateForcedPreflightCompactionSemantics(t *testing.T) {
	newHarness := func(t *testing.T, mutate func(*ActiveStream)) (*Service, *ActiveStream) {
		t.Helper()
		store := NewConversationFileStore(t.TempDir())
		conversation := testConversation(nil)
		appendEntriesInPlace(conversation, []HistoryEntry{
			testUserMessageEntry(t, 1, "request-1", "prior question"),
			newAssistantTextEntry(1, "request-1", "prior answer", "", ""),
			testUserMessageEntry(t, 2, "request-2", "current question"),
			newAssistantTextEntry(2, "request-2", "current answer", "", ""),
		})
		persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
		if err != nil {
			t.Fatalf("SaveConversationWithEntries() error = %v", err)
		}
		provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
		broker := NewStreamBroker()
		service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
		stream, err := broker.OpenStream(
			"request-2",
			persisted.ConversationID,
			2,
			"model-a",
			"model-a",
			agentv1.AgentMode_AGENT_MODE_AGENT,
			"current question",
		)
		if err != nil {
			t.Fatalf("OpenStream() error = %v", err)
		}
		stream.CheckpointConversation = cloneConversationFile(persisted)
		if mutate != nil {
			mutate(stream)
		}
		return service, stream
	}
	compiled := CompiledConversation{
		Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 600_000)}},
	}
	overflowCause := compactionTerminalError{code: compactionOverflowTerminalCode, message: "overflow"}

	t.Run("triggers once and records attempt", func(t *testing.T) {
		service, stream := newHarness(t, nil)
		escalated, err := service.escalateForcedPreflightCompaction(stream, stream.CheckpointConversation, compiled, overflowCause)
		if err != nil {
			t.Fatalf("escalateForcedPreflightCompaction() error = %v", err)
		}
		if !escalated {
			t.Fatal("escalateForcedPreflightCompaction() = false, want true")
		}
		stream.mu.Lock()
		attempts := stream.PreflightForcedCompactionAttempts
		pending := stream.PendingCompaction
		stream.mu.Unlock()
		if attempts != 1 {
			t.Fatalf("PreflightForcedCompactionAttempts = %d, want 1", attempts)
		}
		if pending == nil || pending.Trigger != compactionPreflightForcedTrigger {
			t.Fatalf("PendingCompaction = %#v, want trigger %q", pending, compactionPreflightForcedTrigger)
		}
	})

	t.Run("blocked after attempts exhausted", func(t *testing.T) {
		service, stream := newHarness(t, func(stream *ActiveStream) {
			stream.PreflightForcedCompactionAttempts = preflightForcedCompactionMaxAttempts
		})
		escalated, err := service.escalateForcedPreflightCompaction(stream, stream.CheckpointConversation, compiled, overflowCause)
		if err != nil {
			t.Fatalf("escalateForcedPreflightCompaction() error = %v", err)
		}
		if escalated {
			t.Fatal("escalateForcedPreflightCompaction() = true after attempts exhausted, want false")
		}
	})

	t.Run("ignores non-overflow cause", func(t *testing.T) {
		service, stream := newHarness(t, nil)
		escalated, err := service.escalateForcedPreflightCompaction(stream, stream.CheckpointConversation, compiled, errors.New("some other failure"))
		if err != nil {
			t.Fatalf("escalateForcedPreflightCompaction() error = %v", err)
		}
		if escalated {
			t.Fatal("escalateForcedPreflightCompaction() = true for non-overflow cause, want false")
		}
	})

	t.Run("blocked while manual compaction requested", func(t *testing.T) {
		service, stream := newHarness(t, func(stream *ActiveStream) {
			stream.ManualCompaction = manualCompactionDirective{Requested: true}
		})
		escalated, err := service.escalateForcedPreflightCompaction(stream, stream.CheckpointConversation, compiled, overflowCause)
		if err != nil {
			t.Fatalf("escalateForcedPreflightCompaction() error = %v", err)
		}
		if escalated {
			t.Fatal("escalateForcedPreflightCompaction() = true during manual compaction, want false")
		}
	})

	t.Run("blocked while compaction already pending", func(t *testing.T) {
		service, stream := newHarness(t, func(stream *ActiveStream) {
			stream.PendingCompaction = newPendingCompaction(&compactionPlan{Trigger: contextProjectionTrigger})
		})
		escalated, err := service.escalateForcedPreflightCompaction(stream, stream.CheckpointConversation, compiled, overflowCause)
		if err != nil {
			t.Fatalf("escalateForcedPreflightCompaction() error = %v", err)
		}
		if escalated {
			t.Fatal("escalateForcedPreflightCompaction() = true while compaction pending, want false")
		}
	})

	t.Run("returns false when nothing to compact", func(t *testing.T) {
		service, stream := newHarness(t, nil)
		// 把 conversation 换成只有当前轮（无前序轮次可压缩）。
		store := NewConversationFileStore(t.TempDir())
		conversation := testConversation(nil)
		appendEntriesInPlace(conversation, []HistoryEntry{
			testUserMessageEntry(t, 2, "request-2", "current question"),
			newAssistantTextEntry(2, "request-2", "current answer", "", ""),
		})
		persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
		if err != nil {
			t.Fatalf("SaveConversationWithEntries() error = %v", err)
		}
		escalated, err := service.escalateForcedPreflightCompaction(stream, persisted, compiled, overflowCause)
		if err != nil {
			t.Fatalf("escalateForcedPreflightCompaction() error = %v", err)
		}
		if escalated {
			t.Fatal("escalateForcedPreflightCompaction() = true with nothing to compact, want false")
		}
	})
}

func TestDriveProviderEscalatesToForcedLegacyCompactionWhenAutoExhausted(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation(nil)
	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "prior question"),
		newAssistantTextEntry(1, "request-1", "prior answer", "", ""),
		testUserMessageEntry(t, 2, "request-2", "current question"),
		newAssistantTextEntry(2, "request-2", "current answer", "", ""),
	})
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 2
	conversation.CurrentRequestID = "request-2"
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), preflightOverflowCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-2",
		persisted.ConversationID,
		2,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"current question",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)

	if err := service.driveProvider(stream); err != nil {
		t.Fatalf("driveProvider() error = %v", err)
	}
	stream.mu.Lock()
	pending := stream.PendingCompaction
	attempts := stream.PreflightForcedCompactionAttempts
	status := stream.Status
	providerActive := stream.ProviderActive
	stream.mu.Unlock()
	if pending == nil || pending.Trigger != compactionPreflightForcedTrigger {
		t.Fatalf("PendingCompaction = %#v, want trigger %q", pending, compactionPreflightForcedTrigger)
	}
	if attempts != 1 {
		t.Fatalf("PreflightForcedCompactionAttempts = %d, want 1", attempts)
	}
	if status == StreamStatusFailed || status == StreamStatusCanceled || status == StreamStatusCompleted {
		t.Fatalf("driveProvider terminalized stream with status %q instead of escalating to forced compaction", status)
	}
	if providerActive {
		t.Fatal("forced compaction unexpectedly started the parent provider")
	}
	select {
	case request := <-provider.requests:
		t.Fatalf("forced compaction unexpectedly sent provider request: %#v", request)
	default:
	}
}

func TestDriveProviderDoesNotEscalateWhenAttemptsExhausted(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation(nil)
	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "prior question"),
		newAssistantTextEntry(1, "request-1", "prior answer", "", ""),
		testUserMessageEntry(t, 2, "request-2", "current question"),
		newAssistantTextEntry(2, "request-2", "current answer", "", ""),
	})
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 2
	conversation.CurrentRequestID = "request-2"
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), preflightOverflowCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-2",
		persisted.ConversationID,
		2,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"current question",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.PreflightForcedCompactionAttempts = preflightForcedCompactionMaxAttempts

	if err := service.driveProvider(stream); err != nil {
		t.Fatalf("driveProvider() error = %v", err)
	}
	stream.mu.Lock()
	status := stream.Status
	pending := stream.PendingCompaction
	stream.mu.Unlock()
	if status != StreamStatusFailed {
		t.Fatalf("stream status = %q, want %q (terminal after escalation exhausted)", status, StreamStatusFailed)
	}
	if pending != nil {
		t.Fatalf("PendingCompaction = %#v, want nil (no escalation after attempts exhausted)", pending)
	}
}
