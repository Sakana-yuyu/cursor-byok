package modeladapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrCursorAccountModelGatewayUnavailable = errors.New("Cursor 账户模型执行通道尚未完成协议验证")

// AccountModelGateway 将来承载 Cursor 账户模型的专用 relay。它与 OpenAI、Anthropic、
// Gemini 适配器刻意分离，避免 OAuth 凭据被误当作第三方 API Key。
type AccountModelGateway interface {
	Stream(context.Context, StreamRequest, func(ModelEvent) error) error
}

type unavailableAccountModelGateway struct{}

func (unavailableAccountModelGateway) Stream(_ context.Context, req StreamRequest, _ func(ModelEvent) error) error {
	modelID := strings.TrimSpace(req.ProviderModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(req.ModelID)
	}
	if modelID == "" {
		return ErrCursorAccountModelGatewayUnavailable
	}
	return fmt.Errorf("%w：模型 %s 已隔离，完成真实 RunSSE/BidiAppend 上游抓包验证后才能启用", ErrCursorAccountModelGatewayUnavailable, modelID)
}
