// image_placeholder.go 为不支持视觉的模型提供「图片路径占位」。
// 模型本身不支持图片输入（如 DeepSeek V4 Flash）时，把图片 ContentPart 替换为
// 包含本地文件路径的文字占位，让模型通过可用的读图工具（MCP）自行读取该路径查看图片。
package modeladapter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cursor/internal/modelcontext"
)

const (
	// visionImagePlaceholderFormat 是图片路径占位的文字模板。
	// 不指定具体工具名，让模型自行查询可用读图工具，避免首猜工具名失败。
	visionImagePlaceholderFormat = "[图片文件: %s] 用户刚刚发送了这张图片。请使用你可用的读图工具读取该文件路径来查看图片内容，不要在工作区中搜索或猜测其他图片文件。"
	// visionImageUnavailableText 是图片无法落地为可访问路径时的兜底文案。
	visionImageUnavailableText = "[图片文件不可访问，已省略]"
	// visionTempImagesDir 是临时图片落地目录名（位于系统临时目录下）。
	visionTempImagesDir = "cursor-byok-images"
	// visionDownloadMaxBytes 限制 http(s) 图片下载上限，防超大文件。
	visionDownloadMaxBytes = 50 << 20
)

// placeholderImagesFromMessages 把不支持视觉模型消息里的图片替换为「本地路径占位」。
//   - 支持视觉的模型或未知模型（SupportsVision 为 nil 或 true）→ 原样保留图片；
//   - 不支持视觉的模型 → 每张图片解析为可访问的本地路径后替换为文字占位，
//     让模型通过读图工具（MCP）自行读取；无法落地路径的图片替换为兜底文案。
func placeholderImagesFromMessages(ctx context.Context, messages []Message, modelID string) []Message {
	vision := modelcontext.SupportsVision(modelID)
	if vision == nil || *vision {
		return messages
	}
	imageParts := 0
	for _, msg := range messages {
		if hasImageContentParts(msg.ContentParts) {
			imageParts++
		}
	}
	if imageParts == 0 {
		return messages
	}
	result := make([]Message, len(messages))
	for index, msg := range messages {
		if !hasImageContentParts(msg.ContentParts) {
			result[index] = msg
			continue
		}
		replaced := make([]ContentPart, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			if normalizeContentPartType(part.Type) != contentPartTypeImage {
				replaced = append(replaced, part)
				continue
			}
			path, err := imageLocalPath(ctx, part.Image)
			if err != nil || strings.TrimSpace(path) == "" {
				replaced = append(replaced, ContentPart{Type: contentPartTypeText, Text: visionImageUnavailableText})
				continue
			}
			replaced = append(replaced, ContentPart{Type: contentPartTypeText, Text: fmt.Sprintf(visionImagePlaceholderFormat, path)})
		}
		next := msg
		next.ContentParts = replaced
		// 占位文本必须同步进 message.Content：下游 openAIContentValue 在消息没有图片 part 时
		// 只读 message.Content（ContentParts 会被忽略），若不更新 Content，占位就发不到模型。
		next.Content = collapseTextContentParts(replaced)
		result[index] = next
	}
	return result
}

// imageLocalPath 返回图片可被读图工具访问的本地路径。覆盖多种形态：
//   - Data 非空 → 写入临时文件返回路径；
//   - Path 为 data: URL → 解码后写入临时文件；
//   - Path 为 http(s) URL → 下载后写入临时文件；
//   - Path 为本地路径 → 校验存在后原样返回。
func imageLocalPath(ctx context.Context, image *ImageContent) (string, error) {
	if image == nil {
		return "", errors.New("image content is required")
	}
	if len(image.Data) > 0 {
		return writeVisionTempImage(image.Data, image.MIMEType)
	}
	path := strings.TrimSpace(image.Path)
	if path == "" {
		return "", errors.New("image content is missing data and path")
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasPrefix(lower, "data:"):
		payload, mime, err := decodeVisionDataURL(path)
		if err != nil {
			return "", err
		}
		return writeVisionTempImage(payload, mime)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		payload, mime, err := downloadVisionImage(ctx, path)
		if err != nil {
			return "", err
		}
		return writeVisionTempImage(payload, mime)
	default:
		// 本地路径：校验存在且非目录。
		if info, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("stat image path: %w", err)
		} else if info.IsDir() {
			return "", fmt.Errorf("image path is a directory: %s", path)
		}
		return path, nil
	}
}

// writeVisionTempImage 把图片字节写入系统临时目录下的专用目录，返回绝对路径。
func writeVisionTempImage(payload []byte, mime string) (string, error) {
	if len(payload) == 0 {
		return "", errors.New("image payload is empty")
	}
	dir := filepath.Join(os.TempDir(), visionTempImagesDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create vision temp dir: %w", err)
	}
	ext := imageFileExtension(mime)
	name := fmt.Sprintf("img-%d%s", time.Now().UnixNano(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("write vision temp image: %w", err)
	}
	return path, nil
}

// imageFileExtension 根据 MIME 类型返回临时文件扩展名。
func imageFileExtension(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	default:
		return ".png"
	}
}

// decodeVisionDataURL 解析 data:<mime>;base64,<data> URL，返回图片字节与 MIME。
func decodeVisionDataURL(raw string) ([]byte, string, error) {
	comma := strings.Index(raw, ",")
	if comma < 0 {
		return nil, "", errors.New("invalid data URL: missing comma")
	}
	header := strings.ToLower(strings.TrimSpace(raw[len("data:"):comma]))
	mime := "image/png"
	if header != "" {
		if idx := strings.Index(header, ";"); idx >= 0 {
			header = header[:idx]
		}
		if header != "" {
			mime = header
		}
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw[comma+1:]))
	if err != nil {
		return nil, "", fmt.Errorf("decode data URL image: %w", err)
	}
	return payload, mime, nil
}

// downloadVisionImage 下载 http(s) URL 图片到内存。
func downloadVisionImage(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build image download request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("download image: http %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, visionDownloadMaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read downloaded image: %w", err)
	}
	if len(payload) > visionDownloadMaxBytes {
		return nil, "", fmt.Errorf("downloaded image exceeds %d bytes", visionDownloadMaxBytes)
	}
	mime := http.DetectContentType(payload)
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}
	return payload, mime, nil
}
