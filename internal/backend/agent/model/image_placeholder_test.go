package modeladapter

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func placeholderMessage(texts ...string) Message {
	parts := make([]ContentPart, 0, len(texts)+1)
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			parts = append(parts, ContentPart{Type: contentPartTypeText, Text: text})
		}
	}
	parts = append(parts, ContentPart{Type: contentPartTypeImage, Image: &ImageContent{MIMEType: "image/png", Data: []byte("fake")}})
	return Message{Role: "user", ContentParts: parts}
}

func TestPlaceholderImagesKeepsForVisionModel(t *testing.T) {
	messages := []Message{placeholderMessage("看图")}
	got := placeholderImagesFromMessages(context.Background(), messages, "gpt-4o")
	if len(got) != 1 || len(got[0].ContentParts) != 2 || got[0].ContentParts[1].Type != contentPartTypeImage {
		t.Fatalf("视觉模型应原样保留图片: %+v", got)
	}
}

func TestPlaceholderImagesKeepsForUnknownModel(t *testing.T) {
	messages := []Message{placeholderMessage("看图")}
	got := placeholderImagesFromMessages(context.Background(), messages, "totally-unknown")
	if got[0].ContentParts[1].Type != contentPartTypeImage {
		t.Fatalf("未知模型应保守保留图片: %+v", got)
	}
}

func TestPlaceholderImagesLocalPath(t *testing.T) {
	local := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(local, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	messages := []Message{
		{Role: "user", ContentParts: []ContentPart{
			{Type: contentPartTypeText, Text: "看图"},
			{Type: contentPartTypeImage, Image: &ImageContent{MIMEType: "image/png", Path: local}},
		}},
	}
	got := placeholderImagesFromMessages(context.Background(), messages, "deepseek-v4-flash")
	if len(got[0].ContentParts) != 2 {
		t.Fatalf("part 数异常: %+v", got[0].ContentParts)
	}
	part := got[0].ContentParts[1]
	if part.Type != contentPartTypeText {
		t.Fatalf("应替换为文本占位: %+v", part)
	}
	if !strings.Contains(part.Text, local) {
		t.Errorf("占位应含图片路径 %q: %q", local, part.Text)
	}
	if !strings.Contains(part.Text, "读图工具") {
		t.Errorf("占位应含读图工具提示: %q", part.Text)
	}
	// 占位必须同步进 message.Content（下游 openAIContentValue 无图片 part 时只读 Content）。
	if !strings.Contains(got[0].Content, local) {
		t.Errorf("message.Content 应含占位路径: %q", got[0].Content)
	}
	// 文本块保持不动
	if got[0].ContentParts[0].Text != "看图" {
		t.Errorf("文本块被改动: %q", got[0].ContentParts[0].Text)
	}
}

func TestPlaceholderImagesDataWritesTempFile(t *testing.T) {
	payload := []byte("fake-image-bytes")
	messages := []Message{placeholderMessage("看图")}
	messages[0].ContentParts[1].Image = &ImageContent{MIMEType: "image/png", Data: payload}
	got := placeholderImagesFromMessages(context.Background(), messages, "deepseek-v4-flash")
	part := got[0].ContentParts[1]
	if part.Type != contentPartTypeText {
		t.Fatalf("应替换为文本占位: %+v", part)
	}
	marker := "图片文件: "
	idx := strings.Index(part.Text, marker)
	if idx < 0 {
		t.Fatalf("占位缺少路径: %q", part.Text)
	}
	rest := part.Text[idx+len(marker):]
	path := rest[:strings.Index(rest, "]")]
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("临时图片文件应存在: %v", err)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("扩展名应为 .png: %q", path)
	}
}

func TestPlaceholderImagesDataURLWritesTempFile(t *testing.T) {
	payload := []byte("data-bytes")
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload)
	messages := []Message{placeholderMessage("看图")}
	messages[0].ContentParts[1].Image = &ImageContent{Path: dataURL}
	got := placeholderImagesFromMessages(context.Background(), messages, "deepseek-v4-flash")
	part := got[0].ContentParts[1]
	if !strings.Contains(part.Text, "图片文件: ") {
		t.Fatalf("占位缺少路径: %q", part.Text)
	}
	if strings.Contains(part.Text, "data:") {
		t.Errorf("占位不应泄露 data URL: %q", part.Text)
	}
}

func TestImageLocalPath(t *testing.T) {
	local := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(local, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("x"))
	cases := []struct {
		name    string
		image   *ImageContent
		wantErr bool
	}{
		{"nil", nil, true},
		{"empty", &ImageContent{}, true},
		{"local path", &ImageContent{Path: local}, false},
		{"data bytes", &ImageContent{MIMEType: "image/png", Data: []byte("x")}, false},
		{"data url", &ImageContent{Path: dataURL}, false},
		{"missing path", &ImageContent{Path: filepath.Join(t.TempDir(), "nope.png")}, true},
	}
	for _, tc := range cases {
		got, err := imageLocalPath(context.Background(), tc.image)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: 期望错误, got %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if _, err := os.Stat(got); err != nil {
			t.Errorf("%s: 路径不可访问: %v", tc.name, err)
		}
	}
}
