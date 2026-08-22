package forwarder

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	promptengine "cursor/internal/backend/agent/prompt"
)

func TestProjectCheckpointProjectionBuildsBlobBackedTurns(t *testing.T) {
	toolCall := testEditToolCall(t, "file.txt")

	tests := []struct {
		name    string
		entries []HistoryEntry
	}{
		{
			name: "no tools",
			entries: []HistoryEntry{
				testUserMessageEntry(t, 1, "request-1", "hello"),
				newAssistantTextEntry(1, "request-1", "hi", "", ""),
			},
		},
		{
			name: "completed tool call",
			entries: []HistoryEntry{
				testUserMessageEntry(t, 1, "request-1", "edit the file"),
				newToolCallEntry(1, "request-1", "call-1", "Edit", "", "", toolCall),
				newToolResultEntry(1, "request-1", "call-1", "Edit", `{"path":"file.txt"}`, "edited", "", toolCall),
				newAssistantTextEntry(1, "request-1", "done", "", ""),
			},
		},
		{
			name: "unfinished tool call",
			entries: []HistoryEntry{
				testUserMessageEntry(t, 1, "request-1", "edit the file"),
				newToolCallEntry(1, "request-1", "call-1", "Edit", "", "", toolCall),
			},
		},
		{
			name: "orphan tool result",
			entries: []HistoryEntry{
				testUserMessageEntry(t, 1, "request-1", "edit the file"),
				newToolResultEntry(1, "request-1", "call-1", "Edit", `{"path":"file.txt"}`, "edited", "", toolCall),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conversation := testConversation(test.entries)
			projection, err := NewHistoryProjector().ProjectCheckpointProjection(conversation)
			if err != nil {
				t.Fatalf("ProjectCheckpointProjection() error = %v", err)
			}
			if len(projection.State.GetTurns()) != 1 {
				t.Fatalf("ProjectCheckpointProjection() turns = %d, want 1 Blob ID", len(projection.State.GetTurns()))
			}
			assertCheckpointBlobGraph(t, projection)
			messages, err := promptengine.DecodeReplayMessages(projection.State.GetRootPromptMessagesJson())
			if err != nil {
				t.Fatalf("DecodeReplayMessages() error = %v", err)
			}
			if len(messages) == 0 {
				t.Fatal("ProjectCheckpointProjection() removed all root prompt replay history")
			}
			if messages[0].Role != "user" || messages[0].Content == "" {
				t.Fatalf("first replay message = %#v, want retained user history", messages[0])
			}
		})
	}
}

func TestProjectLegacyCheckpointLargeModelHistoryUsesRootReplay(t *testing.T) {
	entries := make([]HistoryEntry, 0, 400)
	for turn := int64(1); turn <= 200; turn++ {
		requestID := fmt.Sprintf("request-%d", turn)
		entries = append(entries,
			testModelMessageEntry(t, turn, requestID, modeladapter.Message{Role: "user", Content: fmt.Sprintf("question %d", turn)}),
			testModelMessageEntry(t, turn, requestID, modeladapter.Message{Role: "assistant", Content: fmt.Sprintf("answer %d", turn)}),
		)
	}

	state, err := NewHistoryProjector().ProjectLegacyCheckpoint(testConversation(entries))
	if err != nil {
		t.Fatalf("ProjectLegacyCheckpoint() error = %v", err)
	}
	if len(state.GetTurns()) != 0 {
		t.Fatalf("ProjectLegacyCheckpoint() model-only turns = %d, want 0", len(state.GetTurns()))
	}
	messages, err := promptengine.DecodeReplayMessages(state.GetRootPromptMessagesJson())
	if err != nil {
		t.Fatalf("DecodeReplayMessages() error = %v", err)
	}
	if len(messages) != 400 {
		t.Fatalf("decoded replay messages = %d, want 400", len(messages))
	}
}

