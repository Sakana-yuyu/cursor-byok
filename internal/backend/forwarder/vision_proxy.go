package forwarder

import (
	"context"
	"crypto/sha256"
	"cursor/internal/logger"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	// 自动触发时单个 provider pass 的识图总预算：browser 等工具会一次产生多张截图，
	// 同步识图不能无限阻塞主模型请求（期间客户端收不到任何事件，容易判定掉线）。
	// 预算耗尽后剩余图片降级为占位文本，不中断主流程。
	visionProxyPassTimeout = 90 * time.Second
	// 自动触发时单轮内最多并行识图数量，避免大量图片同时打爆识图模型。
	visionProxyMaxParallel = 3
	// 同一 request 内识图结果缓存上限；超过后整体清空（均为短生命周期条目）。
	visionProxyCacheLimit = 512
	// see_image 工具名。
	seeImageToolName = "SeeImage"
	// 注入回原消息的图片识图结果前缀（自动触发场景），明确标注来源为视觉委派。
	visionProxyResultPrefix = "[图片识图结果（视觉委派"
)

// vdbg 输出视觉委派链路调试日志：写 stderr（黑窗调试版即时可见）并进 app.log。
// 正式构建（-H windowsgui）下 stderr 被系统丢弃，只剩 app.log 中的记录，无副作用。
func vdbg(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, "[VDBG] "+msg)
	logger.Infof("vision debug: %s", msg)
}

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
// 视觉能力判定同时检查模型 ID 与显示名：客户端传入的 displayName/渠道名可能匹配不上
// 能力目录（如 "GPT-5.6 Luna" vs "gpt-5.6-luna"），任一命中即视为支持视觉，避免
// 多模态模型被误判为纯文本而重复走委派。
// 图片 content part 是否存在按 Type 判断：历史恢复后图片字节可能丢失（只剩空 part），
// synthesize 内部会通过会话内落地文件缓存补回路径或替换为强引导占位，不会空转，
// 因此这里不需要校验 Image 是否可用——任何图片 part 都必须进入识图流程，否则
// "正常开发中给图 + 叙述需求"的场景会静默不触发。
func (service *Service) needsVisionProxy(modelID string, modelName string, messages []modeladapter.Message) bool {
	config := service.resolveVisionProxyConfig()
	if !config.enabled {
		return false
	}
	if supportsVision(modelID) || supportsVision(modelName) {
		return false
	}
	return messagesContainImage(messages)
}

// messagesContainImage 检查消息列表中是否存在任何图片内容块（按 Type 判断）。
func messagesContainImage(messages []modeladapter.Message) bool {
	return modeladapter.MessagesContainImage(messages)
}

