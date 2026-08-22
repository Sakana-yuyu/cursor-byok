// imported_replay_blobs.go 承载旧会话导入的 blob 水合域：
// 新版客户端把 root_prompt_messages_json / turns 改为 32 字节内容寻址引用，
// 真实内容只存在于客户端本地 state.vscdb 的 cursorDiskKV 表（agentKv:blob:<hex>）。
// 导入侧按「内联 JSON → 请求随附 pre_fetched_blobs → 客户端磁盘 KV」的顺序水合，
// 单条缺失只跳过并计数，不再让整个 turn 以 internal_error 终止。
package forwarder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	promptengine "cursor/internal/backend/agent/prompt"
	"cursor/internal/cursor"
	"cursor/internal/logger"
)

// readCursorDiskKVBlobs 读取客户端本地磁盘 KV；变量化便于测试替换。
var readCursorDiskKVBlobs = cursor.ReadDiskKVBlobs

// enrichImportedBlobsFromDisk 把 refs 中尚缺的 32 字节内容寻址 ID 批量从
// 客户端本地磁盘 KV 水合进 store。读取失败只记录日志，不影响导入主流程。
func enrichImportedBlobsFromDisk(store importedBlobStore, refs [][]byte) importedBlobStore {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if len(ref) != sha256.Size {
			continue
		}
		if _, ok := store.resolve(ref); ok {
			continue
		}
		ids = append(ids, hex.EncodeToString(ref))
	}
	if len(ids) == 0 {
		return store
	}
	values, err := readCursorDiskKVBlobs(ids)
	if err != nil {
		logger.Infof("forwarder imported blob disk hydration skipped err=%v requested=%d", err, len(ids))
		return store
	}
	if len(values) == 0 {
		return store
	}
	if store == nil {
		store = make(importedBlobStore, len(values))
	}
	added := 0
	for id, value := range values {
		raw, err := hex.DecodeString(strings.ToLower(id))
		if err != nil || len(raw) != sha256.Size {
			continue
		}
		store[string(raw)] = append([]byte(nil), value...)
		added++
	}
	logger.Infof("forwarder imported blob disk hydration added=%d requested=%d", added, len(ids))
	return store
}

// decodeReplayBlobItems 解码 root_prompt_messages_json 条目为回放消息。
// 每条要么是内联 JSON 回放消息（旧客户端 / 本后端 checkpoint 直出格式），
// 要么是 32 字节内容寻址引用：先水合成原始 JSON，再按回放格式或客户端
// 规范分块格式（content 为 reasoning/text/tool-call/tool-result 数组）解码。
// 单条解码失败只跳过，返回跳过计数。
func decodeReplayBlobItems(items [][]byte, blobs importedBlobStore) ([]promptengine.Message, int) {
	blobs = enrichImportedBlobsFromDisk(blobs, items)
	messages := make([]promptengine.Message, 0, len(items))
	skipped := 0
	for _, raw := range items {
		if len(raw) == 0 {
			continue
		}
		if message, ok := decodeReplayMessageJSON(raw); ok {
			messages = append(messages, message)
			continue
		}
		data, resolved := blobs.resolve(raw)
		if !resolved {
			skipped++
			continue
		}
		if message, ok := decodeReplayMessageJSON(data); ok {
			messages = append(messages, message)
			continue
		}
		if message, ok := convertCanonicalBlobMessage(data); ok {
			messages = append(messages, message)
			continue
		}
		skipped++
	}
	return messages, skipped
}

// decodeReplayMessageJSON 按本仓库回放消息格式解码；content 为分块数组的
// 客户端规范格式会 Unmarshal 失败，交给 convertCanonicalBlobMessage 处理。
func decodeReplayMessageJSON(raw []byte) (promptengine.Message, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return promptengine.Message{}, false
	}
	var message promptengine.Message
	if err := json.Unmarshal(trimmed, &message); err != nil {
		return promptengine.Message{}, false
	}
	if strings.TrimSpace(message.Role) == "" {
		return promptengine.Message{}, false
	}
	if strings.TrimSpace(message.Content) == "" &&
		len(message.ContentParts) == 0 &&
		len(message.ToolCalls) == 0 &&
		strings.TrimSpace(message.ToolCallID) == "" &&
		strings.TrimSpace(message.ReasoningContent) == "" &&
		strings.TrimSpace(message.ReasoningSignature) == "" &&
		len(message.OpenAIResponsesReasoningSummary) == 0 {
		return promptengine.Message{}, false
	}
	return message, true
}

