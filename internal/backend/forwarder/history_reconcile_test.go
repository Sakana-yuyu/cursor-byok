package forwarder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cursor/gen/agentv1"
)

type reconcileFileSnapshot struct {
	body    []byte
	modTime time.Time
}

func snapshotConversationFiles(t *testing.T, historyRoot string, conversationID string) map[string]reconcileFileSnapshot {
	t.Helper()
	snapshot := make(map[string]reconcileFileSnapshot, 2)
	for _, name := range []string{conversationStateFileName, conversationContextFileName} {
		path := filepath.Join(historyRoot, conversationID, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		snapshot[name] = reconcileFileSnapshot{body: body, modTime: info.ModTime()}
	}
	return snapshot
}

func assertConversationFilesUnchanged(t *testing.T, historyRoot string, conversationID string, before map[string]reconcileFileSnapshot) {
	t.Helper()
	after := snapshotConversationFiles(t, historyRoot, conversationID)
	for name, want := range before {
		got := after[name]
		if string(got.body) != string(want.body) {
			t.Fatalf("%s/%s content changed:\nbefore = %s\nafter  = %s", conversationID, name, want.body, got.body)
		}
		if !got.modTime.Equal(want.modTime) {
			t.Fatalf("%s/%s mtime changed: before = %s after = %s", conversationID, name, want.modTime, got.modTime)
		}
	}
}

// seedReconcileConversation 写入一个「时间戳明显在过去」的会话，便于断言对账不抬时间。
func seedReconcileConversation(t *testing.T, store *ConversationFileStore, conversationID string, entries []HistoryEntry) *ConversationFile {
	t.Helper()
	createdAt := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Millisecond)
	stamped := make([]HistoryEntry, 0, len(entries))
	for index, entry := range entries {
		entry.CreatedAt = createdAt.Add(time.Duration(index) * time.Second)
		stamped = append(stamped, entry)
	}
	conversation := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	if _, err := store.SaveConversationWithEntries(conversationID, conversation, stamped); err != nil {
		t.Fatalf("SaveConversationWithEntries(%s) error = %v", conversationID, err)
	}
	persisted, err := store.UpdateConversationMeta(conversationID, func(target *ConversationFile) error {
		target.CreatedAt = createdAt
		target.UpdatedAt = createdAt.Add(time.Duration(len(stamped)) * time.Second)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateConversationMeta(%s) error = %v", conversationID, err)
	}
	return persisted
}

func readReconcileState(t *testing.T, historyRoot string, conversationID string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(historyRoot, conversationID, conversationStateFileName))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	state := map[string]any{}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decode state.json: %v", err)
	}
	return state
}

func countInterruptedEntries(t *testing.T, store *ConversationFileStore, conversationID string) int {
	t.Helper()
	conversation, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation(%s) error = %v", conversationID, err)
	}
	if conversation == nil {
		t.Fatalf("LoadConversation(%s) = nil", conversationID)
	}
	count := 0
	for _, entry := range conversation.Entries {
		if _, ok := interruptedControlEntryReason(entry); ok {
			count++
		}
	}
	return count
}

