package promptsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpClient 是带固定认证头的 http.Client 包装，供 connect-go 客户端使用。
type httpClient struct {
	headers map[string]string
}

func (c *httpClient) Do(req *http.Request) (*http.Response, error) {
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	return http.DefaultClient.Do(req)
}

// fetchChatPrompt 手写调用 GetChatPrompt（RPC 未在提取的 proto service 中，
// 路径/字段按 GetChatPromptResponse{prompt,token_count} 推断）。
// 成功时返回结果与空 detail；失败时返回 nil 与聚合的失败详情。
func fetchChatPrompt(ctx context.Context, mode string, header map[string]string) (*FetchResult, string) {
	payload := map[string]any{
		"model": map[string]any{
			"id":       "claude-3-5-sonnet-20241022",
			"provider": "anthropic",
		},
		"mode":           mode,
		"conversationId": "",
		"chatContext":    map[string]any{},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Sprintf("GetChatPrompt marshal: %v", err)
	}
	var details []string
	for _, path := range []string{
		"https://api2.cursor.sh/cursor.aiserver.v1.AiService/GetChatPrompt",
		"https://api2.cursor.sh/aiserver.v1.AiService/GetChatPrompt",
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
		if err != nil {
			continue
		}
		for key, value := range header {
			req.Header.Set(key, value)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			details = append(details, fmt.Sprintf("GetChatPrompt@%s: %v", path, err))
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			details = append(details, fmt.Sprintf("GetChatPrompt@%s status=%d body=%s", path, resp.StatusCode, truncateBody(string(data))))
			continue
		}
		var parsed struct {
			Prompt     string `json:"prompt"`
			TokenCount int32  `json:"tokenCount"`
		}
		if err := json.Unmarshal(data, &parsed); err == nil && strings.TrimSpace(parsed.Prompt) != "" {
			return &FetchResult{
				Source:     fmt.Sprintf("GetChatPrompt@%s", path),
				Content:    parsed.Prompt,
				TokenCount: parsed.TokenCount,
			}, ""
		}
		details = append(details, fmt.Sprintf("GetChatPrompt@%s status=%d unexpected=%s", path, resp.StatusCode, truncateBody(string(data))))
	}
	return nil, strings.Join(details, "; ")
}

func truncateBody(text string) string {
	if len(text) <= 300 {
		return text
	}
	return text[:300] + "..."
}