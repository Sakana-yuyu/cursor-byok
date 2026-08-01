package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
	"cursor/internal/modelcontext"
)

const (
	// 视觉委派单次识图调用的最大输出 token 数。
	visionProxyMaxOutputTokens = 4000
	// 视觉委派单次识图调用超时。
	visionProxyCallTimeout = 120 * time.Second
	// 自动触发时单轮内最多并行识图数量，避免大量图片同时打爆识图模型。
	visionProxyMaxParallel = 3
	// see_image 工具名。
	seeImageToolName = "SeeImage"
	// 注入回原消息的图片识图结果前缀（自动触发场景）。
	visionProxyResultPrefix = "[图片识图结果"
)

// visionProxyConfig 是从 delegation 运行时配置派生的视觉委派参数。
type visionProxyConfig struct {
	enabled    bool
	visionID   string
	visionName string
	mode       string
}

// resolveVisionProxyConfig 从 service 持有的 delegation 配置派生视觉委派参数。
// 返回零值（enabled=false）表示未启用或未配置识图模型。
func (service *Service) resolveVisionProxyConfig() visionProxyConfig {
	if service == nil || service.multitaskDelegation == nil {
		return visionProxyConfig{}
	}
	runtime := service.multitaskDelegation.runtimeConfig()
	if !runtime.VisionDelegationEnabled {
		return visionProxyConfig{}
	}
	visionID := strings.TrimSpace(runtime.VisionModelID)
	if visionID == "" {
		return visionProxyConfig{}
	}
	return visionProxyConfig{
		enabled:    true,
		visionID:   visionID,
		visionName: resolveVisionModelName(runtime, visionID),
		mode:       normalizeVisionProxyMode(runtime.VisionMode),
	}
}

func resolveVisionModelName(runtime delegation.RuntimeConfig, visionID string) string {
	if name, ok := runtime.ModelNames[visionID]; ok {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			return trimmed
		}
	}
	return visionID
}

func normalizeVisionProxyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "describe", "ocr":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

// needsVisionProxy 判断是否需要对当前 provider pass 执行视觉委派。
// 条件：视觉委派已启用 && 配置了识图模型 && 主模型明确不支持视觉 && 消息含图片。
func (service *Service) needsVisionProxy(modelName string, messages []modeladapter.Message) bool {
	config := service.resolveVisionProxyConfig()
	if !config.enabled {
		return false
	}
	if supportsVision(modelName) {
		return false
	}
	return messagesContainImage(messages)
}

// visionProxyEnabled 返回视觉委派是否已启用（含 see_image 工具可用性）。
func (service *Service) visionProxyEnabled() bool {
	return service.resolveVisionProxyConfig().enabled
}