func TestHistoryMaintenanceClosesStaleLoopsWithoutTouchingTimestamps(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)

	running := seedReconcileConversation(t, store, "11111111-1111-1111-1111-111111111111", []HistoryEntry{
		testUserMessageEntry(t, 1, "request-running", "跑起来"),
		newAssistantTextEntry(1, "request-running", "开始工作", "", ""),
	})
	if running.CurrentLoopStatus != "running" {
		t.Fatalf("seeded status = %q, want running", running.CurrentLoopStatus)
	}
	waitingTool := seedReconcileConversation(t, store, "22222222-2222-2222-2222-222222222222", []HistoryEntry{
		testUserMessageEntry(t, 1, "request-waiting", "跑起来"),
		newToolCallEntry(1, "request-waiting", "tool-1", "Edit", "", "", testEditToolCall(t, "main.go")),
	})
	if waitingTool.CurrentLoopStatus != "waiting_tool" {
		t.Fatalf("seeded status = %q, want waiting_tool", waitingTool.CurrentLoopStatus)
	}
	completed := seedReconcileConversation(t, store, "33333333-3333-3333-3333-333333333333", []HistoryEntry{
		testUserMessageEntry(t, 1, "request-completed", "跑起来"),
		newMetadataEntry(1, "request-completed", "turn_completed", map[string]any{}),
	})
	if completed.CurrentLoopStatus != "completed" {
		t.Fatalf("seeded status = %q, want completed", completed.CurrentLoopStatus)
	}

	completedBefore := snapshotConversationFiles(t, historyRoot, completed.ConversationID)
	runningStateBefore := readReconcileState(t, historyRoot, running.ConversationID)

	if err := service.runHistoryMaintenance(); err != nil {
		t.Fatalf("runHistoryMaintenance() error = %v", err)
	}

	for _, conversationID := range []string{running.ConversationID, waitingTool.ConversationID} {
		reloaded, err := store.LoadConversation(conversationID)
		if err != nil {
			t.Fatalf("LoadConversation(%s) error = %v", conversationID, err)
		}
		if reloaded.CurrentLoopStatus != conversationStatusInterrupted {
			t.Fatalf("%s status = %q, want interrupted", conversationID, reloaded.CurrentLoopStatus)
		}
		if got := countInterruptedEntries(t, store, conversationID); got != 1 {
			t.Fatalf("%s interrupted entry count = %d, want 1", conversationID, got)
		}
	}

	assertConversationFilesUnchanged(t, historyRoot, completed.ConversationID, completedBefore)

	runningStateAfter := readReconcileState(t, historyRoot, running.ConversationID)
	for _, field := range []string{"created_at", "updated_at"} {
		if runningStateAfter[field] != runningStateBefore[field] {
			t.Fatalf("running %s changed: before = %v after = %v", field, runningStateBefore[field], runningStateAfter[field])
		}
	}

	// 连跑两次只应留下一条 interrupted 条目。
	if err := service.runHistoryMaintenance(); err != nil {
		t.Fatalf("second runHistoryMaintenance() error = %v", err)
	}
	if got := countInterruptedEntries(t, store, running.ConversationID); got != 1 {
		t.Fatalf("interrupted entry count after second pass = %d, want 1", got)
	}
}

// TestHistoryMaintenanceSkipsConversationWrittenAfterProcessStart 钉死对账的第二道
// 安全阀：hasActiveConversationStream 只看得见本进程内存里的流，挡不住「第二个实例
// 正在跑同一个 history 根目录」。凡是本进程启动之后才更新过的会话一律不碰。
func TestHistoryMaintenanceSkipsConversationWrittenAfterProcessStart(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, NewStreamBroker())

	conversationID := "55555555-5555-5555-5555-555555555555"
	// 不走 seedReconcileConversation：这里要的正是「updated_at = 刚刚」。
	fresh := seedRunningConversation(t, store, conversationID, "request-fresh")
	if !fresh.UpdatedAt.After(historyReconcileProcessStartedAt) {
		t.Fatalf("test setup is useless: updated_at %s is not after process start %s", fresh.UpdatedAt, historyReconcileProcessStartedAt)
	}

	before := snapshotConversationFiles(t, historyRoot, conversationID)
	if err := service.runHistoryMaintenance(); err != nil {
		t.Fatalf("runHistoryMaintenance() error = %v", err)
	}
	assertConversationFilesUnchanged(t, historyRoot, conversationID, before)
}

func TestHistoryMaintenanceSkipsConversationWithActiveStream(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)

	active := seedReconcileConversation(t, store, "44444444-4444-4444-4444-444444444444", []HistoryEntry{
		testUserMessageEntry(t, 1, "request-active", "跑起来"),
		newAssistantTextEntry(1, "request-active", "开始工作", "", ""),
	})
	if _, err := broker.OpenStream("request-active", active.ConversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "跑起来"); err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	before := snapshotConversationFiles(t, historyRoot, active.ConversationID)
	if err := service.runHistoryMaintenance(); err != nil {
		t.Fatalf("runHistoryMaintenance() error = %v", err)
	}
	assertConversationFilesUnchanged(t, historyRoot, active.ConversationID, before)
}
