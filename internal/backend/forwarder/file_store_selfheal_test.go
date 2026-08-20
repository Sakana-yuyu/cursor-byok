package forwarder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCorruptHistoryFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), historyDirPerm); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not-valid-json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
}

func findQuarantinedHistoryFile(t *testing.T, dir string, base string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, base+".corrupt-*"))
	if err != nil {
		t.Fatalf("glob quarantined files: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one quarantined %s file, got %v", base, matches)
	}
	return matches[0]
}

func TestLoadConversationQuarantinesCorruptState(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	const conversationID = "conv-corrupt-state"
	writeCorruptHistoryFile(t, store.statePath(conversationID))

	conversation, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v, want self-heal without error", err)
	}
	if conversation != nil {
		t.Fatalf("LoadConversation() = %v, want nil conversation after quarantine", conversation)
	}
	if _, statErr := os.Stat(store.statePath(conversationID)); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt state.json should be renamed away, stat err = %v", statErr)
	}
	quarantined := findQuarantinedHistoryFile(t, store.conversationDir(conversationID), conversationStateFileName)
	body, err := os.ReadFile(quarantined)
	if err != nil {
		t.Fatalf("read quarantined file: %v", err)
	}
	if !strings.Contains(string(body), "{not-valid-json") {
		t.Fatalf("quarantined file should preserve original bytes, got %q", string(body))
	}
}

func TestLoadConversationQuarantinesCorruptContext(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	const conversationID = "conv-corrupt-context"
	dir := store.conversationDir(conversationID)
	if err := os.MkdirAll(dir, historyDirPerm); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	stateBody := `{"conversation_id":"` + conversationID + `","mode":"agent"}`
	if err := os.WriteFile(store.statePath(conversationID), []byte(stateBody), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	writeCorruptHistoryFile(t, store.contextPath(conversationID))

	conversation, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v, want self-heal without error", err)
	}
	if conversation == nil {
		t.Fatal("LoadConversation() = nil, want conversation with empty entries")
	}
	if len(conversation.Entries) != 0 {
		t.Fatalf("want empty entries after context quarantine, got %d", len(conversation.Entries))
	}
	findQuarantinedHistoryFile(t, dir, conversationContextFileName)
}

func TestSaveConversationWithEntriesRebuildsAfterCorruption(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	const conversationID = "conv-rebuild"
	writeCorruptHistoryFile(t, store.statePath(conversationID))

	source := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
	}
	entries := []HistoryEntry{testUserMessageEntry(t, 1, "request-1", "hello")}

	persisted, err := store.SaveConversationWithEntries(conversationID, source, entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v, want rebuild without error", err)
	}
	if len(persisted.Entries) != 1 {
		t.Fatalf("want 1 persisted entry, got %d", len(persisted.Entries))
	}

	reloaded, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() after rebuild error = %v", err)
	}
	if reloaded == nil || len(reloaded.Entries) != 1 {
		t.Fatalf("want reloaded conversation with 1 entry, got %+v", reloaded)
	}
}