// countResolvableImageParts 统计消息中图片内容可用的图片数量。
func countResolvableImageParts(messages []modeladapter.Message) int {
	total := 0
	for _, msg := range messages {
		for _, part := range msg.ContentParts {
			if !modeladapter.IsImageContentPart(part) || part.Image == nil {
				continue
			}
			if len(part.Image.Data) > 0 || strings.TrimSpace(part.Image.Path) != "" {
				total++
			}
		}
	}
	return total
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
// 未知模型（nil）保守视为不支持视觉：图片上传给能力未知的模型可能被纯文本模型
// 拒绝（400），因此由视觉委派/路径占位兜底接管，而不是原样上传。
func supportsVision(modelName string) bool {
	vision := modelcontext.SupportsVision(modelName)
	return vision != nil && *vision
}

// synthesizeImageDescriptions 是自动触发的核心：扫描消息中的所有图片块，
// 对每张图调用识图模型取得"描述 + OCR"文本，把图片块替换为文本块。
// 同一条消息内多张图按顺序识图；不同消息间串行处理（保持消息顺序稳定）。
// 单张识图失败时该图降级为错误说明文字，不中断整轮。
// 整个 pass 注册为一次「视觉委派」运行，经首页委派任务条可见。
//
// 关键行为（针对"后续对话/chat 模型不触发识图"的修复）：
//   - 历史快照恢复后图片字节丢失（只剩 Type=image 空 part）。这里按会话内图片
//     出现顺序从「落地文件缓存」补回 Path，让后续 turn 仍能真实识图；
//   - 补不回路径的图片替换为强引导占位文本（提示模型让用户重发或描述内容），
//     绝不让图片静默丢失导致"模型没看图"。
func (service *Service) synthesizeImageDescriptions(ctx context.Context, requestID string, conversationID string, messages []modeladapter.Message, modelName string) []modeladapter.Message {
	config := service.resolveVisionProxyConfig()
	vdbg("[pass] enter request_id=%s conv=%s model=%s enabled=%v msgs=%d", requestID, conversationID, modelName, config.enabled, len(messages))
	if !config.enabled {
		return messages
	}
	if len(messages) == 0 {
		return messages
	}

	// 历史图片恢复：checkpoint 只保留 Type=image 的空 part，按会话内顺序补 Path。
	imageSeq := 0
	restored := 0
	for mi := range messages {
		for pi := range messages[mi].ContentParts {
			part := &messages[mi].ContentParts[pi]
			if !modeladapter.IsImageContentPart(*part) {
				continue
			}
			if part.Image == nil || (len(part.Image.Data) == 0 && strings.TrimSpace(part.Image.Path) == "") {
				if path := service.visionImagePathFor(conversationID, imageSeq); path != "" {
					part.Image = &modeladapter.ImageContent{Path: path}
					restored++
					vdbg("[pass] restored path seq=%d path=%s", imageSeq, path)
				} else {
					vdbg("[pass] empty image part, no cached path seq=%d", imageSeq)
				}
			}
			imageSeq++
		}
	}
	vdbg("[pass] image parts total=%d restored=%d", imageSeq, restored)

	imageCount := countResolvableImageParts(messages)
	if imageCount == 0 {
		// 没有任何可识别的图片：把空图片替换为强引导占位，让模型明确知道"有图
		// 但读不到"并采取行动（要求重发/描述），而不是静默跳过。
		return service.placeholderUnavailableImages(messages)
	}
	// 会话级归档预检：全部图片已识图过（被打断后继续 / 历史恢复场景）时，
	// 直接替换为归档文本并返回，不注册视觉委派运行、不调用识图模型——
	// 用户"继续"时是纯引用上下文，不应再次出现委派。
	if service.visionAllArchived(conversationID, messages) {
		vdbg("[pass] all images archived -> reuse context request_id=%s conv=%s images=%d", requestID, conversationID, imageCount)
		return service.visionReplaceFromArchive(conversationID, messages)
	}
	startedAt := time.Now()
	logger.Infof("forwarder vision proxy pass started request_model=%s vision_model=%s mode=%s images=%d",
		strings.TrimSpace(modelName), config.visionName, config.mode, imageCount)
	service.beginVisionRun(requestID, config, imageCount, service.visionPassIntent(messages))

	// 整个 pass 的识图总预算：synthesizeImageDescriptions 同步阻塞在 provider pass
	// 编译阶段（期间主模型请求尚未开始、客户端收不到任何事件），必须限制最长等待，
	// 否则 browser 多截图场景会把对话拖到客户端超时判定掉线。
	visionCtx, visionCancel := context.WithTimeout(ctx, visionProxyPassTimeout)

	result := make([]modeladapter.Message, len(messages))
	for i, message := range messages {
		if !modeladapter.MessageHasImage(message) {
			result[i] = message
			continue
		}
		result[i] = service.synthesizeMessageImages(visionCtx, requestID, conversationID, message, config)
	}
	visionCancel()

	status := delegation.TaskCompleted
	service.visionRunsMu.Lock()
	if run := service.visionRuns[visionRunKey(requestID)]; run != nil && run.Failed > 0 {
		status = delegation.TaskFailed
	}
	service.visionRunsMu.Unlock()
	service.finishVisionRun(requestID, status)

	// 结果残留检查：替换后若仍有可解析图片 part，说明识图结果未真正注入消息，
	// 下游主模型会拿到原始图片（可能触发其自身读图或拒绝）。
	remain := 0
	for _, msg := range result {
		for _, part := range msg.ContentParts {
			if modeladapter.IsImageContentPart(part) && part.Image != nil &&
				(len(part.Image.Data) > 0 || strings.TrimSpace(part.Image.Path) != "") {
				remain++
			}
		}
	}
	vdbg("[pass] result residual_images=%d", remain)

	logger.Infof("forwarder vision proxy pass completed request_model=%s vision_model=%s images=%d elapsed_ms=%d",
		strings.TrimSpace(modelName), config.visionName, imageCount, time.Since(startedAt).Milliseconds())
	return result
}

// placeholderUnavailableImages 把消息中图片内容不可用的 part 替换为强引导占位文本。
func (service *Service) placeholderUnavailableImages(messages []modeladapter.Message) []modeladapter.Message {
	result := make([]modeladapter.Message, len(messages))
	for i, msg := range messages {
		if !modeladapter.MessageHasImage(msg) {
			result[i] = msg
			continue
		}
		replaced := make([]modeladapter.ContentPart, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			if modeladapter.IsImageContentPart(part) {
				replaced = append(replaced, modeladapter.NewTextContentPart(visionProxyUnavailableGuide()))
				continue
			}
			replaced = append(replaced, part)
		}
		next := msg
		next.ContentParts = replaced
		// 同步 message.Content：下游 openAIContentValue 在消息没有图片 part 时只读
		// message.Content，不更新会被忽略。
		next.Content = modeladapter.CollapseTextContentParts(replaced)
		result[i] = next
	}
	return result
}

// visionProxyUnavailableGuide 是图片内容不可用时的强引导占位文本。
func visionProxyUnavailableGuide() string {
	return "[图片不可读取] 用户提供了一张图片，但当前无法读取其内容（图片数据可能已随会话历史清理）。请让用户重新发送这张图片，或请用户用文字描述图片内容。"
}

// truncateVisionUserContext 限制注入识图 prompt 的用户文本长度（单行化 + 截断）。
func truncateVisionUserContext(text string) string {
	const maxRunes = 500
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

// ---- 会话内图片落地文件缓存 ----

// visionImagePathFor 返回会话内第 seq 张图片（按出现顺序）的落地文件路径。
// 历史恢复后图片字节丢失时，用它在 synthesize 阶段补回 Path，实现跨 turn 识图。
func (service *Service) visionImagePathFor(conversationID string, seq int) string {
	if service == nil || seq < 0 {
		return ""
	}
	service.visionImageMu.Lock()
	defer service.visionImageMu.Unlock()
	files := service.visionImageFiles[strings.TrimSpace(conversationID)]
	if seq < len(files) {
		return files[seq]
	}
	return ""
}

// registerVisionImageFile 记录会话内一张图片的落地文件路径（按出现顺序追加）。
func (service *Service) registerVisionImageFile(conversationID string, path string) {
	if service == nil || strings.TrimSpace(path) == "" {
		return
	}
	service.visionImageMu.Lock()
	defer service.visionImageMu.Unlock()
	key := strings.TrimSpace(conversationID)
	service.visionImageFiles[key] = append(service.visionImageFiles[key], path)
}

// synthesizeMessageImages 处理单条消息内的所有图片块，返回替换后的消息。
// 提取消息中的文本作为用户意图（userContext）注入识图任务，让识图结果贴合用户需求。
func (service *Service) synthesizeMessageImages(ctx context.Context, requestID string, conversationID string, message modeladapter.Message, config visionProxyConfig) modeladapter.Message {
	type pendingImage struct {
		index    int
		image    *modeladapter.ImageContent
		fallback modeladapter.ContentPart
	}
	pendings := make([]pendingImage, 0)
	replaced := make([]modeladapter.ContentPart, len(message.ContentParts))
	for idx, part := range message.ContentParts {
		if modeladapter.IsImageContentPart(part) {
			if part.Image != nil && (len(part.Image.Data) > 0 || strings.TrimSpace(part.Image.Path) != "") {
				// 纯字节图片落地为临时文件并注册会话缓存：checkpoint 不持久化图片
				// 字节，后续 turn 靠这个缓存补回路径才能继续识图。
				if len(part.Image.Data) > 0 && strings.TrimSpace(part.Image.Path) == "" {
					if path, pathErr := modeladapter.ImageLocalPath(ctx, part.Image); pathErr == nil && strings.TrimSpace(path) != "" {
						part.Image.Path = path
						service.registerVisionImageFile(conversationID, path)
						vdbg("[img] data-only landed path=%s", path)
					} else {
						vdbg("[img] data-only land FAILED err=%v", pathErr)
					}
				}
				pendings = append(pendings, pendingImage{
					index:    idx,
					image:    part.Image,
					fallback: modeladapter.NewTextContentPart(visionProxyImagePlaceholder()),
				})
				vdbg("[img] pending idx=%d role=%s data_len=%d path=%q", idx, message.Role, len(part.Image.Data), strings.TrimSpace(part.Image.Path))
				continue
			}
			// 图片内容不可用（快照恢复后字节丢失且无落地缓存）：强引导占位，
			// 不透传空图片 part，避免下游序列化异常或静默丢失。
			replaced[idx] = modeladapter.NewTextContentPart(visionProxyUnavailableGuide())
			vdbg("[img] unavailable placeholder idx=%d", idx)
			continue
		}
		replaced[idx] = part
	}
	if len(pendings) == 0 {
		vdbg("[img] no pendings, return original message parts=%d", len(message.ContentParts))
		return message
	}

	// 用户意图：同一 user 消息中的文本部分（"这张图哪里改错了"等），
	// 注入识图 prompt 让识别结果与用户需求对齐。超长文本截断，避免浪费识图 token。
	userContext := ""
	if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		userContext = truncateVisionUserContext(modeladapter.CollapseTextContentParts(replaced))
	}

	// 并行识图，受 visionProxyMaxParallel 限制。
	sem := make(chan struct{}, visionProxyMaxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, item := range pendings {
		wg.Add(1)
		go func(p pendingImage) {
			defer wg.Done()
			vdbg("[goro] start idx=%d", p.index)
			defer func() {
				if r := recover(); r != nil {
					logger.Infof("forwarder vision proxy image panic recovered request_id=%s panic=%v", strings.TrimSpace(requestID), r)
					service.visionRunImageDone(requestID, fmt.Errorf("vision proxy panic: %v", r))
					mu.Lock()
					replaced[p.index] = modeladapter.NewTextContentPart(
						fmt.Sprintf("%s失败：识图内部异常）]", visionProxyResultPrefix),
					)
					mu.Unlock()
					vdbg("[goro] PANIC idx=%d panic=%v", p.index, r)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctxErr := ctx.Err(); ctxErr != nil {
				// pass 预算耗尽：降级为占位文本，不再等待识图模型。
				service.visionRunImageDone(requestID, ctxErr)
				mu.Lock()
				replaced[p.index] = modeladapter.NewTextContentPart(
					fmt.Sprintf("%s预算耗尽，未识别）]", visionProxyResultPrefix),
				)
				mu.Unlock()
				vdbg("[goro] CTX_ERR idx=%d err=%v", p.index, ctxErr)
				return
			}
			// 会话级图片归档：同一会话内同一张图（按内容哈希）已识图过时，
			// 直接复用归档文本替换图片 part，不再调识图模型，也不再让图片字节
			// 反复进入 provider 上下文（历史恢复/被打断后继续场景的关键兜底）。
			archiveKeys := service.visionArchiveKeys(conversationID, p.image)
			if text, ok := service.lookupVisionArchive(conversationID, archiveKeys); ok {
				service.visionRunImageDone(requestID, nil)
				mu.Lock()
				replaced[p.index] = modeladapter.NewTextContentPart(text)
				mu.Unlock()
				vdbg("[goro] ARCHIVE_HIT idx=%d keys=%d text_len=%d", p.index, len(archiveKeys), len(text))
				return
			}
			key := visionCacheKey(requestID, p.image)
			vdbg("[goro] describe idx=%d key=%q", p.index, key)
			description, err := service.cachedVisionDescribe(ctx, key, p.image, config, userContext)
			service.visionRunImageDone(requestID, err)
			if err != nil {
				vdbg("[goro] FAILED idx=%d err=%v", p.index, err)
			} else {
				vdbg("[goro] OK idx=%d desc_len=%d", p.index, len(description))
			}
			mu.Lock()
			if err != nil {
				logger.Errorf("forwarder vision proxy image failed vision_model=%s error=%v", config.visionName, err)
				// 委派失败时保留图片本地路径占位（MCP 兜底衔接）：让纯文本主模型仍能
				// 通过读图 MCP 工具读取该路径。路径无法落地时退化为纯失败说明。
				if path, pathErr := modeladapter.ImageLocalPath(ctx, p.image); pathErr == nil && strings.TrimSpace(path) != "" {
					replaced[p.index] = modeladapter.NewTextContentPart(fmt.Sprintf(
						"[图片识图失败（视觉委派：%s）]\n[图片文件: %s] 请使用你可用的读图工具读取该文件路径来查看图片内容，不要在工作区中搜索或猜测其他图片文件。",
						truncateErr(err.Error()), path))
				} else {
					replaced[p.index] = modeladapter.NewTextContentPart(
						fmt.Sprintf("%s失败：%s）]", visionProxyResultPrefix, truncateErr(err.Error())),
					)
				}
			} else {
				replaced[p.index] = modeladapter.NewTextContentPart(
					fmt.Sprintf("%s · 由 %s 提供）]\n%s", visionProxyResultPrefix, config.visionName, description),
				)
				// 识图成功即归档，后续 turn 直接引用，防重复委派与上下文膨胀。
				service.storeVisionArchive(conversationID, archiveKeys, replaced[p.index].Text)
			}
			mu.Unlock()
		}(item)
	}
	wg.Wait()

	newMessage := message
	newMessage.ContentParts = replaced
	// 同步 message.Content：下游 openAIContentValue 在消息没有图片 part 时只读
	// message.Content（ContentParts 会被忽略）。若不更新，识图结果文本会被丢弃，
	// 纯文本主模型只会收到原始文本、看不到识图结果（图片已替换，无法恢复）。
	newMessage.Content = modeladapter.CollapseTextContentParts(replaced)
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
// userContext 是用户随图片发出的需求文本，注入识图 prompt 让识别结果贴合用户意图。
func (service *Service) describeImageOnce(ctx context.Context, image *modeladapter.ImageContent, config visionProxyConfig, userContext string, extraQuestion ...string) (string, error) {
	if service.provider == nil {
		vdbg("[describe] provider NIL")
		return "", fmt.Errorf("provider gateway is not initialized")
	}
	payload, err := buildVisionImageContent(image)
	if err != nil {
		vdbg("[describe] build payload FAILED err=%v path=%q data_len=%d", err, strings.TrimSpace(image.Path), len(image.Data))
		return "", err
	}
	vdbg("[describe] payload ok data_len=%d mime=%s path=%q", len(payload.Data), payload.MIMEType, strings.TrimSpace(payload.Path))
	question := ""
	if len(extraQuestion) > 0 {
		question = strings.TrimSpace(extraQuestion[0])
	}
	prompt := buildVisionPrompt(config.mode, question, userContext)
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
	vdbg("[describe] StartStream call vision_id=%s model_call_id=%s", config.visionID, modelCallID)
	startedAt := time.Now()
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
		vdbg("[describe] StartStream RETURNED err=%v elapsed_ms=%d", err, time.Since(startedAt).Milliseconds())
		return "", err
	}
	description := strings.TrimSpace(accumulated)
	vdbg("[describe] StartStream ok elapsed_ms=%d accum_len=%d", time.Since(startedAt).Milliseconds(), len(accumulated))
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
// userContext：用户随这张图片发出的需求文本（主模型看到的那部分文字），
// 让识图模型知道用户在问什么，从而输出与需求相关的描述，而不是泛化描述。
func buildVisionPrompt(mode string, question string, userContext string) string {
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
	core.WriteString("\n\n请特别关注图片中用户圈画、框选、箭头、高亮或标注的区域，优先详细描述这些区域的内容与文字，这些往往是用户最关心的部分。")
	if userContext = strings.TrimSpace(userContext); userContext != "" {
		core.WriteString("\n\n用户随这张图片提出的需求是：\n" + userContext + "\n请结合该需求，重点描述图片中与需求相关的内容。")
	}
	if question = strings.TrimSpace(question); question != "" {
		core.WriteString("\n\n针对这张图片，请额外回答以下问题：\n" + question)
	}
	return core.String()
}

// errVisionProxyToolInvocation 表示识图模型意外发起了工具调用（识图子调用不应带工具）。
var errVisionProxyToolInvocation = fmt.Errorf("vision model attempted a tool invocation")

// ---- 识图结果缓存 ----

// visionCacheEntry 缓存一次识图调用的结果。
type visionCacheEntry struct {
	text string
	err  error
}

// visionCacheKey 生成识图缓存键：同一 request（同一 turn 内多个 provider pass）对同一
// 张图（按字节内容 hash）复用识图结果，避免 browser 等多截图场景在每个 pass 重复调用
// 识图模型（每次调用同步阻塞数秒，累积后会把对话拖到客户端超时）。仅缓存带字节内容的
// 图片；Path 型图片每次重新解析（文件可能变化），返回空 key 表示不缓存。
func visionCacheKey(requestID string, image *modeladapter.ImageContent) string {
	if image == nil || len(image.Data) == 0 {
		return ""
	}
	sum := sha256.Sum256(image.Data)
	return strings.TrimSpace(requestID) + "#" + hex.EncodeToString(sum[:12])
}

// ---- 会话级图片识图归档 ----

const (
	// visionArchiveMaxEntries 会话级图片识图归档上限；超限时清空整体（防进程内无限增长）。
	visionArchiveMaxEntries = 1024
)

// visionArchiveEntry 归档一条图片识图结果。
type visionArchiveEntry struct {
	text string
	at   time.Time
}

// visionArchiveDiskFile 是落盘格式：imageHash -> 识图结果。
// 文件位于 history/<conversationID>/vision-archive.json，进程重启后仍可命中，
// 避免同一会话的图片在每次重启后被重新识图。
type visionArchiveDiskFile struct {
	Entries map[string]visionArchiveDiskEntry `json:"entries"`
}

type visionArchiveDiskEntry struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const visionArchiveFileName = "vision-archive.json"

// visionArchivePath 返回会话级识图归档的落盘路径。
func (service *Service) visionArchivePath(conversationID string) string {
	if service == nil || service.store == nil {
		return ""
	}
	normalized := strings.TrimSpace(conversationID)
	if normalized == "" {
		return ""
	}
	return filepath.Join(service.store.conversationDir(normalized), visionArchiveFileName)
}

// ensureVisionArchiveLoaded 懒加载该会话的磁盘归档进内存；已加载或读取失败时静默返回。
// 读取失败（文件损坏/缺失）不阻断流程：归档仅是加速，缺失时按未归档处理一次即可。
func (service *Service) ensureVisionArchiveLoaded(conversationID string) {
	if service == nil {
		return
	}
	normalized := strings.TrimSpace(conversationID)
	if normalized == "" {
		return
	}
	path := service.visionArchivePath(normalized)
	if path == "" {
		return
	}
	service.visionArchiveMu.Lock()
	defer service.visionArchiveMu.Unlock()
	if service.visionArchiveLoaded == nil {
		service.visionArchiveLoaded = make(map[string]struct{})
	}
	if _, ok := service.visionArchiveLoaded[normalized]; ok {
		return
	}
	service.visionArchiveLoaded[normalized] = struct{}{}
	data, err := os.ReadFile(path)
	if err != nil {
		return // 文件不存在或不可读：按空归档处理
	}
	var diskFile visionArchiveDiskFile
	if err := json.Unmarshal(data, &diskFile); err != nil {
		logger.Errorf("forwarder vision archive decode failed conv=%s error=%v", normalized, err)
		return
	}
	if service.visionArchive == nil {
		service.visionArchive = make(map[string]visionArchiveEntry)
	}
	for imageHash, entry := range diskFile.Entries {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		service.visionArchive[normalized+"#"+imageHash] = visionArchiveEntry{
			text: strings.TrimSpace(entry.Text),
			at:   entry.At,
		}
	}
}

// persistVisionArchive 把该会话的归档条目写盘（尽力而为，失败仅记日志不阻断）。
func (service *Service) persistVisionArchive(conversationID string) {
	if service == nil {
		return
	}
	normalized := strings.TrimSpace(conversationID)
	if normalized == "" {
		return
	}
	path := service.visionArchivePath(normalized)
	if path == "" {
		return
	}
	service.visionArchiveMu.Lock()
	diskFile := visionArchiveDiskFile{Entries: make(map[string]visionArchiveDiskEntry, 8)}
	prefix := normalized + "#"
	for key, entry := range service.visionArchive {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		imageHash := strings.TrimPrefix(key, prefix)
		diskFile.Entries[imageHash] = visionArchiveDiskEntry{Text: entry.text, At: entry.at}
	}
	service.visionArchiveMu.Unlock()
	if len(diskFile.Entries) == 0 {
		return
	}
	data, err := json.Marshal(diskFile)
	if err != nil {
		logger.Errorf("forwarder vision archive marshal failed conv=%s error=%v", normalized, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
	}
}

// lookupVisionArchive 查询会话级识图归档；候选键任一命中即返回已归档的识图结果文本。
func (service *Service) lookupVisionArchive(conversationID string, keys []string) (string, bool) {
	if service == nil || len(keys) == 0 {
		return "", false
	}
	service.ensureVisionArchiveLoaded(conversationID)
	service.visionArchiveMu.Lock()
	defer service.visionArchiveMu.Unlock()
	for _, key := range keys {
		entry, ok := service.visionArchive[key]
		if ok && strings.TrimSpace(entry.text) != "" {
			return entry.text, true
		}
	}
	return "", false
}

// storeVisionArchive 写入会话级识图归档；空 key 或空文本不写。
// 同一张图的所有候选键都写入，保证后续以任意形态出现都能命中；同时落盘，
// 使进程重启后（同会话、同图片内容）仍可命中归档。
func (service *Service) storeVisionArchive(conversationID string, keys []string, text string) {
	if service == nil || len(keys) == 0 || strings.TrimSpace(text) == "" {
		return
	}
	service.visionArchiveMu.Lock()
	if service.visionArchive == nil {
		service.visionArchive = make(map[string]visionArchiveEntry)
	}
	if limit := service.visionArchiveLimit; limit > 0 && len(service.visionArchive) >= limit {
		service.visionArchive = make(map[string]visionArchiveEntry)
		service.visionArchiveLoaded = make(map[string]struct{})
	}
	entry := visionArchiveEntry{text: strings.TrimSpace(text), at: time.Now().UTC()}
	for _, key := range keys {
		service.visionArchive[key] = entry
	}
	service.visionArchiveMu.Unlock()
	service.persistVisionArchive(conversationID)
}

// visionAllArchived 判断消息中的所有图片是否都已在会话归档中（无需再调识图模型）。
// 用于「被打断后继续 / 历史恢复」时跳过视觉委派：图片之前已识图过，直接引用归档结果。
func (service *Service) visionAllArchived(conversationID string, messages []modeladapter.Message) bool {
	if service == nil {
		return false
	}
	found := false
	for _, msg := range messages {
		for _, part := range msg.ContentParts {
			if !modeladapter.IsImageContentPart(part) || part.Image == nil {
				continue
			}
			if len(part.Image.Data) == 0 && strings.TrimSpace(part.Image.Path) == "" {
				continue
			}
			found = true
			if _, ok := service.lookupVisionArchive(conversationID, service.visionArchiveKeys(conversationID, part.Image)); !ok {
				return false
			}
		}
	}
	return found
}

// visionArchiveKeys 生成会话级归档候选键：conversationID + 图片内容哈希。
// 与 visionCacheKey 不同，它跨 request/turn 有效，是「被打断后继续 / 历史恢复」时
// 直接引用已识图结果、避免重复委派的关键。同一张图在不同时刻可能以不同形态出现：
//   - 首次上传：携带 Data 字节（可能同时有 Path）；
//   - 历史恢复：字节丢失只剩 Path（checkpoint 不持久化图片字节）。
//
// 键选择规则（保证同一张图两种形态映射到同一键，且不同图片绝不串键）：
//   - Data 非空：只用 Data 内容哈希（字节级唯一，最可靠）；
//   - Data 为空：用 Path 指向的本地文件内容哈希；读不到内容时退回 Path 字符串哈希。
//
// 注意：Data 存在时绝不能把 Path 文件内容哈希加入候选键——落地临时文件可能因
// 并发写入出现路径冲突（同名文件被覆盖），会导致不同图片共享同一 Path 内容哈希
// 而误命中归档、串用识图结果。
func (service *Service) visionArchiveKeys(conversationID string, image *modeladapter.ImageContent) []string {
	if image == nil {
		return nil
	}
	prefix := strings.TrimSpace(conversationID)
	if prefix == "" {
		return nil
	}
	if len(image.Data) > 0 {
		sum := sha256.Sum256(image.Data)
		return []string{prefix + "#" + hex.EncodeToString(sum[:])}
	}
	path := strings.TrimSpace(image.Path)
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			sum := sha256.Sum256(data)
			return []string{prefix + "#" + hex.EncodeToString(sum[:])}
		}
	}
	sum := sha256.Sum256([]byte(path))
	return []string{prefix + "#p:" + hex.EncodeToString(sum[:])}
}

// visionReplaceFromArchive 把消息中所有图片替换为会话归档中的识图结果文本。
// 调用前必须已通过 visionAllArchived 确认所有图片均可命中归档。
func (service *Service) visionReplaceFromArchive(conversationID string, messages []modeladapter.Message) []modeladapter.Message {
	result := make([]modeladapter.Message, len(messages))
	for i, msg := range messages {
		if !modeladapter.MessageHasImage(msg) {
			result[i] = msg
			continue
		}
		replaced := make([]modeladapter.ContentPart, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			if modeladapter.IsImageContentPart(part) && part.Image != nil {
				if text, ok := service.lookupVisionArchive(conversationID, service.visionArchiveKeys(conversationID, part.Image)); ok {
					replaced = append(replaced, modeladapter.NewTextContentPart(text))
					continue
				}
			}
			replaced = append(replaced, part)
		}
		next := msg
		next.ContentParts = replaced
		next.Content = modeladapter.CollapseTextContentParts(replaced)
		result[i] = next
	}
	return result
}

// cachedVisionDescribe 带缓存执行识图调用。
func (service *Service) cachedVisionDescribe(ctx context.Context, key string, image *modeladapter.ImageContent, config visionProxyConfig, userContext string) (string, error) {
	if key != "" && service != nil {
		service.visionCacheMu.Lock()
		if entry, ok := service.visionCache[key]; ok {
			service.visionCacheMu.Unlock()
			vdbg("[cache] HIT key=%q text_len=%d err=%v", key, len(entry.text), entry.err)
			return entry.text, entry.err
		}
		service.visionCacheMu.Unlock()
	} else {
		vdbg("[cache] SKIP (empty key)")
	}
	text, err := service.describeImageOnce(ctx, image, config, userContext)
	if key != "" && service != nil {
		service.visionCacheMu.Lock()
		if len(service.visionCache) >= visionProxyCacheLimit {
			service.visionCache = make(map[string]visionCacheEntry)
		}
		service.visionCache[key] = visionCacheEntry{text: text, err: err}
		service.visionCacheMu.Unlock()
	}
	return text, err
}

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
func (service *Service) handleSeeImageToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) (err error) {
	// 工具调用链是同步执行的，任何 panic 都会冒泡崩掉 forwarder（所有活跃对话掉线）。
	// 这里把 panic 收口为工具结果文本，保证对话可继续。
	defer func() {
		if r := recover(); r != nil {
			logger.Infof("forwarder see_image tool panic recovered request_id=%s panic=%v",
				strings.TrimSpace(stream.RequestID), r)
			service.visionRunImageDone(strings.TrimSpace(stream.RequestID), fmt.Errorf("see_image panic: %v", r))
			service.finishVisionRun(strings.TrimSpace(stream.RequestID), delegation.TaskFailed)
			err = service.completeSeeImageTool(stream, invocation, fmt.Sprintf("识图内部异常：%v", r))
		}
	}()
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
	requestID := strings.TrimSpace(stream.RequestID)
	// 主模型未显式传 question 时，用最近一条用户消息文本作为识图任务的用户意图，
	// 让识图模型知道用户在问什么（如"这块改错了"），并结合圈画/标注区域作答。
	userContext := truncateVisionUserContext(strings.TrimSpace(stream.LatestUserText))
	seeImageIntent := strings.TrimSpace(args.Question)
	if seeImageIntent == "" {
		seeImageIntent = userContext
	}
	service.beginVisionRun(requestID, config, 1, seeImageIntent)
	description, err := service.describeImageOnce(context.Background(), image, visionConfig, userContext, args.Question)
	if err != nil {
		service.visionRunImageDone(requestID, err)
		service.finishVisionRun(requestID, delegation.TaskFailed)
		return service.completeSeeImageTool(stream, invocation, fmt.Sprintf("识图失败：%s", err.Error()))
	}
	service.visionRunImageDone(requestID, nil)
	service.finishVisionRun(requestID, delegation.TaskCompleted)
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

// ---- 视觉委派运行状态（首页委派任务条） ----

// visionDelegationRun 记录一次视觉委派（自动识图 pass 或 see_image 显式调用）的运行状态，
// 经 DelegationTaskSnapshots 暴露给桌面首页的「委派任务」条。与 delegate worker 不同，
// 视觉委派是即时子调用，不支持取消，因此 Cancelable 固定为 false。
type visionDelegationRun struct {
	ID              string
	ParentRequestID string
	Intent          string // 用户随图片提出的需求（委派意图），用于任务条标题
	Description     string
	ModelID         string
	ModelName       string
	Mode            string
	Status          delegation.TaskStatus
	Total           int
	Completed       int
	Failed          int
	ProgressSummary string
	Error           string
	QueuedAt        time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	UpdatedAt       time.Time
}

func visionRunKey(requestID string) string {
	return "vision:" + strings.TrimSpace(requestID)
}

// beginVisionRun 注册一次视觉委派运行；同一 request 重复调用（一轮多图）时复用已有条目并刷新图片总数。
func (service *Service) beginVisionRun(requestID string, config visionProxyConfig, total int, intent string) {
	if service == nil || total <= 0 {
		return
	}
	key := visionRunKey(requestID)
	now := time.Now().UTC()
	service.visionRunsMu.Lock()
	defer service.visionRunsMu.Unlock()
	run := service.visionRuns[key]
	if run == nil {
		run = &visionDelegationRun{
			ID:              key,
			ParentRequestID: strings.TrimSpace(requestID),
			Intent:          strings.TrimSpace(intent),
			Description:     "视觉委派识图",
			ModelID:         config.visionID,
			ModelName:       config.visionName,
			Mode:            config.mode,
			Status:          delegation.TaskRunning,
			QueuedAt:        now,
			StartedAt:       now,
			UpdatedAt:       now,
		}
		service.visionRuns[key] = run
	}
	run.Total = total
	run.Status = delegation.TaskRunning
	run.UpdatedAt = now
}

// visionPassIntent 提取本轮识图任务要服务的用户意图：取最后一条含图片的 user
// 消息中的文本部分，单行化并截断，作为任务条标题展示（如「识别截图中的报错」）。
func (service *Service) visionPassIntent(messages []modeladapter.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !modeladapter.MessageHasImage(message) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		text := strings.Join(strings.Fields(modeladapter.CollapseTextContentParts(message.ContentParts)), " ")
		if text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > 48 {
			runes = runes[:48]
		}
		return string(runes)
	}
	return ""
}

// visionRunImageDone 记录一张图片识图完成（成功或失败），更新进度摘要。并发安全。
func (service *Service) visionRunImageDone(requestID string, err error) {
	if service == nil {
		return
	}
	key := visionRunKey(requestID)
	service.visionRunsMu.Lock()
	defer service.visionRunsMu.Unlock()
	run := service.visionRuns[key]
	if run == nil {
		return
	}
	if err != nil {
		run.Failed++
		if run.Error == "" {
			run.Error = truncateErr(err.Error())
		}
	} else {
		run.Completed++
	}
	done := run.Completed + run.Failed
	remain := run.Total - done
	switch {
	case run.Total > 0 && remain == 0:
		run.ProgressSummary = fmt.Sprintf("识图完成 %d/%d", run.Completed, run.Total)
	case run.Failed > 0:
		run.ProgressSummary = fmt.Sprintf("识图成功 %d，失败 %d（共 %d）", run.Completed, run.Failed, run.Total)
	default:
		run.ProgressSummary = fmt.Sprintf("识图中 %d/%d", done, run.Total)
	}
	run.UpdatedAt = time.Now().UTC()
}

// finishVisionRun 结束一次视觉委派运行并落定终态。
func (service *Service) finishVisionRun(requestID string, status delegation.TaskStatus) {
	if service == nil {
		return
	}
	key := visionRunKey(requestID)
	service.visionRunsMu.Lock()
	defer service.visionRunsMu.Unlock()
	run := service.visionRuns[key]
	if run == nil {
		return
	}
	run.Status = status
	run.FinishedAt = time.Now().UTC()
	run.UpdatedAt = run.FinishedAt
	switch status {
	case delegation.TaskCompleted:
		run.ProgressSummary = fmt.Sprintf("识图完成 %d/%d", run.Completed, run.Total)
	case delegation.TaskFailed:
		if run.Error == "" {
			run.Error = fmt.Sprintf("%d 张图片识图失败", run.Failed)
		}
		run.ProgressSummary = fmt.Sprintf("识图失败 %d/%d", run.Completed+run.Failed, run.Total)
	}
}

// visionRunDisplayTitle 任务条标题：优先展示委派意图，无意图时回退固定描述。
func visionRunDisplayTitle(run *visionDelegationRun) string {
	if run == nil {
		return "视觉委派识图"
	}
	intent := strings.TrimSpace(run.Intent)
	if intent == "" {
		return run.Description
	}
	return fmt.Sprintf("视觉委派：%s", intent)
}

// visionDelegationSnapshots 输出视觉委派运行的首页快照，复用委派任务条的数据契约。
// 终态记录保留 nativeDelegationRetention 时长后清理。
func (service *Service) visionDelegationSnapshots() []DelegationTaskSnapshot {
	if service == nil {
		return nil
	}
	now := time.Now().UTC()
	service.visionRunsMu.Lock()
	defer service.visionRunsMu.Unlock()
	for key, run := range service.visionRuns {
		if run == nil {
			delete(service.visionRuns, key)
			continue
		}
		if delegatedStatusTerminal(run.Status) && run.UpdatedAt.Before(now.Add(-nativeDelegationRetention)) {
			delete(service.visionRuns, key)
		}
	}
	items := make([]DelegationTaskSnapshot, 0, len(service.visionRuns))
	for _, run := range service.visionRuns {
		if run == nil {
			continue
		}
		end := firstNonZeroTime(run.FinishedAt, now)
		duration := int64(0)
		if !run.StartedAt.IsZero() {
			duration = end.Sub(run.StartedAt).Milliseconds()
			if duration < 0 {
				duration = 0
			}
		}
		items = append(items, DelegationTaskSnapshot{
			ID:               run.ID,
			AggregateID:      run.ParentRequestID,
			Description:      visionRunDisplayTitle(run),
			ModelID:          run.ModelID,
			ModelName:        run.ModelName,
			WorkerRole:       "vision",
			ExecutionMode:    "vision",
			Status:           run.Status,
			ProgressSummary:  run.ProgressSummary,
			Error:            run.Error,
			ParentRequestID:  run.ParentRequestID,
			ParentExecID:     run.ID,
			GroupID:          run.ParentRequestID,
			QueuedAtUnixMS:   unixMilliseconds(run.QueuedAt),
			StartedAtUnixMS:  unixMilliseconds(run.StartedAt),
			FinishedAtUnixMS: unixMilliseconds(run.FinishedAt),
			UpdatedAtUnixMS:  unixMilliseconds(run.UpdatedAt),
			DurationMS:       duration,
			Cancelable:       false,
		})
	}
	return items
}
