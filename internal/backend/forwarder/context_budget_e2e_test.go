package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
	modeladapter "cursor/internal/backend/agent/model"
	legacyruntime "cursor/internal/runtime"
	"google.golang.org/protobuf/proto"
)

// These tests use a real protobuf/BidiAppend/actor/compiler/default-gateway/HTTP
// path. Only the remote model is replaced by deterministic local SSE responses.
// Token counts remain the production conservative estimates, not exact tokenizer counts.
type contextBudgetE2EResolver struct {
	fixedContextWindowResolver
	baseURL string
}

func (r contextBudgetE2EResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return &legacyruntime.ResolvedChannel{ID: "budget-e2e-channel", Name: "budget e2e", Provider: "openai", Model: "unlisted-budget-e2e-model", BaseURL: r.baseURL, APIKey: "test-key", ContextWindowTokens: r.tokens, MaxTokens: 4096, ProtocolMode: "fixed", ProtocolGroup: "chat_completions", OpenAIEndpoint: "/v1/chat/completions", OpenAIRequestGroup: "chat_completions"}, nil
}

type contextBudgetE2ECatalog struct{}

func (contextBudgetE2ECatalog) Load(agentv1.AgentMode, string) ([]json.RawMessage, []string, error) {
	return []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}`)}, []string{"Read"}, nil
}

type contextBudgetE2EAttempt struct {
	request ProviderRequest
	err     error
}
type contextBudgetE2EGateway struct {
	base     ProviderGateway
	mu       sync.Mutex
	attempts []contextBudgetE2EAttempt
	// Injected local pre-send protocol expansion rejection; never a network send.
	rejectParents int
	rejectWindow  int
}

func (g *contextBudgetE2EGateway) StartStream(ctx context.Context, request ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	summary := strings.Contains(request.CompileSummary, "compaction trigger=")
	var err error
	g.mu.Lock()
	reject := !summary && g.rejectParents > 0
	if reject {
		g.rejectParents--
	}
	window := g.rejectWindow
	g.mu.Unlock()
	if reject {
		err = &modeladapter.ContextBudgetExceededError{ContextWindowTokens: window, EstimatedInputTokens: int64(window), MaxOutputTokens: request.MaxTokens}
	} else {
		err = g.base.StartStream(ctx, request, sink)
	}
	g.mu.Lock()
	g.attempts = append(g.attempts, contextBudgetE2EAttempt{request: request, err: err})
	g.mu.Unlock()
	return err
}
func (g *contextBudgetE2EGateway) snapshot() []contextBudgetE2EAttempt {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]contextBudgetE2EAttempt(nil), g.attempts...)
}

type contextBudgetE2EWire struct {
	body   map[string]any
	input  int64
	output int
}
type contextBudgetE2EHarness struct {
	service *Service
	gateway *contextBudgetE2EGateway
	mu      sync.Mutex
	wires   []contextBudgetE2EWire
	seq     map[string]int64
}

func newContextBudgetE2EHarness(t *testing.T) *contextBudgetE2EHarness {
	t.Helper()
	h := &contextBudgetE2EHarness{seq: map[string]int64{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		encoded, _ := json.Marshal(body)
		input := modeladapter.EstimateContextTextTokens(string(encoded)) + 256
		output := 0
		for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			if v, ok := body[key].(float64); ok && int(v) > output {
				output = int(v)
			}
		}
		h.mu.Lock()
		h.wires = append(h.wires, contextBudgetE2EWire{body: body, input: input, output: output})
		h.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"budget-e2e\",\"model\":\"unlisted-budget-e2e-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Budget e2e compact summary and answer.\"}}]}\n\n")
		fmt.Fprintf(w, "data: {\"id\":\"budget-e2e\",\"model\":\"unlisted-budget-e2e-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":3,\"total_tokens\":%d}}\n\ndata: [DONE]\n\n", input, input+3)
	}))
	t.Cleanup(server.Close)
	resolver := contextBudgetE2EResolver{fixedContextWindowResolver: fixedContextWindowResolver{tokens: 200000}, baseURL: server.URL}
	h.gateway = &contextBudgetE2EGateway{base: NewProviderGateway(resolver)}
	projector := NewHistoryProjector()
	compiler := NewPromptCompiler(projector, contextBudgetE2ECatalog{}, NewReminderInjector(), nil, nil)
	h.service = newServiceWithDependencies(NewConversationFileStore(t.TempDir()), projector, compiler, h.gateway, NewStreamBroker())
	h.service.resolver = resolver
	t.Cleanup(h.service.CloseDebugRecorder)
	return h
}
func (h *contextBudgetE2EHarness) wireSnapshot() []contextBudgetE2EWire {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]contextBudgetE2EWire(nil), h.wires...)
}
func (h *contextBudgetE2EHarness) append(requestID string, message *agentv1.AgentClientMessage) error {
	bytes, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	h.seq[requestID]++
	_, err = h.service.BidiAppend(context.Background(), connect.NewRequest(&aiserverv1.BidiAppendRequest{RequestId: &aiserverv1.BidiRequestId{RequestId: requestID}, AppendSeqno: h.seq[requestID], DataBinary: bytes}))
	return err
}
func (h *contextBudgetE2EHarness) run(t *testing.T, requestID, conversationID, messageID, text, selection string, maxMode bool) error {
	t.Helper()
	model := &agentv1.RequestedModel{ModelId: "budget-e2e-channel", MaxMode: maxMode}
	if selection != "" {
		model.Parameters = []*agentv1.RequestedModel_ModelParameterValue{{Id: "context", Value: selection}}
	}
	return h.append(requestID, &agentv1.AgentClientMessage{Message: &agentv1.AgentClientMessage_RunRequest{RunRequest: &agentv1.AgentRunRequest{
		ConversationId: proto.String(conversationID), RequestedModel: model, Action: &agentv1.ConversationAction{Action: &agentv1.ConversationAction_UserMessageAction{UserMessageAction: &agentv1.UserMessageAction{UserMessage: &agentv1.UserMessage{MessageId: messageID, Text: text, Mode: agentv1.AgentMode_AGENT_MODE_AGENT}}}},
	}}})
}
func (h *contextBudgetE2EHarness) wait(t *testing.T, requestID string) (*ActiveStream, *agentv1.ConversationStateStructure) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	cursor := 0
	var checkpoint *agentv1.ConversationStateStructure
	for time.Now().Before(deadline) {
		events, err := h.service.broker.ReadFromCursor(requestID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		cursor += len(events)
		for _, event := range events {
			if state := event.Message.GetConversationCheckpointUpdate(); state != nil {
				checkpoint = state
			}
			if kv := event.Message.GetKvServerMessage(); kv.GetSetBlobArgs() != nil {
				err := h.append(requestID, &agentv1.AgentClientMessage{Message: &agentv1.AgentClientMessage_KvClientMessage{KvClientMessage: &agentv1.KvClientMessage{Id: kv.GetId(), Message: &agentv1.KvClientMessage_SetBlobResult{SetBlobResult: &agentv1.SetBlobResult{}}}}})
				if err != nil {
					t.Fatalf("ack checkpoint blob: %v", err)
				}
			}
		}
		stream, ok := h.service.broker.Get(requestID)
		if ok {
			stream.mu.Lock()
			terminal := isTerminalStreamStatus(stream.Status)
			done := stream.ActorDone
			stream.mu.Unlock()
			if terminal && done != nil {
				select {
				case <-done:
					// The last ACK may have published a terminal checkpoint after this batch.
					tail, _ := h.service.broker.ReadFromCursor(requestID, cursor)
					for _, event := range tail {
						if state := event.Message.GetConversationCheckpointUpdate(); state != nil {
							checkpoint = state
						}
					}
					return stream, checkpoint
				default:
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	stream, _ := h.service.broker.Get(requestID)
	if stream != nil {
		stream.mu.Lock()
		status, phase, compaction := stream.Status, stream.Phase, stream.PendingCompaction
		stream.mu.Unlock()
		t.Fatalf("timeout status=%s phase=%s pending_compaction=%v requests=%d wires=%d", status, phase, compaction, len(h.gateway.snapshot()), len(h.wireSnapshot()))
	}
	t.Fatal("no stream created")
	return nil, nil
}
func contextBudgetE2ETerminal(service *Service, stream *ActiveStream) StreamEvent {
	events, _ := service.broker.ReadFromCursor(stream.RequestID, 0)
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].End {
			return events[index]
		}
	}
	return StreamEvent{}
}

func contextBudgetE2EParentAttempts(attempts []contextBudgetE2EAttempt) []contextBudgetE2EAttempt {
	var parents []contextBudgetE2EAttempt
	for _, attempt := range attempts {
		if !strings.Contains(attempt.request.CompileSummary, "compaction trigger=") {
			parents = append(parents, attempt)
		}
	}
	return parents
}
func contextBudgetE2EAssertPairing(t *testing.T, messages []modeladapter.Message) {
	t.Helper()
	calls := map[string]bool{}
	results := map[string]bool{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			calls[call.ID] = true
		}
		if message.ToolCallID != "" {
			results[message.ToolCallID] = true
		}
	}
	if !reflect.DeepEqual(calls, results) {
		t.Fatalf("tool calls/results unpaired: calls=%v results=%v", calls, results)
	}
}
func contextBudgetE2EAssertBudget(t *testing.T, request ProviderRequest) int64 {
	t.Helper()
	input := estimateCompiledPromptTokens(CompiledConversation{Messages: request.Messages, Tools: request.Tools})
	if request.ContextWindowTokens <= 0 || input+int64(request.MaxTokens)+providerOutputSafetyTokens > int64(request.ContextWindowTokens) {
		t.Fatalf("compiled request exceeds budget: input=%d output=%d safety=%d window=%d", input, request.MaxTokens, providerOutputSafetyTokens, request.ContextWindowTokens)
	}
	return input
}

func TestContextBudgetE2EProtocolHTTPCompactsSelectedWindow(t *testing.T) {
	h := newContextBudgetE2EHarness(t)
	conversation := contextProjectionHighPressureConversation(t)
	conversation.ConversationID = "budget-e2e-history"
	conversation.RootConversationID = conversation.ConversationID
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	conversation.CurrentLoopStatus = "completed"
	appendEntriesInPlace(conversation, []HistoryEntry{
		testModelMessageEntry(t, 8, "request-8", modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "budget-e2e-read", Type: "function", Function: modeladapter.ToolCallFunctionShape{Name: "Read", Arguments: `{"path":"file.txt"}`}}}}),
		testModelMessageEntry(t, 8, "request-8", modeladapter.Message{Role: "tool", ToolCallID: "budget-e2e-read", Name: "Read", Content: "paired retained tool result"}),
	})
	saved, err := h.service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatal(err)
	}
	// Compare disk snapshots on both sides: SaveConversationWithEntries returns
	// compact payload JSON, whereas disk loading preserves its pretty indentation.
	before, err := h.service.store.LoadConversation(saved.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := h.service.compiler.Compile(saved, agentv1.AgentMode_AGENT_MODE_AGENT, "budget question", "unlisted-budget-e2e-model", "")
	if err != nil {
		t.Fatal(err)
	}
	originalInput := estimateCompiledPromptTokens(compiled)
	if originalInput <= 64000 {
		t.Fatalf("fixture pressure=%d must clearly exceed 32k", originalInput)
	}
	if err := h.run(t, "budget-e2e-32k", saved.ConversationID, "budget-e2e-message-32k", "budget question", "32000", true); err != nil {
		t.Fatal(err)
	}
	stream, checkpoint := h.wait(t, "budget-e2e-32k")
	if stream.Status != StreamStatusCompleted {
		t.Fatalf("32k run failed: status=%s error=%s %s", stream.Status, contextBudgetE2ETerminal(h.service, stream).TerminalErrorCode, contextBudgetE2ETerminal(h.service, stream).TerminalErrorMessage)
	}
	parents := contextBudgetE2EParentAttempts(h.gateway.snapshot())
	if len(parents) != 1 {
		t.Fatalf("parent attempts=%d, want 1; all=%#v", len(parents), h.gateway.snapshot())
	}
	request := parents[0].request
	if parents[0].err != nil {
		t.Fatal(parents[0].err)
	}
	if request.RequestedContextWindowTokens != 32000 || request.ContextWindowTokens != 32000 || !request.MaxMode {
		t.Fatalf("selected context/MaxMode lost: requested=%d effective=%d max=%t", request.RequestedContextWindowTokens, request.ContextWindowTokens, request.MaxMode)
	}
	finalInput := contextBudgetE2EAssertBudget(t, request)
	if finalInput >= originalInput/2 {
		t.Fatalf("request was not materially compacted: original=%d final=%d", originalInput, finalInput)
	}
	diagnostics, ok := request.RequestKnobs["context_projection"].(map[string]any)
	if !ok || (diagnostics["mode"] != contextProjectionModeSummary && diagnostics["mode"] != contextProjectionModeRecentTail) {
		t.Fatalf("no real summary/recent-tail projection: %#v", request.RequestKnobs)
	}
	projection, err := h.service.store.LoadContextProjection(saved.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if projection == nil || !projection.Applied || projection.ContextWindowTokens != 32000 {
		t.Fatalf("32k projection not applied: %#v", projection)
	}
	text := contextProjectionMessageText(request.Messages)
	if strings.Contains(text, "covered question 2 covered question 2") || strings.Contains(text, "covered answer 3 covered answer 3") {
		t.Fatal("covered history still present in actual parent request")
	}
	if !strings.Contains(text, "budget question") || !strings.Contains(text, "paired retained tool result") {
		t.Fatal("projection dropped current input or recent tool pair")
	}
	contextBudgetE2EAssertPairing(t, request.Messages)
	persisted, err := h.service.store.LoadConversation(saved.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Entries) < len(before.Entries) {
		t.Fatal("canonical history was cleared")
	}
	for index, entry := range before.Entries {
		if !reflect.DeepEqual(entry, persisted.Entries[index]) {
			t.Fatalf("canonical history entry %d mutated: before=%+v after=%+v", index, entry, persisted.Entries[index])
		}
	}
	if checkpoint == nil || checkpoint.GetTokenDetails().GetMaxTokens() != 32000 {
		t.Fatalf("checkpoint window is not 32k: %v", checkpoint.GetTokenDetails())
	}
	wires := h.wireSnapshot()
	if len(wires) == 0 {
		t.Fatal("no real HTTP provider request")
	}
	for index, wire := range wires {
		if wire.input+int64(wire.output)+providerOutputSafetyTokens > 32000 {
			t.Fatalf("HTTP request %d exceeds 32k including safety: input=%d output=%d", index, wire.input, wire.output)
		}
	}
	finalWire := wires[len(wires)-1]
	if finalWire.input+int64(finalWire.output)+providerOutputSafetyTokens > 32000 {
		t.Fatalf("actual serialized payload exceeds selected window including safety: input=%d output=%d", finalWire.input, finalWire.output)
	}
	if checkpoint.GetTokenDetails().GetUsedTokens() != uint32(finalWire.input) || persisted.TokenDetailsUsedTokens != uint32(finalWire.input) {
		t.Errorf("checkpoint/persisted usage not synchronized with compressed provider usage: checkpoint=%d persisted=%d wire=%d", checkpoint.GetTokenDetails().GetUsedTokens(), persisted.TokenDetailsUsedTokens, finalWire.input)
	}
	if int64(checkpoint.GetTokenDetails().GetUsedTokens()) >= originalInput/2 {
		t.Fatal("checkpoint retained canonical pre-compression usage")
	}
	t.Logf("32k MaxMode HTTP closed loop: canonical=%d final=%d wire=%d output=%d projection=%s history_entries=%d", originalInput, finalInput, finalWire.input, finalWire.output, diagnostics["mode"], len(persisted.Entries))

	// New protocol runs in the same conversation must resolve the new selection,
	// while an independent conversation must not inherit this conversation's 32k.
	for _, tc := range []struct {
		request, conversation, selection string
		want, requested                  int
	}{
		{"budget-e2e-64k", saved.ConversationID, "64000", 64000, 64000},
		{"budget-e2e-default", saved.ConversationID, "", 200000, 0},
		{"budget-e2e-other", "budget-e2e-other-conversation", "", 200000, 0},
	} {
		if err := h.run(t, tc.request, tc.conversation, tc.request+"-message", "next budget question", tc.selection, false); err != nil {
			t.Fatal(err)
		}
		next, state := h.wait(t, tc.request)
		if next.Status != StreamStatusCompleted {
			t.Fatalf("%s status=%s error=%s %s", tc.request, next.Status, contextBudgetE2ETerminal(h.service, next).TerminalErrorCode, contextBudgetE2ETerminal(h.service, next).TerminalErrorMessage)
		}
		attempts := contextBudgetE2EParentAttempts(h.gateway.snapshot())
		last := attempts[len(attempts)-1].request
		if last.RequestID != tc.request || last.ContextWindowTokens != tc.want || last.RequestedContextWindowTokens != tc.requested || state.GetTokenDetails().GetMaxTokens() != uint32(tc.want) {
			t.Fatalf("new run budget leaked: request=%s effective=%d requested=%d checkpoint=%d want=%d", last.RequestID, last.ContextWindowTokens, last.RequestedContextWindowTokens, state.GetTokenDetails().GetMaxTokens(), tc.want)
		}
		contextBudgetE2EAssertBudget(t, last)
		contextBudgetE2EAssertPairing(t, last.Messages)
		nextWires := h.wireSnapshot()
		lastWire := nextWires[len(nextWires)-1]
		if lastWire.input+int64(lastWire.output)+providerOutputSafetyTokens > int64(tc.want) {
			t.Fatalf("new run actual HTTP request exceeds context: input=%d output=%d window=%d", lastWire.input, lastWire.output, tc.want)
		}
		if tc.conversation != saved.ConversationID && strings.Contains(contextProjectionMessageText(last.Messages), "paired retained tool result") {
			t.Fatal("history leaked into independent conversation")
		}
	}
	if stream.RequestedContextWindowTokens != 32000 {
		t.Fatal("later run mutated original stream selection")
	}
}

func TestContextBudgetE2ERejectsInvalidAndUncompressibleInputBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, selection, text string
		invalid               bool
	}{
		{"invalid", "-32000", "hello", true},
		{"overflow", "2147483648", "hello", true},
		{"uncompressible_current_input", "32000", strings.Repeat("irreducible-current-input ", 9000), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newContextBudgetE2EHarness(t)
			err := h.run(t, "budget-e2e-reject", "budget-e2e-reject-conversation", "budget-e2e-reject-message", tc.text, tc.selection, true)
			if tc.invalid {
				if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("invalid selection was acknowledged as success: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("valid protocol must be admitted, then fail budget validation: %v", err)
				}
				stream, _ := h.wait(t, "budget-e2e-reject")
				if stream.Status != StreamStatusFailed {
					t.Fatalf("uncompressible current input falsely succeeded: %s", stream.Status)
				}
				if contextBudgetE2ETerminal(h.service, stream).TerminalErrorCode != compactionOverflowTerminalCode {
					t.Fatalf("failure did not describe budget overflow: %s %s", contextBudgetE2ETerminal(h.service, stream).TerminalErrorCode, contextBudgetE2ETerminal(h.service, stream).TerminalErrorMessage)
				}
			}
			if got := len(h.wireSnapshot()); got != 0 {
				t.Fatalf("oversized/invalid request reached network: %d HTTP sends", got)
			}
		})
	}
}

func TestContextBudgetE2ELocalRejectionRecoversToSmallerHTTPWindow(t *testing.T) {
	h := newContextBudgetE2EHarness(t)
	h.gateway.rejectParents = 1
	h.gateway.rejectWindow = 32000
	conversation := contextProjectionTestConversation(t, 12)
	conversation.ConversationID = "budget-e2e-recover-conversation"
	conversation.RootConversationID = conversation.ConversationID
	conversation.CurrentTurnSeq = 12
	conversation.CurrentRequestID = "request-12"
	conversation.CurrentLoopStatus = "completed"
	if _, err := h.service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "budget-e2e-recover", conversation.ConversationID, "budget-e2e-recover-message", "recover budget question", "64000", false); err != nil {
		t.Fatal(err)
	}
	stream, checkpoint := h.wait(t, "budget-e2e-recover")
	if stream.Status != StreamStatusCompleted {
		t.Fatalf("local rejection did not recover: %s %s", stream.Status, contextBudgetE2ETerminal(h.service, stream).TerminalErrorMessage)
	}
	parents := contextBudgetE2EParentAttempts(h.gateway.snapshot())
	if len(parents) != 2 || stream.ContextBudgetRetryCount != 1 {
		t.Fatalf("unexpected recovery attempts=%d retries=%d", len(parents), stream.ContextBudgetRetryCount)
	}
	var budgetErr *modeladapter.ContextBudgetExceededError
	if !errors.As(parents[0].err, &budgetErr) || parents[1].err != nil {
		t.Fatalf("typed rejection/success not preserved: first=%v second=%v", parents[0].err, parents[1].err)
	}
	request := parents[1].request
	if reflect.DeepEqual(parents[0].request.Messages, request.Messages) {
		t.Errorf("local budget rejection resent identical canonical messages after generating summary; input estimates before=%d after=%d", estimateCompiledPromptTokens(CompiledConversation{Messages: parents[0].request.Messages, Tools: parents[0].request.Tools}), estimateCompiledPromptTokens(CompiledConversation{Messages: request.Messages, Tools: request.Tools}))
	}
	if request.RequestedContextWindowTokens != 64000 || request.ContextWindowTokens != 32000 || checkpoint.GetTokenDetails().GetMaxTokens() != 32000 {
		t.Fatalf("recovery did not lower only effective context: requested=%d effective=%d checkpoint=%d", request.RequestedContextWindowTokens, request.ContextWindowTokens, checkpoint.GetTokenDetails().GetMaxTokens())
	}
	contextBudgetE2EAssertBudget(t, request)
	diagnostics, ok := request.RequestKnobs["context_projection"].(map[string]any)
	if !ok || (diagnostics["mode"] != contextProjectionModeSummary && diagnostics["mode"] != contextProjectionModeRecentTail) {
		t.Errorf("recovery did not recompile a projection: %#v", request.RequestKnobs)
	}
	wires := h.wireSnapshot()
	if len(wires) < 2 {
		t.Fatalf("expected summary and successful parent HTTP sends, got %d", len(wires))
	}
	for _, wire := range wires {
		if wire.input+int64(wire.output)+providerOutputSafetyTokens > 32000 {
			t.Fatalf("recovery HTTP request violates lowered context: input=%d output=%d", wire.input, wire.output)
		}
	}
	encoded, _ := json.Marshal(wires[len(wires)-1].body)
	if !strings.Contains(string(encoded), "recover budget question") {
		t.Fatal("successful recovery did not send the current user input")
	}
}

func TestContextBudgetE2ELocalRejectionRetriesAreBounded(t *testing.T) {
	h := newContextBudgetE2EHarness(t)
	h.gateway.rejectParents = 10
	h.gateway.rejectWindow = 32000
	conversation := contextProjectionTestConversation(t, 12)
	conversation.ConversationID = "budget-e2e-retry-conversation"
	conversation.RootConversationID = conversation.ConversationID
	conversation.CurrentTurnSeq = 12
	conversation.CurrentRequestID = "request-12"
	conversation.CurrentLoopStatus = "completed"
	if _, err := h.service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "budget-e2e-retry", conversation.ConversationID, "budget-e2e-retry-message", "retry budget question", "64000", false); err != nil {
		t.Fatal(err)
	}
	stream, _ := h.wait(t, "budget-e2e-retry")
	if stream.Status != StreamStatusFailed {
		t.Fatalf("persistent local rejection falsely succeeded: %s", stream.Status)
	}
	parents := contextBudgetE2EParentAttempts(h.gateway.snapshot())
	if len(parents) != 3 || stream.ContextBudgetRetryCount != 2 {
		t.Fatalf("local retries not bounded to two recompilations: attempts=%d retry_count=%d error=%s %s", len(parents), stream.ContextBudgetRetryCount, contextBudgetE2ETerminal(h.service, stream).TerminalErrorCode, contextBudgetE2ETerminal(h.service, stream).TerminalErrorMessage)
	}
	for index, attempt := range parents {
		var budgetErr *modeladapter.ContextBudgetExceededError
		if !errors.As(attempt.err, &budgetErr) {
			t.Fatalf("attempt %d did not preserve typed budget error: %v", index, attempt.err)
		}
		if index > 0 && attempt.request.ContextWindowTokens != 32000 {
			t.Fatalf("retry %d did not lower effective window: %d", index, attempt.request.ContextWindowTokens)
		}
		contextBudgetE2EAssertBudget(t, attempt.request)
	}
	for _, wire := range h.wireSnapshot() {
		encoded, _ := json.Marshal(wire.body)
		if strings.Contains(string(encoded), "retry budget question") {
			t.Fatal("locally rejected parent request reached HTTP")
		}
	}
	if stream.ContextBudgetCeilingTokens != 32000 {
		t.Fatalf("route ceiling = %d", stream.ContextBudgetCeilingTokens)
	}
}
