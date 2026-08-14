package forwarder

import (
	"path/filepath"
	"testing"
	"time"
)

func TestConversationFileLockBlocksInsteadOfSpinning(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "conversation.lock")
	held := make(chan struct{})
	go func() {
		releaseLock, err := acquireConversationFileLock(lockPath)
		if err != nil {
			t.Errorf("initial acquireConversationFileLock() error = %v", err)
			close(held)
			return
		}
		close(held)
		time.Sleep(100 * time.Millisecond)
		releaseLock()
	}()
	<-held

	start := time.Now()
	releaseSecond, err := acquireConversationFileLock(lockPath)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("contended acquireConversationFileLock() error = %v", err)
	}
	releaseSecond()

	if elapsed < 20*time.Millisecond {
		t.Fatalf("contended lock acquisition returned too quickly (%s), want blocking wait", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("contended lock acquisition took too long (%s), want prompt wake after release", elapsed)
	}
}

func TestConversationFileLockSerializesWriters(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversationID := "aaaaaaaa-0000-0000-0000-000000000001"
	conversation := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	if _, err := store.SaveConversationWithEntries(conversationID, conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "hello"),
	}); err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}

	done := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(seq int) {
			_, _, err := store.AppendEntries(conversationID, []HistoryEntry{
				newMetadataEntry(1, "request-1", "note", map[string]any{"seq": seq}),
			})
			done <- err
		}(index)
	}

	for index := 0; index < 2; index++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("AppendEntries() error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("AppendEntries() timed out waiting for lock")
		}
	}

	persisted, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if len(persisted.Entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(persisted.Entries))
	}
}