// canonicalBlobMessage 是客户端规范回放消息的分块形状。
// assistant: content = [{type:reasoning,text,signature},{type:text,text},{type:tool-call,toolCallId,toolName,args}]
// tool:      content = [{type:tool-result,toolCallId,toolName,result}]
type canonicalBlobMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Parts   []canonicalPart `json:"parts"`
}

type canonicalPart struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Signature   string          `json:"signature"`
	ToolCallID  string          `json:"toolCallId"`
	ToolName    string          `json:"toolName"`
	Args        json.RawMessage `json:"args"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result"`
	MIMEType    string          `json:"mimeType"`
	Image       json.RawMessage `json:"image"`
}

// convertCanonicalBlobMessage 把客户端规范分块消息转换为本仓库回放消息。
func convertCanonicalBlobMessage(raw []byte) (promptengine.Message, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return promptengine.Message{}, false
	}
	var payload canonicalBlobMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return promptengine.Message{}, false
	}
	role := strings.TrimSpace(payload.Role)
	if role == "" {
		return promptengine.Message{}, false
	}
	parts := payload.Parts
	if len(parts) == 0 && len(payload.Content) > 0 && payload.Content[0] == '[' {
		if err := json.Unmarshal(payload.Content, &parts); err != nil {
			return promptengine.Message{}, false
		}
	}
	if len(parts) == 0 {
		// content 为纯字符串时与回放格式同形，直接取文本。
		var text string
		if err := json.Unmarshal(payload.Content, &text); err != nil {
			return promptengine.Message{}, false
		}
		if strings.TrimSpace(text) == "" {
			return promptengine.Message{}, false
		}
		return promptengine.Message{Role: role, Content: text}, true
	}
	message := promptengine.Message{Role: role}
	contentParts := make([]promptengine.ContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			message.Content = joinReplayText(message.Content, part.Text)
		case "reasoning":
			message.ReasoningContent = joinReplayText(message.ReasoningContent, part.Text)
			if strings.TrimSpace(part.Signature) != "" {
				message.ReasoningSignature = strings.TrimSpace(part.Signature)
			}
		case "tool-call":
			arguments := canonicalArgumentsString(part.Args, part.Input)
			if strings.TrimSpace(part.ToolCallID) == "" || strings.TrimSpace(part.ToolName) == "" {
				continue
			}
			message.ToolCalls = append(message.ToolCalls, promptengine.ToolCallDescriptor{
				ID:   part.ToolCallID,
				Type: "function",
				Function: promptengine.ToolCallFunctionShape{
					Name:      part.ToolName,
					Arguments: arguments,
				},
			})
		case "tool-result":
			if strings.TrimSpace(message.ToolCallID) == "" {
				message.ToolCallID = part.ToolCallID
			}
			if strings.TrimSpace(message.Name) == "" {
				message.Name = part.ToolName
			}
			message.Content = joinReplayText(message.Content, canonicalResultString(part.Result))
		case "image":
			if image := canonicalImageContent(part); image != nil {
				contentParts = append(contentParts, promptengine.ContentPart{Type: "image", Image: image})
			}
		}
	}
	message.ContentParts = contentParts
	if strings.TrimSpace(message.Content) == "" &&
		len(message.ContentParts) == 0 &&
		len(message.ToolCalls) == 0 &&
		strings.TrimSpace(message.ToolCallID) == "" &&
		strings.TrimSpace(message.ReasoningContent) == "" &&
		strings.TrimSpace(message.ReasoningSignature) == "" {
		return promptengine.Message{}, false
	}
	return message, true
}

func canonicalArgumentsString(args json.RawMessage, input json.RawMessage) string {
	raw := args
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = input
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "{}"
	}
	var asString string
	if err := json.Unmarshal(trimmed, &asString); err == nil {
		return asString
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return string(trimmed)
	}
	return compact.String()
}

func canonicalResultString(result json.RawMessage) string {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(trimmed, &asString); err == nil {
		return asString
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return string(trimmed)
	}
	return compact.String()
}

func canonicalImageContent(part canonicalPart) *promptengine.ImageContent {
	image := &promptengine.ImageContent{MIMEType: strings.TrimSpace(part.MIMEType)}
	data := bytes.TrimSpace(part.Image)
	if len(data) == 0 {
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		image.Data = []byte(asString)
		return image
	}
	image.Data = append([]byte(nil), data...)
	return image
}

func joinReplayText(current string, addition string) string {
	if strings.TrimSpace(addition) == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return addition
	}
	return current + "\n" + addition
}
