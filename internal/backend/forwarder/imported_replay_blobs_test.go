package forwarder

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	promptengine "cursor/internal/backend/agent/prompt"
)

func stubDiskKVBlobs(t *testing.T, values map[string][]byte) {
	t.Helper()
	previous := readCursorDiskKVBlobs
	readCursorDiskKVBlobs = func(ids []string) (map[string][]byte, error) {
		if values == nil {
			return nil, nil
		}
		result := make(map[string][]byte, len(ids))
		for _, id := range ids {
			if value, ok := values[id]; ok {
				result[id] = value
			}
		}
		return result, nil
	}
	t.Cleanup(func() {
		readCursorDiskKVBlobs = previous
	})
}

func diskBlobID(t *testing.T, value []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(value)
	return digest[:]
}

func diskBlobHex(t *testing.T, value []byte) string {
	t.Helper()
	return hex.EncodeToString(diskBlobID(t, value))
}

func TestDecodeReplayBlobItemsHydratesFromDiskKV(t *testing.T) {
	inline := []byte(`{"role":"user","content":"inline question"}`)
	ourFormat := []byte(`{"role":"assistant","content":"hydrated answer"}`)
	canonical := []byte(`{"role":"assistant","content":[` +
		`{"type":"reasoning","text":"thinking","signature":"sig-1"},` +
		`{"type":"text","text":"canonical answer"},` +
		`{"type":"tool-call","toolCallId":"call-1","toolName":"Read","args":{"path":"a.go"}}]}`)

	disk := map[string][]byte{
		diskBlobHex(t, ourFormat):  ourFormat,
		diskBlobHex(t, canonical):  canonical,
	}
	stubDiskKVBlobs(t, disk)

	items := [][]byte{
		inline,
		diskBlobID(t, ourFormat),
		diskBlobID(t, canonical),
		diskBlobID(t, []byte("missing blob")),
	}
	messages, skipped := decodeReplayBlobItems(items, nil)
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "inline question" {
		t.Fatalf("inline message = %#v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "hydrated answer" {
		t.Fatalf("hydrated message = %#v", messages[1])
	}
	third := messages[2]
	if third.ReasoningContent != "thinking" || third.ReasoningSignature != "sig-1" {
		t.Fatalf("canonical reasoning = %#v", third)
	}
	if third.Content != "canonical answer" {
		t.Fatalf("canonical content = %q", third.Content)
	}
	if len(third.ToolCalls) != 1 ||
		third.ToolCalls[0].ID != "call-1" ||
		third.ToolCalls[0].Function.Name != "Read" ||
		third.ToolCalls[0].Function.Arguments != `{"path":"a.go"}` {
		t.Fatalf("canonical tool calls = %#v", third.ToolCalls)
	}
}

func TestConvertCanonicalBlobMessageToolResult(t *testing.T) {
	raw := []byte(`{"role":"tool","content":[` +
		`{"type":"tool-result","toolCallId":"call-9","toolName":"Grep","result":"match line"}],` +
		`"id":"tool-1"}`)
	message, ok := convertCanonicalBlobMessage(raw)
	if !ok {
		t.Fatal("convertCanonicalBlobMessage() rejected tool result message")
	}
	if message.Role != "tool" || message.ToolCallID != "call-9" || message.Name != "Grep" {
		t.Fatalf("tool message = %#v", message)
	}
	if message.Content != "match line" {
		t.Fatalf("tool content = %q", message.Content)
	}
}

func TestConvertCanonicalBlobMessageStructuredResult(t *testing.T) {
	raw := []byte(`{"role":"tool","content":[` +
		`{"type":"tool-result","toolCallId":"call-10","toolName":"TodoWrite","result":{"todos":[]}}]}`)
	message, ok := convertCanonicalBlobMessage(raw)
	if !ok {
		t.Fatal("convertCanonicalBlobMessage() rejected structured tool result")
	}
	if !strings.Contains(message.Content, `"todos":[]`) {
		t.Fatalf("structured tool content = %q", message.Content)
	}
}

func TestConvertCanonicalBlobMessageRejectsEmpty(t *testing.T) {
	if _, ok := convertCanonicalBlobMessage([]byte(`{"role":"assistant","content":[]}`)); ok {
		t.Fatal("convertCanonicalBlobMessage() accepted empty part list")
	}
	if _, ok := convertCanonicalBlobMessage([]byte(`{"content":"no role"}`)); ok {
		t.Fatal("convertCanonicalBlobMessage() accepted missing role")
	}
	if _, ok := convertCanonicalBlobMessage([]byte(`not json`)); ok {
		t.Fatal("convertCanonicalBlobMessage() accepted non-json")
	}
}

func TestImportConversationStateHydratesRootPromptBlobsFromDisk(t *testing.T) {
	systemMessage := []byte(`{"role":"system","content":"imported system prompt"}`)
	userMessage := []byte(`{"role":"user","content":"old question"}`)
	assistantMessage := []byte(`{"role":"assistant","content":"old answer"}`)
	stubDiskKVBlobs(t, map[string][]byte{
		diskBlobHex(t, systemMessage):    systemMessage,
		diskBlobHex(t, userMessage):      userMessage,
		diskBlobHex(t, assistantMessage): assistantMessage,
	})

	state := &agentv1.ConversationStateStructure{
		RootPromptMessagesJson: [][]byte{
			diskBlobID(t, systemMessage),
			diskBlobID(t, userMessage),
			diskBlobID(t, assistantMessage),
		},
	}
	messages, err := importedConversationStateModelMessages(state, nil)
	if err != nil {
		t.Fatalf("importedConversationStateModelMessages() error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	if messages[0].Role != "system" || messages[0].Content != "imported system prompt" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[2].Content != "old answer" {
		t.Fatalf("assistant message = %#v", messages[2])
	}
}

func TestImportConversationStateFallsBackToSummaryWhenBlobsUnresolvable(t *testing.T) {
	stubDiskKVBlobs(t, nil)
	summaryMessage := &agentv1.ConversationSummary{Summary: "fallback summary"}
	summary, err := proto.Marshal(summaryMessage)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	state := &agentv1.ConversationStateStructure{
		RootPromptMessagesJson: [][]byte{diskBlobID(t, []byte("missing"))},
		Summary:                summary,
	}
	conversation := testConversation(nil)
	entries, err := (&Service{}).importConversationState(conversation, state, nil)
	if err != nil {
		t.Fatalf("importConversationState() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "compacted_summary" {
		t.Fatalf("entries = %#v, want compaction summary fallback", entries)
	}
}

func TestDecodeReplayMessageJSONRejectsClientCanonicalShape(t *testing.T) {
	raw := []byte(`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c","toolName":"T","result":"r"}]}`)
	if _, ok := decodeReplayMessageJSON(raw); ok {
		t.Fatal("decodeReplayMessageJSON() accepted canonical part-array content")
	}
	plain := []byte(`{"role":"user","content":"hello"}`)
	message, ok := decodeReplayMessageJSON(plain)
	if !ok || message.Role != "user" || message.Content != "hello" {
		t.Fatalf("decodeReplayMessageJSON() plain = %#v ok=%v", message, ok)
	}
}

func TestEnrichImportedBlobsFromDiskSkipsWithoutRefs(t *testing.T) {
	stubDiskKVBlobs(t, nil)
	if store := enrichImportedBlobsFromDisk(nil, [][]byte{[]byte("short")}); store != nil {
		t.Fatalf("enrichImportedBlobsFromDisk() = %#v, want nil", store)
	}
}

func TestPromptReplayRoundTripKeepsCanonicalImport(t *testing.T) {
	raw := []byte(`{"role":"assistant","content":[` +
		`{"type":"text","text":"roundtrip answer"},` +
		`{"type":"tool-call","toolCallId":"call-77","toolName":"Edit","args":{"path":"b.go"}}]}`)
	message, ok := convertCanonicalBlobMessage(raw)
	if !ok {
		t.Fatal("convertCanonicalBlobMessage() failed")
	}
	encoded, err := promptengine.EncodeReplayMessages([]promptengine.Message{message})
	if err != nil {
		t.Fatalf("EncodeReplayMessages() error = %v", err)
	}
	decoded, err := promptengine.DecodeReplayMessages(encoded)
	if err != nil {
		t.Fatalf("DecodeReplayMessages() error = %v", err)
	}
	if len(decoded) != 1 || decoded[0].Content != "roundtrip answer" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if len(decoded[0].ToolCalls) != 1 || decoded[0].ToolCalls[0].ID != "call-77" {
		t.Fatalf("decoded tool calls = %#v", decoded[0].ToolCalls)
	}
}
