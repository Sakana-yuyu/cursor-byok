package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestRewriteCheckpointTokenDetailsUsesActiveContextProjection(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(
		conversation.ConversationID,
		conversation,
		conversation.Entries,
	)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	projection, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, projection); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}

	compiler := contextProjectionLifecycleCompiler{}
	canonicalCompiled, err := compiler.Compile(
		persisted,
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
		"model-a",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("Compile(canonical) error = %v", err)
	}
	projectedConversation, err := projectConversationWithContextProjection(persisted, projection, "model-a")
	if err != nil {
		t.Fatalf("projectConversationWithContextProjection() error = %v", err)
	}
	projectedCompiled, err := compiler.Compile(
		projectedConversation,
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
		"model-a",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("Compile(projected) error = %v", err)
	}
	canonicalTokens := estimateCompiledPromptTokens(canonicalCompiled)
	projectedTokens := estimateCompiledPromptTokens(projectedCompiled)
	if canonicalTokens <= projectedTokens {
		t.Fatalf("test setup tokens: canonical=%d projected=%d", canonicalTokens, projectedTokens)
	}

	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), compiler, nil, broker)
	stream, err := broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	state := &agentv1.ConversationStateStructure{}
	canonicalEntries := append([]HistoryEntry(nil), persisted.Entries...)

	service.rewriteCheckpointTokenDetailsForClient(stream, persisted, state)

	if got, want := int64(state.TokenDetails.GetUsedTokens()), projectedTokens; got != want {
		t.Fatalf("checkpoint used tokens = %d, want projected estimate %d (canonical=%d)", got, want, canonicalTokens)
	}
	if state.TokenDetails.GetBreakdown().GetTotalUsedTokens() != state.TokenDetails.GetUsedTokens() {
		t.Fatalf(
			"breakdown total = %d, want used tokens %d",
			state.TokenDetails.GetBreakdown().GetTotalUsedTokens(),
			state.TokenDetails.GetUsedTokens(),
		)
	}
	if len(persisted.Entries) != len(canonicalEntries) {
		t.Fatal("checkpoint token display modified canonical entries")
	}
	stored, err := store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if stored == nil || stored.Applied {
		t.Fatalf("checkpoint token display modified projection applied state: %#v", stored)
	}
}

func TestRewriteCheckpointTokenDetailsFallsBackForInvalidContextProjection(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(
		conversation.ConversationID,
		conversation,
		conversation.Entries,
	)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	projection, err := newContextProjectionState(
		persisted,
		"other-model",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, projection); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}

	compiler := contextProjectionLifecycleCompiler{}
	canonicalCompiled, err := compiler.Compile(
		persisted,
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
		"model-a",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("Compile(canonical) error = %v", err)
	}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), compiler, nil, broker)
	stream, err := broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	state := &agentv1.ConversationStateStructure{}

	service.rewriteCheckpointTokenDetailsForClient(stream, persisted, state)

	if got, want := int64(state.TokenDetails.GetUsedTokens()), estimateCompiledPromptTokens(canonicalCompiled); got != want {
		t.Fatalf("checkpoint used tokens = %d, want canonical estimate %d for invalid sidecar", got, want)
	}
}
