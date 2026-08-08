package modeladapter

// protocolMessageText 提取消息的纯文本内容：优先完整 Content，其次折叠 ContentParts。
// 供 Gemini 适配器将 canonical 消息转换为 Gemini 请求体时使用。
func protocolMessageText(message Message) string {
	if message.Content != "" {
		return message.Content
	}
	return collapseTextContentParts(message.ContentParts)
}
