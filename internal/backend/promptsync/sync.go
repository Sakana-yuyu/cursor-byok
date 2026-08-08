// Package promptsync 实现云端原生提示词的拉取与本地缓存（A3）。
//
// 提示词由 Cursor 服务端 AiService RPC 下发（GetChatPrompt/GetSimplePrompt/
// GetPassthroughPrompt），本地客户端不含明文模板。本包用官方账号 token
// 直连 api2.cursor.sh 拉取，缓存到 appdata/native-prompts/，供渲染引擎
// 在原生模板模式下使用。拉取失败/未登录时调用方应回退自编资产。
package promptsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"cursor/gen/aiserverv1"
	"cursor/gen/aiserverv1/aiserverv1connect"
	"cursor/internal/appdata"
	"cursor/internal/backend/server/upstream"
	"cursor/internal/cursoraccount"
)

// CloudPromptMeta 是云端提示词缓存的元数据。
type CloudPromptMeta struct {
	Mode       string    `json:"mode"`
	Source     string    `json:"source"` // GetChatPrompt / GetSimplePrompt / GetPassthroughPrompt
	TokenCount int32     `json:"tokenCount,omitempty"`
	FetchedAt  time.Time `json:"fetchedAt"`
}

// FetchResult 是一次拉取的原始结果。
type FetchResult struct {
	Source     string
	Content    string
	TokenCount int32
}

// cacheDir 返回云端提示词缓存目录（appdata/native-prompts）。
func cacheDir() (string, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return "", err
	}
	return filepath.Join(appdata.DataRootPath(), "native-prompts"), nil
}

// CachePath 返回指定 mode 的缓存文件路径（prompt 正文）。
func CachePath(mode string) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, strings.TrimSpace(mode)+".md"), nil
}

func metaPath(mode string) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, strings.TrimSpace(mode)+".meta.json"), nil
}

// Save 把拉取结果写入缓存（正文 + 元数据）。失败时不清除旧缓存。
func Save(mode string, result FetchResult) error {
	mode = strings.TrimSpace(mode)
	if mode == "" || strings.TrimSpace(result.Content) == "" {
		return fmt.Errorf("save cloud prompt requires non-empty mode and content")
	}
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path, err := CachePath(mode)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(result.Content), 0o644); err != nil {
		return err
	}
	meta := CloudPromptMeta{
		Mode:       mode,
		Source:     strings.TrimSpace(result.Source),
		TokenCount: result.TokenCount,
		FetchedAt:  time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	metaFile, err := metaPath(mode)
	if err != nil {
		return err
	}
	return os.WriteFile(metaFile, raw, 0o644)
}

// Fetch 用官方账号直连 api2.cursor.sh 拉取指定 mode 的提示词。
// 依次尝试 GetChatPrompt（手写）→ GetSimplePrompt → GetPassthroughPrompt，
// 返回第一个成功的响应；全部失败时返回聚合错误。
func Fetch(ctx context.Context, mode string) (*FetchResult, error) {
	manager := cursoraccount.NewManager(
		filepath.Join(appdata.DataRootPath(), "cursor-account.json"),
		http.DefaultClient,
	)
	if !manager.SignedIn() {
		return nil, errors.New("未登录官方 Cursor 账号（promptsync 需要官方账号 token）")
	}
	authorization, err := manager.Authorization(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取官方账号认证失败: %w", err)
	}
	header := map[string]string{
		"Authorization":               authorization,
		"x-cursor-checksum":           upstream.BuildCursorChecksum(authorization),
		"User-Agent":                  "Cursor/3.14.7",
		"x-cursor-client-version":     "3.14.7",
		"x-cursor-client-commit":      "a758f2241ca99fecf380180b6cbdbbce0f1f42c0",
		"x-cursor-client-type":        "ide",
		"x-cursor-client-os":          "win32",
		"x-cursor-client-arch":        "x64",
		"x-cursor-client-os-version":  "10.0.19045",
		"x-cursor-client-device-type": "desktop",
		"x-cursor-timezone":           "Asia/Shanghai",
		"x-new-onboarding-completed":  "true",
		"x-ghost-mode":                "false",
		"origin":                      "https://cursor.com",
	}
	client := aiserverv1connect.NewAiServiceClient(
		&httpClient{headers: header},
		"https://api2.cursor.sh",
	)

	var details []string
	if result, detail := fetchChatPrompt(ctx, mode, header); result != nil {
		return result, nil
	} else if detail != "" {
		details = append(details, detail)
	}
	if resp, err := client.GetSimplePrompt(ctx, connect.NewRequest(&aiserverv1.GetSimplePromptRequest{
		Query:             "You are an expert programmer.",
		AnswerPlaceholder: "Code review request",
	})); err != nil {
		details = append(details, fmt.Sprintf("GetSimplePrompt: %v", err))
	} else if strings.TrimSpace(resp.Msg.GetResult()) != "" && !strings.Contains(resp.Msg.GetResult(), "Code review request") {
		return &FetchResult{Source: "GetSimplePrompt", Content: resp.Msg.GetResult()}, nil
	}

	// StreamPriomptPrompt：Cursor 的 Priompt 提示词组装端点（服务端流式）。
	// prompt_props_type_name 是服务端 props 类型的注册名，先用常见名尝试，
	// 服务端错误信息会提示正确的 type name。
	for _, typeName := range []string{"ChatPromptProps", "AgentPromptProps", "ChatProps"} {
		stream, err := client.StreamPriomptPrompt(ctx, connect.NewRequest(&aiserverv1.StreamPriomptPromptRequest{
			PromptProps:         "{}",
			PromptPropsTypeName: typeName,
			SkipLoginCheck:      false,
		}))
		if err != nil {
			details = append(details, fmt.Sprintf("StreamPriomptPrompt(%s): %v", typeName, err))
			continue
		}
		var parts []string
		for stream.Receive() {
			parts = append(parts, stream.Msg().GetText())
		}
		if err := stream.Err(); err != nil {
			details = append(details, fmt.Sprintf("StreamPriomptPrompt(%s) stream: %v", typeName, err))
			continue
		}
		content := strings.TrimSpace(strings.Join(parts, ""))
		if content != "" {
			return &FetchResult{Source: "StreamPriomptPrompt(" + typeName + ")", Content: content}, nil
		}
	}
	if resp, err := client.GetPassthroughPrompt(ctx, connect.NewRequest(&aiserverv1.GetPassthroughPromptRequest{
		Query:     "You are an expert programmer.",
		ModelName: "claude-3-5-sonnet-20241022",
	})); err != nil {
		details = append(details, fmt.Sprintf("GetPassthroughPrompt: %v", err))
	} else if strings.TrimSpace(resp.Msg.GetResult()) != "" {
		return &FetchResult{Source: "GetPassthroughPrompt", Content: resp.Msg.GetResult()}, nil
	}
	if len(details) == 0 {
		details = append(details, "所有端点返回空内容")
	}
	return nil, fmt.Errorf("全部提示词端点均失败: %s", strings.Join(details, "; "))
}
