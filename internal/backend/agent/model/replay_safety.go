package modeladapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrReplayUnsafeDrop 标记一次请求因「不具重放安全性」而被跳过自动重试。
// 上游可能在连接断开前已处理该请求（计费、hosted 工具、存储等副作用），
// 透明重连或跨渠道重发会导致重复计费或重复副作用，因此由调用方决定是否手动重试。
var ErrReplayUnsafeDrop = errors.New("request is not replay-safe; automatic retry skipped")

// ReplaySafety 描述一次请求能否被透明重连/重试。
type ReplaySafety struct {
	Safe   bool
	Reason string
}

func replaySafetySafe() ReplaySafety {
	return ReplaySafety{Safe: true}
}

func replaySafetyUnsafe(reason string) ReplaySafety {
	return ReplaySafety{Safe: false, Reason: reason}
}

// requestReplaySafety 基于请求内容判断是否可安全透明重发。
// 不可安全重发的场景：
//   - 请求体覆盖（RequestBodyOverride）：任何 raw override 都可能启用 store、hosted 工具或持久化。
//   - provider 托管工具：web 搜索、图片生成、computer 等由上游执行，连接断开前可能已发生副作用。
//
// 客户端工具（Read/Write/Bash 等）由本进程在前向输出后执行，不在此类风险内。
func requestReplaySafety(req StreamRequest) ReplaySafety {
	if len(req.RequestBodyOverride) > 0 {
		return replaySafetyUnsafe("请求体覆盖可能启用有状态或托管行为")
	}
	if name := hostedProviderToolName(req.Tools); name != "" {
		return replaySafetyUnsafe("请求包含 provider 托管工具 " + name)
	}
	return replaySafetySafe()
}

// replayUnsafeDropError 包装错误并附加 ErrReplayUnsafeDrop 标记，同时保留底层错误链。
func replayUnsafeDropError(safety ReplaySafety, err error) error {
	return fmt.Errorf("%w (reason: %s): %w", ErrReplayUnsafeDrop, safety.Reason, err)
}

// hostedProviderToolName 返回请求中第一个由 provider 托管执行的工具名（小写）；无则返回空串。
func hostedProviderToolName(tools []json.RawMessage) string {
	for _, raw := range tools {
		if name := hostedProviderToolNameFromRaw(raw); name != "" {
			return name
		}
	}
	return ""
}

func hostedProviderToolNameFromRaw(raw json.RawMessage) string {
	var descriptor struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(descriptor.Function.Name))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(descriptor.Type))
	}
	switch name {
	case "web_search", "web_search_preview", "web_search_2024_06_17", "web_search_2025_03_11",
		"hosted_web_search", "image_generation", "image", "image_gen", "gpt_image",
		"computer", "computer_use", "computer_use_preview", "computer_2024_09_13", "computer_2025_01_14", "computer_2025_03_11",
		"code_interpreter", "container", "hosted", "browser":
		return name
	}
	return ""
}