func TestProjectPromptReplayCompactsHistoricalLsWithoutChangingToolSequence(t *testing.T) {
	const toolCallID = "call-ls-1"
	files := make([]*agentv1.LsDirectoryTreeNode_File, 0, 500)
	for index := 0; index < cap(files); index++ {
		files = append(files, &agentv1.LsDirectoryTreeNode_File{
			Name: fmt.Sprintf("large-directory-entry-%03d.txt", index),
		})
	}
	toolCallPayload, err := protojson.Marshal(&agentv1.ToolCall{
		Tool: &agentv1.ToolCall_LsToolCall{
			LsToolCall: &agentv1.LsToolCall{
				Args: &agentv1.LsArgs{
					Path:       "E:\\MyProject\\cursor-byok",
					ToolCallId: toolCallID,
				},
				Result: &agentv1.LsResult{
					Result: &agentv1.LsResult_Success{
						Success: &agentv1.LsSuccess{
							DirectoryTreeRoot: &agentv1.LsDirectoryTreeNode{
								AbsPath:       "E:\\MyProject\\cursor-byok",
								ChildrenFiles: files,
								NumFiles:      int32(len(files)),
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal Ls tool call: %v", err)
	}
	if len(toolCallPayload) <= staleToolResultAggressiveThreshold {
		t.Fatalf("Ls fixture bytes = %d, want > %d", len(toolCallPayload), staleToolResultAggressiveThreshold)
	}

	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "列出项目目录"),
		newToolCallEntry(1, "request-1", toolCallID, "Ls", "", "", toolCallPayload),
		newToolResultEntry(1, "request-1", toolCallID, "Ls", "{\"path\":\"E:\\\\MyProject\\\\cursor-byok\"}", "ls success path=E:\\MyProject\\cursor-byok files=500", "", toolCallPayload),
		newAssistantTextEntry(1, "request-1", "目录读取完成", "", ""),
		testUserMessageEntry(t, 2, "request-2", "继续检查"),
	})

	projector := NewHistoryProjector()
	first, err := projector.ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("first ProjectPromptReplay() error = %v", err)
	}
	second, err := projector.ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("second ProjectPromptReplay() error = %v", err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first replay: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second replay: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("ProjectPromptReplay() output is not deterministic")
	}

	assistantIndex := -1
	toolResultIndex := -1
	nextUserIndex := -1
	projectedTools := make(map[string]struct{})
	for index, message := range first {
		for _, toolCall := range message.ToolCalls {
			projectedTools[toolCall.Function.Name] = struct{}{}
			if toolCall.ID == toolCallID {
				assistantIndex = index
				if toolCall.Function.Name != "Ls" {
					t.Fatalf("projected tool name = %q, want Ls", toolCall.Function.Name)
				}
			}
		}
		if message.Role == "tool" && message.ToolCallID == toolCallID {
			toolResultIndex = index
			if message.Name != "Ls" {
				t.Fatalf("tool result name = %q, want Ls", message.Name)
			}
			if strings.Contains(message.Content, "large-directory-entry-") {
				t.Fatal("historical Ls directory tree leaked into provider replay")
			}
			if !strings.Contains(message.Content, "历史 Ls 目录树已从 provider 回放中省略") {
				t.Fatalf("historical Ls omission notice missing: %s", message.Content)
			}
		}
		if message.Role == "user" && strings.Contains(message.Content, "继续检查") {
			nextUserIndex = index
		}
	}
	if assistantIndex < 0 || toolResultIndex < 0 || nextUserIndex < 0 {
		t.Fatalf("replay indexes assistant=%d tool=%d next_user=%d", assistantIndex, toolResultIndex, nextUserIndex)
	}
	if !(assistantIndex < toolResultIndex && toolResultIndex < nextUserIndex) {
		t.Fatalf("tool replay order assistant=%d tool=%d next_user=%d", assistantIndex, toolResultIndex, nextUserIndex)
	}
	if len(projectedTools) != 1 {
		t.Fatalf("projected tool set = %#v, want only Ls", projectedTools)
	}
}

func TestProjectLegacyCheckpointSnapshotIsIsolatedFromLaterHistory(t *testing.T) {
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
	})
	projector := NewHistoryProjector()
	midpointProjection, err := projector.ProjectCheckpointProjection(conversation)
	if err != nil {
		t.Fatalf("midpoint ProjectCheckpointProjection() error = %v", err)
	}
	midpoint := midpointProjection.State

	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 2, "request-2", "second question"),
		newAssistantTextEntry(2, "request-2", "second answer", "", ""),
	})
	latestProjection, err := projector.ProjectCheckpointProjection(conversation)
	if err != nil {
		t.Fatalf("latest ProjectCheckpointProjection() error = %v", err)
	}
	latest := latestProjection.State

	midpointMessages, err := promptengine.DecodeReplayMessages(midpoint.GetRootPromptMessagesJson())
	if err != nil {
		t.Fatalf("decode midpoint replay: %v", err)
	}
	latestMessages, err := promptengine.DecodeReplayMessages(latest.GetRootPromptMessagesJson())
	if err != nil {
		t.Fatalf("decode latest replay: %v", err)
	}
	if len(midpointMessages) != 2 {
		t.Fatalf("midpoint replay messages = %d, want 2", len(midpointMessages))
	}
	if len(latestMessages) != 4 {
		t.Fatalf("latest replay messages = %d, want 4", len(latestMessages))
	}
	if len(midpoint.GetTurns()) != 1 || len(latest.GetTurns()) != 2 {
		t.Fatalf("checkpoint turn counts = (%d, %d), want (1, 2)", len(midpoint.GetTurns()), len(latest.GetTurns()))
	}
	assertCheckpointBlobGraph(t, midpointProjection)
	assertCheckpointBlobGraph(t, latestProjection)
}