// filterToolDescriptorByName 从工具描述符列表中剔除指定名称的工具。
// 无法解析名称的描述符保留原样，避免误删。
func filterToolDescriptorByName(tools []json.RawMessage, name string) []json.RawMessage {
	if len(tools) == 0 || strings.TrimSpace(name) == "" {
		return tools
	}
	filtered := make([]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		extracted, err := extractToolName(tool)
		if err == nil && extracted == name {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// supportsVision 复用 modelcontext 能力目录判定模型是否支持视觉输入。
// 未知模型（nil）保守视为支持，避免对未登记模型误触发委派。
func supportsVision(modelName string) bool {
	vision := modelcontext.SupportsVision(modelName)
	if vision == nil {
		return true
	}
	return *vision
}

func messagesContainImage(messages []modeladapter.Message) bool {
	return modeladapter.MessagesContainImage(messages)
}

// synthesizeImageDescriptions 是自动触发的核心：扫描消息中的所有图片块，
// 对每张图调用识图模型取得"描述 + OCR"文本，把图片块替换为文本块。
// 同一条消息内多张图按顺序识图；不同消息间串行处理（保持消息顺序稳定）。
// 单张识图失败时该图降级为错误说明文字，不中断整轮。
func (service *Service) synthesizeImageDescriptions(ctx context.Context, messages []modeladapter.Message, modelName string) []modeladapter.Message {
	config := service.resolveVisionProxyConfig()
	if !config.enabled {
		return messages
	}
	if len(messages) == 0 {
		return messages
	}
	imageCount := countImageParts(messages)
	if imageCount == 0 {
		return messages
	}
	startedAt := time.Now()
	log.Printf("forwarder vision proxy pass started request_model=%s vision_model=%s mode=%s images=%d",
		strings.TrimSpace(modelName), config.visionName, config.mode, imageCount)

	result := make([]modeladapter.Message, len(messages))
	for i, message := range messages {
		if !modeladapter.MessageHasImage(message) {
			result[i] = message
			continue
		}
		result[i] = service.synthesizeMessageImages(ctx, message, config)
	}

	log.Printf("forwarder vision proxy pass completed request_model=%s vision_model=%s images=%d elapsed_ms=%d",
		strings.TrimSpace(modelName), config.visionName, imageCount, time.Since(startedAt).Milliseconds())
	return result
}

func countImageParts(messages []modeladapter.Message) int {
	return modeladapter.CountImageParts(messages)
}

// synthesizeMessageImages 处理单条消息内的所有图片块，返回替换后的消息。
func (service *Service) synthesizeMessageImages(ctx context.Context, message modeladapter.Message, config visionProxyConfig) modeladapter.Message {
	type pendingImage struct {
		index    int
		image    *modeladapter.ImageContent
		fallback modeladapter.ContentPart
	}
	pendings := make([]pendingImage, 0)
	replaced := make([]modeladapter.ContentPart, len(message.ContentParts))
	for idx, part := range message.ContentParts {
		if modeladapter.IsImageContentPart(part) && part.Image != nil {
			pendings = append(pendings, pendingImage{
				index:    idx,
				image:    part.Image,
				fallback: modeladapter.NewTextContentPart(visionProxyImagePlaceholder()),
			})
		}
		replaced[idx] = part
	}
	if len(pendings) == 0 {
		return message
	}

	// 并行识图，受 visionProxyMaxParallel 限制。
	sem := make(chan struct{}, visionProxyMaxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, item := range pendings {
		wg.Add(1)
		go func(p pendingImage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			description, err := service.describeImageOnce(ctx, p.image, config)
			mu.Lock()
			if err != nil {
				log.Printf("forwarder vision proxy image failed vision_model=%s error=%v", config.visionName, err)
				replaced[p.index] = modeladapter.NewTextContentPart(
					fmt.Sprintf("%s失败：%s]", visionProxyResultPrefix, truncateErr(err.Error())),
				)
			} else {
				replaced[p.index] = modeladapter.NewTextContentPart(
					fmt.Sprintf("%s（由 %s 提供）]\n%s", visionProxyResultPrefix, config.visionName, description),
				)
			}
			mu.Unlock()
		}(item)
	}
	wg.Wait()

	newMessage := message
	newMessage.ContentParts = replaced
	return newMessage
}

func visionProxyImagePlaceholder() string {
	return "[图片已省略：当前模型不支持图片输入]"
}

func truncateErr(message string) string {
	const limit = 200
	if len(message) <= limit {
		return message
	}
	return message[:limit] + "..."
}

// describeImageOnce 对单张图片发起一次识图子调用，返回识图文本。
// 内部构造一条带 [文本 prompt + 图片块] 的 user 消息发给识图模型，累积 TextDelta。
func (service *Service) describeImageOnce(ctx context.Context, image *modeladapter.ImageContent, config visionProxyConfig, extraQuestion ...string) (string, error) {
	if service.provider == nil {
		return "", fmt.Errorf("provider gateway is not initialized")
	}
	payload, err := buildVisionImageContent(image)
	if err != nil {
		return "", err
	}
	question := ""
	if len(extraQuestion) > 0 {
		question = strings.TrimSpace(extraQuestion[0])
	}
	prompt := buildVisionPrompt(config.mode, question)
	message := modeladapter.Message{
		Role: "user",
		ContentParts: []modeladapter.ContentPart{
			modeladapter.NewTextContentPart(prompt),
			modeladapter.NewImageContentPart(payload),
		},
	}
	callCtx, cancel := context.WithTimeout(ctx, visionProxyCallTimeout)
	defer cancel()

	requestID := fmt.Sprintf("vision-proxy-%d", time.Now().UnixNano())
	modelCallID := requestID + "-model"
	accumulated := ""
	err = service.provider.StartStream(callCtx, ProviderRequest{
		RequestID:      requestID,
		RunID:          requestID,
		ModelCallID:    modelCallID,
		ModelID:        config.visionID,
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		Messages:       []modeladapter.Message{message},
		Tools:          nil,
		MaxTokens:      visionProxyMaxOutputTokens,
		CompileSummary: fmt.Sprintf("vision proxy describe image vision_model=%s mode=%s", config.visionName, config.mode),
	}, func(event modeladapter.ModelEvent) error {
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			accumulated += event.Text
			return nil
		case modeladapter.ModelEventKindThinkingDelta, modeladapter.ModelEventKindThinkingCompleted, modeladapter.ModelEventKindTurnFinished:
			return nil
		case modeladapter.ModelEventKindToolLikeCompleted, modeladapter.ModelEventKindPartialToolCall, modeladapter.ModelEventKindToolCallDelta:
			return errVisionProxyToolInvocation
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return providerTerminalError{cause: event.Err}
			}
			return providerTerminalError{cause: fmt.Errorf("vision provider error")}
		default:
			return nil
		}
	})
	if err != nil {
		return "", err
	}
	description := strings.TrimSpace(accumulated)
	if description == "" {
		return "", fmt.Errorf("vision model returned empty description")
	}
	return description, nil
}

// buildVisionImageContent 把内部 ImageContent 解析为可发送给 provider 的图片内容块。
// 优先使用已有字节；当仅有 Path 且指向 http(s) URL 时，补一个简单的 HTTP fetch 兜底
// （现有 resolveImageContent 只会 os.ReadFile，对 URL 会失败）。
func buildVisionImageContent(image *modeladapter.ImageContent) (*modeladapter.ImageContent, error) {
	if image == nil {
		return nil, fmt.Errorf("image content is required")
	}
	if len(image.Data) > 0 {
		return image, nil
	}
	path := strings.TrimSpace(image.Path)
	if path == "" {
		return nil, fmt.Errorf("image content is missing data and path")
	}
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		// 本地文件路径交给底层 resolveImageContent 处理，无需预读。
		return image, nil
	}
	data, mediaType, err := fetchImageURL(path)
	if err != nil {
		return nil, err
	}
	return &modeladapter.ImageContent{
		MIMEType: mediaType,
		Data:     data,
	}, nil
}

func fetchImageURL(rawURL string) ([]byte, string, error) {
	client := &http.Client{Timeout: visionProxyCallTimeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetch image url failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch image url failed: status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, "", fmt.Errorf("read image url body failed: %w", err)
	}
	mediaType := modeladapter.NormalizeImageMIMEType("", rawURL, data)
	return data, mediaType, nil
}

// buildVisionPrompt 根据识图模式生成发给识图模型的统一指令。
// auto：画面描述 + OCR 抄录（默认，最完整）。
// describe：仅结构化描述画面内容。
// ocr：仅原样抄录可见文字 / 表格。
func buildVisionPrompt(mode string, question string) string {
	var core strings.Builder
	switch mode {
	case "describe":
		core.WriteString("请对这张图片进行结构化描述：包括主体内容、布局、颜色、明显文字的位置，以及画面传达的关键信息。")
	case "ocr":
		core.WriteString("你是精确的读图助手。请完整抄录图片中的全部可见文字（含中文、英文、数字、符号、表格）。按原样输出，不要翻译，不要总结，不要加解释。表格请用 Markdown 表格输出。若几乎无文字，再简短说明画面内容。")
	default: // auto
		core.WriteString("请按以下两点输出这张图片的内容：\n")
		core.WriteString("1. 画面描述：主体内容、布局、颜色、UI 结构或场景，以及画面传达的关键信息。\n")
		core.WriteString("2. 文字抄录（OCR）：完整抄录图中所有可见文字（含中文、英文、数字、符号）；表格用 Markdown 表格输出。若无文字则跳过此项。\n")
		core.WriteString("不要编造图中不存在的内容。")
	}
	if question != "" {
		return core.String() + "\n\n针对这张图片，请额外回答以下问题：\n" + question
	}
	return core.String()
}

// errVisionProxyToolInvocation 表示识图模型意外发起了工具调用（识图子调用不应带工具）。
var errVisionProxyToolInvocation = fmt.Errorf("vision model attempted a tool invocation")

// ---- see_image 工具入口 ----

// seeImageToolArgs 是 see_image 工具的入参契约。
type seeImageToolArgs struct {
	ImagePath string `json:"image_path"`
	Mode      string `json:"mode,omitempty"`
	Question  string `json:"question,omitempty"`
}

// handleSeeImageToolInvocation 处理主模型显式调用的 see_image 工具。
// 它读取一张本地图片（或 URL），调用识图模型识图，把文本作为工具结果回填给主模型。
// 仿照 handleGenerateImageToolInvocation 的 immediate native 收口模式。
func (service *Service) handleSeeImageToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) error {
	args, decodeErr := decodeSeeImageArgs(invocation.ArgsJSON)
	config := service.resolveVisionProxyConfig()

	if decodeErr != nil {
		return service.completeSeeImageTool(stream, invocation, fmt.Sprintf("解析 see_image 参数失败：%s", decodeErr.Error()))
	}
	if !config.enabled {
		return service.completeSeeImageTool(stream, invocation,
			"视觉委派未启用或未配置识图模型，无法识图。请在设置的「模型与委派 - 视觉委派」中指定识图模型。")
	}

	image, err := resolveSeeImageContent(args.ImagePath)
	if err != nil {
		return service.completeSeeImageTool(stream, invocation, fmt.Sprintf("读取图片失败：%s", err.Error()))
	}
	// 工具显式指定 mode 时优先于全局配置；未指定时沿用全局模式。
	visionConfig := config
	if trimmedMode := strings.TrimSpace(args.Mode); trimmedMode != "" {
		visionConfig.mode = normalizeVisionProxyMode(trimmedMode)
	}
	description, err := service.describeImageOnce(context.Background(), image, visionConfig, args.Question)
	if err != nil {
		return service.completeSeeImageTool(stream, invocation, fmt.Sprintf("识图失败：%s", err.Error()))
	}
	return service.completeSeeImageTool(stream, invocation, description)
}

