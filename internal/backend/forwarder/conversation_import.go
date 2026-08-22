// conversation_import.go 承载会话导入域：外部 conversation state 转 history entries、
// model messages 解码与 runtime state 载荷提取。
package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/logger"
)

func (service *Service) importConversationState(item *ConversationFile, state *agentv1.ConversationStateStructure, prefetchedBlobs []*agentv1.PreFetchedBlob) ([]HistoryEntry, error) {
	if item == nil || state == nil {
		return nil, nil
	}
	blobs, err := newImportedBlobStore(prefetchedBlobs)
	if err != nil {
		return nil, err
	}
	// 新版客户端把 root_prompt_messages_json / turns 全部换成了内容寻址引用，
	// 请求本身不再携带内联 JSON；先从客户端本地磁盘 KV 批量水合再解码。
	blobs = enrichImportedBlobsFromDisk(blobs, state.GetRootPromptMessagesJson())
	blobs = enrichImportedBlobsFromDisk(blobs, state.GetTurns())
	importedIDs, err := importedTurnIDs(state.GetTurns(), blobs)
	if err != nil {
		return nil, err
	}
	item.TokenDetailsUsedTokens = state.GetTokenDetails().GetUsedTokens()
	item.ImportedTurnIDs = importedIDs
	if minimumNextTurnSeq := int64(len(item.ImportedTurnIDs)) + 1; item.NextTurnSeq < minimumNextTurnSeq {
		item.NextTurnSeq = minimumNextTurnSeq
	}
	entries := make([]HistoryEntry, 0, 2)
	if messages, err := importedConversationStateModelMessages(state, blobs); err != nil {
		return nil, err
	} else {
		for _, message := range messages {
			entry, ok, err := newModelMessageEntry(0, "", message)
			if err != nil {
				return nil, err
			}
			if ok {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		summary, ok, err := importedConversationStateSummary(state)
		if err != nil {
			return nil, err
		}
		if ok {
			payload, err := json.Marshal(compactionSummaryEntryPayload{
				Summary: strings.TrimSpace(summary),
				Trigger: "imported_conversation_state",
			})
			if err != nil {
				return nil, fmt.Errorf("encode imported summary context: %w", err)
			}
			entries = append(entries, HistoryEntry{
				TurnSeq: 0,
				Role:    "system",
				Kind:    "compacted_summary",
				Payload: payload,
			})
		}
	}
	runtimeState, ok, err := runtimeStatePayloadFromConversationState(state)
	if err != nil {
		return nil, err
	}
	if ok {
		payload, err := json.Marshal(runtimeState)
		if err != nil {
			return nil, fmt.Errorf("encode imported runtime state context: %w", err)
		}
		entries = append(entries, HistoryEntry{
			TurnSeq: 0,
			Role:    "system",
			Kind:    "runtime_state",
			Payload: payload,
		})
	}
	return entries, nil
}

func importedConversationStateModelMessages(state *agentv1.ConversationStateStructure, blobs importedBlobStore) ([]modeladapter.Message, error) {
	if state == nil {
		return nil, nil
	}
	if len(state.GetRootPromptMessagesJson()) > 0 {
		decoded, skipped := decodeReplayBlobItems(state.GetRootPromptMessagesJson(), blobs)
		if skipped > 0 {
			logger.Infof("forwarder imported replay messages hydrated with gaps total=%d skipped=%d", len(state.GetRootPromptMessagesJson()), skipped)
		}
		// restoreImportedReplayUserMessages 需要解码 turn 及其内嵌的 user_message 引用，
		// 先把两层引用批量水合。
		blobs = enrichImportedBlobsFromDisk(blobs, state.GetTurns())
		blobs = enrichImportedBlobsFromDisk(blobs, importedTurnsNestedRefs(state.GetTurns(), blobs))
		decoded = restoreImportedReplayUserMessages(decoded, state.GetTurns(), blobs)
		decoded = filterLegacyPlainWriteReplay(decoded)
		decoded = filterInternalPromptContextReplay(decoded)
		messages := make([]modeladapter.Message, 0, len(decoded))
		for _, item := range decoded {
			messages = append(messages, toModelMessage(item))
		}
		return normalizeReplayMessageSequence(messages), nil
	}
	if len(state.GetSummary()) > 0 {
		return nil, nil
	}
	if len(state.GetTurns()) == 0 {
		return nil, nil
	}
	messages := make([]modeladapter.Message, 0, len(state.GetTurns())*2)
	blobs = enrichImportedBlobsFromDisk(blobs, state.GetTurns())
	blobs = enrichImportedBlobsFromDisk(blobs, importedTurnsNestedRefs(state.GetTurns(), blobs))
	skippedTurns := 0
	for _, rawTurn := range state.GetTurns() {
		if len(rawTurn) == 0 {
			continue
		}
		turn, turnID, err := decodeImportedTurn(rawTurn, blobs)
		if err != nil {
			return nil, err
		}
		if turn == nil && len(turnID) > 0 {
			skippedTurns++
			continue
		}
		turnMessages, err := importedBlobTurnMessages(turn, blobs)
		if err != nil {
			return nil, err
		}
		messages = append(messages, turnMessages...)
	}
	if skippedTurns > 0 {
		logger.Infof("forwarder imported turn blobs hydrated with gaps total=%d skipped=%d", len(state.GetTurns()), skippedTurns)
		if len(messages) == 0 {
			// 全部 turn 都未能水合：导入会「成功」但续聊没有任何历史上下文，
			// 用户感知是模型失忆而非报错，必须留下 error 级痕迹便于排查。
			logger.Warnf("forwarder imported conversation lost all history: total_turns=%d skipped_turns=%d (disk kv unavailable or blob keys missing)",
				len(state.GetTurns()), skippedTurns)
		}
	}
	return normalizeReplayMessageSequence(messages), nil
}

// importedTurnNestedRefs 收集 turn 内部仍以内容寻址引用的 user_message / step 条目，
// 供水合器批量从客户端磁盘 KV 解析。
func importedTurnNestedRefs(turn *agentv1.ConversationTurnStructure) [][]byte {
	agentTurn := turn.GetAgentConversationTurn()
	if agentTurn == nil {
		return nil
	}
	refs := make([][]byte, 0, 1+len(agentTurn.GetSteps()))
	if len(agentTurn.GetUserMessage()) > 0 {
		refs = append(refs, agentTurn.GetUserMessage())
	}
	refs = append(refs, agentTurn.GetSteps()...)
	return refs
}

// importedTurnsNestedRefs 解码整批 turn 后收集全部嵌套引用。
func importedTurnsNestedRefs(turns [][]byte, blobs importedBlobStore) [][]byte {
	var refs [][]byte
	for _, rawTurn := range turns {
		if len(rawTurn) == 0 {
			continue
		}
		turn, _, err := decodeImportedTurn(rawTurn, blobs)
		if err != nil || turn == nil {
			continue
		}
		refs = append(refs, importedTurnNestedRefs(turn)...)
	}
	return refs
}

func importedConversationStateSummary(state *agentv1.ConversationStateStructure) (string, bool, error) {
	if state == nil || len(state.GetSummary()) == 0 {
		return "", false, nil
	}
	item := &agentv1.ConversationSummary{}
	if err := proto.Unmarshal(state.GetSummary(), item); err != nil {
		return "", false, fmt.Errorf("decode imported summary: %w", err)
	}
	text := strings.TrimSpace(item.GetSummary())
	return text, text != "", nil
}

func newModelMessageEntry(turnSeq int64, requestID string, message modeladapter.Message) (HistoryEntry, bool, error) {
	message.Role = strings.TrimSpace(message.Role)
	if message.Role == "" {
		return HistoryEntry{}, false, nil
	}
	if strings.TrimSpace(message.Content) == "" &&
		len(message.ContentParts) == 0 &&
		len(message.ToolCalls) == 0 &&
		strings.TrimSpace(message.ToolCallID) == "" &&
		!hasReplayableReasoningPayload(message.ReasoningContent, message.ReasoningSignature, message.ReasoningSignatureSource) &&
		len(message.OpenAIResponsesReasoningSummary) == 0 {
		return HistoryEntry{}, false, nil
	}
	payload, err := json.Marshal(modelMessageEntryPayload{Message: message})
	if err != nil {
		return HistoryEntry{}, false, fmt.Errorf("encode imported model message context: %w", err)
	}
	return HistoryEntry{
		TurnSeq:   turnSeq,
		RequestID: strings.TrimSpace(requestID),
		Role:      message.Role,
		Kind:      "model_message",
		Payload:   payload,
	}, true, nil
}

func runtimeStatePayloadFromConversationState(state *agentv1.ConversationStateStructure) (runtimeStateEntryPayload, bool, error) {
	if state == nil {
		return runtimeStateEntryPayload{}, false, nil
	}
	payload := runtimeStateEntryPayload{
		Plans: clonePlanRegistryEntries(state.GetPlans()),
	}
	if len(state.GetPlan()) > 0 {
		plan := &agentv1.ConversationPlan{}
		if err := proto.Unmarshal(state.GetPlan(), plan); err != nil {
			return runtimeStateEntryPayload{}, false, fmt.Errorf("decode imported plan: %w", err)
		}
		payload.PlanText = strings.TrimSpace(plan.GetPlan())
	}
	if len(state.GetTodos()) > 0 {
		todos := make([]*agentv1.TodoItem, 0, len(state.GetTodos()))
		for _, raw := range state.GetTodos() {
			if len(raw) == 0 {
				continue
			}
			item := &agentv1.TodoItem{}
			if err := proto.Unmarshal(raw, item); err != nil {
				return runtimeStateEntryPayload{}, false, fmt.Errorf("decode imported todo: %w", err)
			}
			todos = append(todos, cloneTodoItem(item))
		}
		payload.Todos = todos
	}
	ok := strings.TrimSpace(payload.PlanText) != "" || len(payload.Plans) > 0 || len(payload.Todos) > 0
	return payload, ok, nil
}