func TestProjectCheckpointProjectionKeepsVisibleTurnsAcrossCompaction(t *testing.T) {
	summaryPayload, err := json.Marshal(compactionSummaryEntryPayload{Summary: "first turn summarized"})
	if err != nil {
		t.Fatalf("marshal compaction summary: %v", err)
	}
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
		{TurnSeq: 0, Role: "system", Kind: "compaction_summary", Payload: summaryPayload},
		testUserMessageEntry(t, 2, "request-2", "second question"),
		newAssistantTextEntry(2, "request-2", "second answer", "", ""),
	})

	projection, err := NewHistoryProjector().ProjectCheckpointProjection(conversation)
	if err != nil {
		t.Fatalf("ProjectCheckpointProjection() error = %v", err)
	}
	if len(projection.State.GetTurns()) != 2 {
		t.Fatalf("visible turns after compaction = %d, want 2", len(projection.State.GetTurns()))
	}
	assertCheckpointBlobGraph(t, projection)

	messages, err := promptengine.DecodeReplayMessages(projection.State.GetRootPromptMessagesJson())
	if err != nil {
		t.Fatalf("DecodeReplayMessages() error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("compacted root replay messages = %d, want summary plus latest turn", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "<conversation_summary>\nfirst turn summarized\n</conversation_summary>" {
		t.Fatalf("first compacted replay message = %#v", messages[0])
	}
}

func TestImportedConversationStateSkipsUnhydratableBlobTurnIDs(t *testing.T) {
	stubDiskKVBlobs(t, nil)
	turnID := sha256.Sum256([]byte("imported turn"))
	state := &agentv1.ConversationStateStructure{Turns: [][]byte{turnID[:]}}
	if _, err := importedConversationStateModelMessages(state, nil); err != nil {
		t.Fatalf("importedConversationStateModelMessages() error = %v", err)
	}
	conversation := testConversation(nil)
	service := &Service{}
	if _, err := service.importConversationState(conversation, state, nil); err != nil {
		t.Fatalf("importConversationState() error = %v", err)
	}
}

func TestImportedConversationStateRestoresBlobOnlyForkFromPrefetchedBlobs(t *testing.T) {
	projection, err := NewHistoryProjector().ProjectCheckpointProjection(testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "parent question"),
		newAssistantTextEntry(1, "request-1", "parent answer", "", ""),
	}))
	if err != nil {
		t.Fatalf("ProjectCheckpointProjection() error = %v", err)
	}
	prefetched := make([]*agentv1.PreFetchedBlob, 0, len(projection.Blobs))
	for _, blob := range projection.Blobs {
		prefetched = append(prefetched, &agentv1.PreFetchedBlob{Id: blob.ID, Value: blob.Data})
	}
	state := proto.Clone(projection.State).(*agentv1.ConversationStateStructure)
	state.RootPromptMessagesJson = nil
	conversation := testConversation(nil)
	entries, err := (&Service{}).importConversationState(conversation, state, prefetched)
	if err != nil {
		t.Fatalf("importConversationState() error = %v", err)
	}
	if len(conversation.ImportedTurnIDs) != 1 {
		t.Fatalf("ImportedTurnIDs = %d, want 1", len(conversation.ImportedTurnIDs))
	}
	if len(entries) != 2 {
		t.Fatalf("imported model entries = %d, want user and assistant", len(entries))
	}
}

