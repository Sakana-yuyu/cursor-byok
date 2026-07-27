package modeladapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripImagesKimiOpenAIShape(t *testing.T) {
	// 模拟 Cursor 发给 Kimi 的带图片用户消息
	messages := []Message{
		{Role: "user", Content: "", ContentParts: []ContentPart{
			{Type: "text", Text: "看这张图"},
			{Type: "image", Image: &ImageContent{MIMEType: "image/png", Data: []byte("fake")}},
		}},
		{Role: "user", Content: "纯文字问题"},
	}
	// kimi-k2.6 在 catalog 中 SupportsVision=false
	stripped := stripImagesFromMessages(messages, "kimi-k2.6")
	for i, m := range stripped {
		content, err := openAIContentValue(m)
		if err != nil {
			t.Fatalf("msg %d openAIContentValue error: %v", i, err)
		}
		item := map[string]any{"role": m.Role, "content": content}
		bs, _ := json.Marshal(item)
		t.Logf("msg %d -> %s", i, string(bs))
		if strings.Contains(string(bs), "image_url") {
			t.Errorf("msg %d 仍包含 image_url", i)
		}
	}
}

func TestStripImagesNoOpForVisionModel(t *testing.T) {
	messages := []Message{
		{Role: "user", ContentParts: []ContentPart{
			{Type: "image", Image: &ImageContent{MIMEType: "image/png", Data: []byte("fake")}},
		}},
	}
	// gpt-4o 支持视觉，不应被 strip
	stripped := stripImagesFromMessages(messages, "gpt-4o")
	if len(stripped) != 1 || len(stripped[0].ContentParts) != 1 || stripped[0].ContentParts[0].Type != "image" {
		t.Fatalf("视觉模型图片被错误 strip: %+v", stripped)
	}
}