func (service *Service) completeSeeImageTool(stream *ActiveStream, invocation runtimecore.ToolInvocation, resultText string) error {
	// see_image 没有专属 proto ToolCall 类型，toolCall 传 nil；模型需要的识图文本
	// 完全由 resultText 承载，appendToolResult 会把它写入会话历史的 tool_result。
	if err := service.completeImmediateToolResult(stream, invocation, resultText, nil); err != nil {
		return err
	}
	markProviderTerminalToolInvocation(stream)
	return nil
}

func decodeSeeImageArgs(raw []byte) (seeImageToolArgs, error) {
	var args seeImageToolArgs
	if len(raw) == 0 {
		return args, fmt.Errorf("image_path is required")
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("decode see_image args failed: %w", err)
	}
	args.ImagePath = strings.TrimSpace(args.ImagePath)
	args.Mode = strings.TrimSpace(args.Mode)
	args.Question = strings.TrimSpace(args.Question)
	if args.ImagePath == "" {
		return args, fmt.Errorf("image_path is required")
	}
	return args, nil
}

// resolveSeeImageContent 把 see_image 工具的 image_path 解析为可发送的图片内容块。
// 支持本地文件路径与 http(s) URL。
func resolveSeeImageContent(imagePath string) (*modeladapter.ImageContent, error) {
	if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		data, mediaType, err := fetchImageURL(imagePath)
		if err != nil {
			return nil, err
		}
		return &modeladapter.ImageContent{MIMEType: mediaType, Data: data}, nil
	}
	// 本地文件交给底层 resolveImageContent（os.ReadFile）处理。
	return &modeladapter.ImageContent{Path: imagePath}, nil
}
