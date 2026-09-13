package forwarder

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cursor/gen/agentv1"
)

func TestCheckpointPersistDebouncesDiskWrites(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, NewStreamBroker())
	conversationID := "aaaaaaaa-0000-0000-0000-000000000001"
	conversation := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	persisted, err := store.SaveConversationWithEntries(conversationID, conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "hello"),
	})
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	stream, err := service.broker.OpenStream("request-1", conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "hello")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := service.replaceCheckpointConversation(stream, persisted); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}

	before := snapshotConversationFiles(t, historyRoot, conversationID)
	for index := 0; index < 4; index++ {
		if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
			newMetadataEntry(1, "request-1", "note", map[string]any{"index": index}),
		}); err != nil {
			t.Fatalf("appendConversationEntries(%d) error = %v", index, err)
		}
	}

	time.Sleep(50 * time.Millisecond)
	assertConversationFilesUnchanged(t, historyRoot, conversationID, before)

	if err := service.flushCheckpointPersistSync(stream, conversationID); err != nil {
		t.Fatalf("flushCheckpointPersistSync() error = %v", err)
	}

	loaded, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if len(loaded.Entries) != 5 {
		t.Fatalf("entry count = %d, want 5 after flush", len(loaded.Entries))
	}
}

func TestCheckpointPersistFlushOnTerminalPhase(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, NewStreamBroker())
	conversationID := "aaaaaaaa-0000-0000-0000-000000000002"
	conversation := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	persisted, err := store.SaveConversationWithEntries(conversationID, conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-2", "hello"),
	})
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	stream, err := service.broker.OpenStream("request-2", conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "hello")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := service.replaceCheckpointConversation(stream, persisted); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(1, "request-2", "note", map[string]any{"done": true}),
	}); err != nil {
		t.Fatalf("appendConversationEntries() error = %v", err)
	}

	service.setTurnPhase(stream, TurnPhaseCompleted)

	loaded, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("entry count = %d, want 2 after terminal flush", len(loaded.Entries))
	}
	contextPath := filepath.Join(historyRoot, conversationID, conversationContextFileName)
	if _, err := store.LoadConversation(conversationID); err != nil {
		t.Fatalf("context still readable at %s: %v", contextPath, err)
	}
}

// 回归：防抖定时器回调与同步 flush 并发进入 persistCheckpointDelta 时，
// 旧实现会各自快照同一批 pending、重复落盘，甚至把含重复条目的文件写回内存
// checkpoint。persistMu 串行化后，并发调用不得产生磁盘重复条目。
func TestCheckpointPersistConcurrentCallsDoNotDuplicateEntries(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, NewStreamBroker())
	conversationID := "aaaaaaaa-0000-0000-0000-000000000003"
	conversation := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	persisted, err := store.SaveConversationWithEntries(conversationID, conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-3", "hello"),
	})
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	stream, err := service.broker.OpenStream("request-3", conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "hello")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := service.replaceCheckpointConversation(stream, persisted); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(1, "request-3", "note", map[string]any{"kind": "concurrent-flush"}),
	}); err != nil {
		t.Fatalf("appendConversationEntries() error = %v", err)
	}

	const concurrency = 8
	var wg sync.WaitGroup
	for index := 0; index < concurrency; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = service.persistCheckpointDelta(stream, conversationID)
		}()
	}
	wg.Wait()

	loaded, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("entry count = %d, want 2 (user message + single metadata entry, no duplicates)", len(loaded.Entries))
	}

	stream.mu.Lock()
	checkpointCount := len(stream.CheckpointConversation.Entries)
	stream.mu.Unlock()
	if checkpointCount != 2 {
		t.Fatalf("memory checkpoint entry count = %d, want 2", checkpointCount)
	}
}
