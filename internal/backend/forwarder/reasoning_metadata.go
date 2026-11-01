// reasoning_metadata.go 保存 history/replay 共用的 reasoning metadata 判定。
package forwarder

import (
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
)

func hasReplayableReasoningPayload(reasoningContent string, reasoningSignature string, reasoningSignatureSource string) bool {
	if strings.TrimSpace(reasoningContent) != "" {
		return true
	}
	return strings.TrimSpace(reasoningSignature) != "" &&
		strings.TrimSpace(reasoningSignatureSource) == modeladapter.ReasoningSignatureSourceOpenAIResponses
}

// opaqueThinkingPlaceholder 返回 provider 只回加密/隐藏思考时展示给用户的说明文本。
// 该文本只发给客户端，不进入 reasoning 累积与 replay。
func opaqueThinkingPlaceholder(reasoningSignatureSource string) string {
	if strings.TrimSpace(reasoningSignatureSource) == modeladapter.ReasoningSignatureSourceOpenAIResponses {
		return "上游未返回本轮推理摘要（OpenAI 原始推理为加密数据，无法在本地解密显示）。"
	}
	return "上游未返回本轮可读思考内容（思考已被隐藏或加密，无法在本地解密显示）。"
}