func TestImportedInlineTurnWithSHA256LengthIsNotMisclassified(t *testing.T) {
	var rawTurn []byte
	for size := 1; size <= 128; size++ {
		rawUser, err := proto.Marshal(&agentv1.UserMessage{Text: strings.Repeat("x", size), MessageId: "inline"})
		if err != nil {
			t.Fatalf("marshal user message: %v", err)
		}
		rawTurn, err = proto.Marshal(&agentv1.ConversationTurnStructure{
			Turn: &agentv1.ConversationTurnStructure_AgentConversationTurn{
				AgentConversationTurn: &agentv1.AgentConversationTurnStructure{UserMessage: rawUser},
			},
		})
		if err != nil {
			t.Fatalf("marshal turn: %v", err)
		}
		if len(rawTurn) == sha256.Size {
			break
		}
	}
	if len(rawTurn) != sha256.Size {
		t.Fatal("test could not construct a 32-byte inline turn")
	}
	ids, err := importedTurnIDs([][]byte{rawTurn}, nil)
	if err != nil {
		t.Fatalf("importedTurnIDs() error = %v", err)
	}
	if len(ids) != 0 {
		t.Fatal("32-byte inline turn was misclassified as a Blob ID")
	}
	messages, err := importedConversationStateModelMessages(&agentv1.ConversationStateStructure{Turns: [][]byte{rawTurn}}, nil)
	if err != nil {
		t.Fatalf("importedConversationStateModelMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("inline turn messages = %#v, want one user message", messages)
	}
}

func TestImportedTurnIDsPersistThroughConversationStore(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	turnID := sha256.Sum256([]byte("parent turn"))
	conversation := testConversation(nil)
	conversation.ImportedTurnIDs = [][]byte{turnID[:]}
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, []HistoryEntry{
		testUserMessageEntry(t, 2, "request-2", "fork question"),
	})
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	if len(persisted.ImportedTurnIDs) != 1 || string(persisted.ImportedTurnIDs[0]) != string(turnID[:]) {
		t.Fatalf("persisted ImportedTurnIDs = %x, want %x", persisted.ImportedTurnIDs, turnID)
	}
	loaded, err := store.LoadConversation(conversation.ConversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if len(loaded.ImportedTurnIDs) != 1 || string(loaded.ImportedTurnIDs[0]) != string(turnID[:]) {
		t.Fatalf("loaded ImportedTurnIDs = %x, want %x", loaded.ImportedTurnIDs, turnID)
	}
}

func TestRewindImportedTurnPrefixUsesClientForkPoint(t *testing.T) {
	ids := make([][]byte, 4)
	for index := range ids {
		digest := sha256.Sum256([]byte(fmt.Sprintf("turn-%d", index+1)))
		ids[index] = digest[:]
	}
	trimmed := rewindImportedTurnPrefix(ids, runRewindDecision{
		TargetTurnSeq:      4,
		HasClientTurnCount: true,
		ClientTurnCount:    2,
	})
	if len(trimmed) != 2 || string(trimmed[0]) != string(ids[0]) || string(trimmed[1]) != string(ids[1]) {
		t.Fatalf("rewindImportedTurnPrefix() = %x, want first two IDs", trimmed)
	}
}

func TestRewindImportedTurnPrefixClearsAllIDsAtClientTurnZero(t *testing.T) {
	ids := make([][]byte, 2)
	for index := range ids {
		digest := sha256.Sum256([]byte(fmt.Sprintf("turn-%d", index+1)))
		ids[index] = digest[:]
	}
	trimmed := rewindImportedTurnPrefix(ids, runRewindDecision{
		TargetTurnSeq:      3,
		HasClientTurnCount: true,
		ClientTurnCount:    0,
	})
	if trimmed != nil {
		t.Fatalf("rewindImportedTurnPrefix() = %x, want nil at client turn zero", trimmed)
	}
}

func TestRewindImportedTurnPrefixUsesTargetWithoutClientCount(t *testing.T) {
	ids := make([][]byte, 4)
	for index := range ids {
		digest := sha256.Sum256([]byte(fmt.Sprintf("turn-%d", index+1)))
		ids[index] = digest[:]
	}
	trimmed := rewindImportedTurnPrefix(ids, runRewindDecision{TargetTurnSeq: 3})
	if len(trimmed) != 2 || string(trimmed[0]) != string(ids[0]) || string(trimmed[1]) != string(ids[1]) {
		t.Fatalf("rewindImportedTurnPrefix() = %x, want target-derived first two IDs", trimmed)
	}
}

func assertCheckpointBlobGraph(t *testing.T, projection *CheckpointProjection) {
	t.Helper()
	if projection == nil || projection.State == nil {
		t.Fatal("checkpoint projection is nil")
	}
	blobByID := make(map[string][]byte, len(projection.Blobs))
	for _, blob := range projection.Blobs {
		if len(blob.ID) != sha256.Size {
			t.Fatalf("blob id length = %d, want %d", len(blob.ID), sha256.Size)
		}
		digest := sha256.Sum256(blob.Data)
		if string(blob.ID) != string(digest[:]) {
			t.Fatal("blob id does not match SHA-256(data)")
		}
		blobByID[string(blob.ID)] = blob.Data
	}
	for _, turnID := range projection.State.GetTurns() {
		turnData, ok := blobByID[string(turnID)]
		if !ok {
			t.Fatal("turn references missing blob")
		}
		turn := &agentv1.ConversationTurnStructure{}
		if err := proto.Unmarshal(turnData, turn); err != nil {
			t.Fatalf("decode turn blob: %v", err)
		}
		agentTurn := turn.GetAgentConversationTurn()
		if agentTurn == nil {
			continue
		}
		if userID := agentTurn.GetUserMessage(); len(userID) > 0 {
			userData, exists := blobByID[string(userID)]
			if !exists {
				t.Fatal("turn references missing user message blob")
			}
			if err := proto.Unmarshal(userData, &agentv1.UserMessage{}); err != nil {
				t.Fatalf("decode user message blob: %v", err)
			}
		}
		for _, stepID := range agentTurn.GetSteps() {
			stepData, exists := blobByID[string(stepID)]
			if !exists {
				t.Fatal("turn references missing step blob")
			}
			if err := proto.Unmarshal(stepData, &agentv1.ConversationStep{}); err != nil {
				t.Fatalf("decode conversation step blob: %v", err)
			}
		}
	}
}

func testConversation(entries []HistoryEntry) *ConversationFile {
	conversation := &ConversationFile{
		ConversationID:     "conversation-1",
		RootConversationID: "conversation-1",
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
		Entries:            make([]HistoryEntry, 0, len(entries)),
	}
	appendEntriesInPlace(conversation, entries)
	return conversation
}

func testUserMessageEntry(t *testing.T, turnSeq int64, requestID string, text string) HistoryEntry {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.UserMessage{Text: text, MessageId: fmt.Sprintf("message-%d", turnSeq)})
	if err != nil {
		t.Fatalf("marshal user message: %v", err)
	}
	return HistoryEntry{
		TurnSeq:   turnSeq,
		RequestID: requestID,
		Role:      "user",
		Kind:      "user_message",
		Payload:   payload,
	}
}

func testModelMessageEntry(t *testing.T, turnSeq int64, requestID string, message modeladapter.Message) HistoryEntry {
	t.Helper()
	entry, ok, err := newModelMessageEntry(turnSeq, requestID, message)
	if err != nil {
		t.Fatalf("newModelMessageEntry() error = %v", err)
	}
	if !ok {
		t.Fatal("newModelMessageEntry() rejected test message")
	}
	return entry
}

func testEditToolCall(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.ToolCall{
		Tool: &agentv1.ToolCall_EditToolCall{
			EditToolCall: &agentv1.EditToolCall{
				Args: &agentv1.EditArgs{Path: path},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal edit tool call: %v", err)
	}
	return payload
}
