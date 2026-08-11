package forwarder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	"cursor/internal/logger"
	"cursor/internal/safego"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type debugLogConfig interface {
	IsObservabilityLogEnabled(context.Context) bool
	// DebugLogMaxBytes 返回单个 debug jsonl 文件的字节上限。返回 0 表示用默认值，
	// 负数表示不限制。 recorder 在写盘后据此裁剪文件，保留尾部（错误附近）。
	DebugLogMaxBytes(context.Context) int
}

// debugWriteJob 是一次待落盘的 debug 日志事件。
type debugWriteJob struct {
	dir      string
	filename string
	payload  []byte
	// epoch 是入队时的清理世代号。若落盘时世代号已推进（期间发生过清理），
	// 这条事件会被丢弃，避免把刚删掉的 debug 目录重建回来。
	epoch uint64
}

// debugQueueCapacity 是 debug 写盘队列容量。写盘是尽力而为的证据层，队列满时丢弃
// 新事件（限频日志提示），绝不让日志拖垮主链路（此前同步写盘 + 全局互斥导致
// BidiAppend 接口超时与 provider 事件流吞吐下降，见 13:36 的 timeout 日志）。
const debugQueueCapacity = 8192

type debugRecorder struct {
	historyRoot string
	broker      *StreamBroker
	config      debugLogConfig
	queue       chan debugWriteJob
	workerOnce  sync.Once
	health      debugRecorderHealth
}

type debugRecorderHealth struct {
	MarshalFailures atomic.Uint64
	WriteFailures   atomic.Uint64
	DroppedEvents   atomic.Uint64
	WorkerPanics    atomic.Uint64
}

func (recorder *debugRecorder) healthSnapshot() debugRecorderHealthSnapshot {
	if recorder == nil {
		return debugRecorderHealthSnapshot{}
	}
	return debugRecorderHealthSnapshot{
		MarshalFailures: recorder.health.MarshalFailures.Load(),
		WriteFailures:   recorder.health.WriteFailures.Load(),
		DroppedEvents:   recorder.health.DroppedEvents.Load(),
		WorkerPanics:    recorder.health.WorkerPanics.Load(),
	}
}

type debugRecorderHealthSnapshot struct {
	MarshalFailures uint64
	WriteFailures   uint64
	DroppedEvents   uint64
	WorkerPanics    uint64
}

func (recorder *debugRecorder) recordWriteFailure(operation string, filename string) {
	if recorder == nil {
		return
	}
	recorder.health.WriteFailures.Add(1)
	logger.Error("debug recorder write failed", "operation", operation, "filename", strings.TrimSpace(filename))
}

const maxDebugProtoPayloadBytes = 64 * 1024

// 以下常量与 config 包的 Default* 保持一致。forwarder 通过 debugLogConfig 接口
// 拿到运行时上限，这里只是「配置未提供/为 0」时的兜底默认值，避免 forwarder
// 反向依赖 config 包。
const (
	configDefaultDebugLogMaxBytes = 50 * 1024 * 1024
	configMinDebugLogReserveBytes = 256 * 1024
)

func newDebugRecorder(historyRoot string, broker *StreamBroker, config debugLogConfig) *debugRecorder {
	return &debugRecorder{
		historyRoot: strings.TrimSpace(historyRoot),
		broker:      broker,
		config:      config,
	}
}

func (recorder *debugRecorder) enabled(ctx context.Context) bool {
	if recorder == nil || recorder.config == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return recorder.config.IsObservabilityLogEnabled(ctx)
}

func (recorder *debugRecorder) LogBidiRaw(ctx context.Context, requestID string, conversationID string, appendSeqno int64, dataHex string, status string, extra map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	event := recorder.baseEvent("bidi_raw", requestID, conversationID)
	event["direction"] = "client_to_backend"
	event["procedure"] = "/aiserver.v1.BidiService/BidiAppend"
	event["append_seqno"] = appendSeqno
	event["status"] = strings.TrimSpace(status)
	// data_hex 截断到 maxDebugProtoPayloadBytes：大 payload（如多文件并行读取的工具
	// 结果）完整写盘会放大磁盘占用并拖慢落盘 worker，data_len 保留完整长度信息。
	event["data_hex"] = truncateDebugPayload(dataHex)
	event["data_len"] = len(dataHex)
	for key, value := range extra {
		event[key] = value
	}
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, "bidi.raw.jsonl", event)
}

// truncateDebugPayload 把超长 hex/文本截断到 maxDebugProtoPayloadBytes。
func truncateDebugPayload(value string) string {
	if len(value) <= maxDebugProtoPayloadBytes {
		return value
	}
	return value[:maxDebugProtoPayloadBytes]
}

func (recorder *debugRecorder) LogBidiDecoded(ctx context.Context, requestID string, conversationID string, appendSeqno int64, clientKind string, message *agentv1.AgentClientMessage, intent InboundIntent, extra map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	event := recorder.baseEvent("bidi_decoded", requestID, conversationID)
	event["schema_version"] = 2
	event["append_seqno"] = appendSeqno
	event["client_kind"] = strings.TrimSpace(clientKind)
	event["message_case"] = agentClientMessageCase(message)
	event["message"] = protoJSONDebugPayload(message)
	event["intent"] = inboundIntentDebugPayload(intent)
	if requestedModel := requestedModelDebugPayload(message); requestedModel != nil {
		event["requested_model"] = requestedModel
	}
	if actionCase := conversationActionCase(message); actionCase != "" {
		event["conversation_action"] = actionCase
	}
	for key, value := range extra {
		event[key] = value
	}
	recorder.appendJSONLEnabled(ctx, requestID, firstNonEmpty(conversationID, intent.ConversationID), "bidi.decoded.jsonl", event)
}

func (recorder *debugRecorder) LogRuntime(ctx context.Context, requestID string, conversationID string, eventName string, fields map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	event := recorder.baseEvent("runtime", requestID, conversationID)
	event["event"] = strings.TrimSpace(eventName)
	for key, value := range fields {
		event[key] = value
	}
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, "runtime.jsonl", event)
}

// LogRuntimeLazy 只在观测日志启用时构造字段，避免关闭日志时影响流式热路径。
func (recorder *debugRecorder) LogRuntimeLazy(ctx context.Context, requestID string, conversationID string, eventName string, buildFields func() map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	var fields map[string]any
	if buildFields != nil {
		fields = buildFields()
	}
	if len(fields) == 0 {
		return
	}
	event := recorder.baseEvent("runtime", requestID, conversationID)
	event["event"] = strings.TrimSpace(eventName)
	for key, value := range fields {
		event[key] = value
	}
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, "runtime.jsonl", event)
}

func (recorder *debugRecorder) LogRunSSE(ctx context.Context, requestID string, conversationID string, eventName string, fields map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	event := recorder.baseEvent("runsse", requestID, conversationID)
	event["event"] = strings.TrimSpace(eventName)
	for key, value := range fields {
		event[key] = value
	}
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, "runsse.jsonl", event)
}

// runSSEMessageDebugFields 仅记录 RunSSE 消息的协议类别、大小与正文增量摘要。
// RunSSE 位于用户可见输出热路径，不能为调试日志同步执行 protobuf JSON 编码和反解码。
func runSSEMessageDebugFields(cursor int, message *agentv1.AgentServerMessage) map[string]any {
	fields := map[string]any{
		"cursor":       cursor,
		"message_case": agentServerMessageCase(message),
		"message_size": proto.Size(message),
	}
	if textDelta := message.GetInteractionUpdate().GetTextDelta(); textDelta != nil {
		text := textDelta.GetText()
		if text != "" {
			sum := sha256.Sum256([]byte(text))
			fields["text_delta_bytes"] = len([]byte(text))
			fields["text_delta_sha256"] = hex.EncodeToString(sum[:])
		}
	}
	return fields
}

func (recorder *debugRecorder) LogProvider(ctx context.Context, requestID string, conversationID string, eventName string, fields map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	event := recorder.baseEvent("provider", requestID, conversationID)
	event["event"] = strings.TrimSpace(eventName)
	for key, value := range fields {
		event[key] = value
	}
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, "provider.jsonl", event)
}

func (recorder *debugRecorder) LogProviderArtifact(ctx context.Context, requestID string, conversationID string, modelCallID string, eventName string, payload map[string]any) {
	if !recorder.enabled(ctx) {
		return
	}
	event := recorder.baseEvent("provider", requestID, conversationID)
	event["event"] = strings.TrimSpace(eventName)
	event["model_call_id"] = strings.TrimSpace(modelCallID)
	event["payload"] = payload
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, "provider.jsonl", event)
}

func (recorder *debugRecorder) baseEvent(layer string, requestID string, conversationID string) map[string]any {
	resolvedConversationID := firstNonEmpty(strings.TrimSpace(conversationID), recorder.conversationIDForRequest(requestID))
	return map[string]any{
		"schema_version":  1,
		"at":              time.Now().UTC().Format(time.RFC3339Nano),
		"layer":           strings.TrimSpace(layer),
		"request_id":      strings.TrimSpace(requestID),
		"conversation_id": resolvedConversationID,
	}
}

func (recorder *debugRecorder) appendJSONL(ctx context.Context, requestID string, conversationID string, filename string, event map[string]any) {
	if !recorder.enabled(ctx) || len(event) == 0 {
		return
	}
	recorder.appendJSONLEnabled(ctx, requestID, conversationID, filename, event)
}

func (recorder *debugRecorder) appendJSONLEnabled(ctx context.Context, requestID string, conversationID string, filename string, event map[string]any) {
	if recorder == nil || len(event) == 0 {
		return
	}
	dir := recorder.debugDir(requestID, conversationID)
	if strings.TrimSpace(dir) == "" {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		recorder.health.MarshalFailures.Add(1)
		logger.Error("debug recorder marshal failed", "filename", strings.TrimSpace(filename))
		return
	}
	// 异步落盘：序列化后的 payload 投递到后台 worker，主链路（BidiAppend / provider
	// 事件流 / RunSSE）不再被磁盘 IO 阻塞，也不被全局互斥串行化。
	recorder.enqueue(debugWriteJob{dir: dir, filename: filename, payload: payload, epoch: debugPurge.currentEpoch()})
}

// enqueue 把一条 debug 事件投递到写盘队列；队列满时丢弃并限频提示。
func (recorder *debugRecorder) enqueue(job debugWriteJob) {
	if recorder == nil {
		return
	}
	recorder.workerOnce.Do(func() {
		recorder.queue = make(chan debugWriteJob, debugQueueCapacity)
		safego.GoWithPanicHandler("forwarder:debug-recorder", recorder.writeLoop, func(error) {
			recorder.health.WorkerPanics.Add(1)
			logger.Error("debug recorder worker panic recovered")
		})
	})
	select {
	case recorder.queue <- job:
	default:
		recorder.health.DroppedEvents.Add(1)
		logger.Warn("debug recorder queue full, dropping event", "filename", strings.TrimSpace(job.filename))
	}
}

// writeLoop 是 debug 落盘 worker：单 goroutine 串行写文件，天然保序。
func (recorder *debugRecorder) writeLoop() {
	for job := range recorder.queue {
		recorder.writeJob(job)
	}
}

// writeJob 落盘一条 debug 事件。写盘期间持有清理闸门的读锁，保证不会与
// 「清理调试日志」并发；世代号落后的事件直接丢弃，不再重建已删除的目录。
func (recorder *debugRecorder) writeJob(job debugWriteJob) {
	if !debugPurge.beginWrite(job.epoch) {
		return
	}
	defer debugPurge.endWrite()
	if err := os.MkdirAll(job.dir, 0o755); err != nil {
		recorder.recordWriteFailure("mkdir", job.filename)
		return
	}
	path := filepath.Join(job.dir, job.filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		recorder.recordWriteFailure("open", job.filename)
		return
	}
	payload := append(job.payload, '\n')
	written, writeErr := file.Write(payload)
	if writeErr != nil || written != len(payload) {
		recorder.recordWriteFailure("write", job.filename)
	}
	if closeErr := file.Close(); closeErr != nil {
		recorder.recordWriteFailure("close", job.filename)
	}
	if writeErr != nil || written != len(payload) {
		return
	}
	// 写完检查大小，超上限则裁剪保留尾部（最新 = 最可能含错误的部分）。
	// 在 worker goroutine 内执行，不阻塞主链路。
	recorder.rotateIfNeeded(context.Background(), path)
}

// rotateIfNeeded 在文件超过配置的字节上限时，保留尾部 reserve 字节并丢弃头部，
// 防止长会话的 debug 日志无限膨胀。裁剪从行边界开始（避免截断半行 JSON）。
func (recorder *debugRecorder) rotateIfNeeded(ctx context.Context, path string) {
	maxBytes := recorder.maxBytes(ctx)
	if maxBytes <= 0 {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= int64(maxBytes) {
		return
	}
	reserve := debugLogReserveBytesFor(maxBytes)
	trimDebugFileTail(path, reserve)
}

// maxBytes 解析配置的上限：0/正数直接用（0 视为默认值），负数表示不限制。
func (recorder *debugRecorder) maxBytes(ctx context.Context) int {
	if recorder == nil || recorder.config == nil {
		return configDefaultDebugLogMaxBytes
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value := recorder.config.DebugLogMaxBytes(ctx)
	if value < 0 {
		return -1 // 不限制
	}
	if value == 0 {
		return configDefaultDebugLogMaxBytes
	}
	return value
}

// trimDebugFileTail 把文件裁剪到约 reserve 字节的尾部，从行边界开始切割。
// 用流式读取避免把整个大文件（可能上 GB）读进内存。
func trimDebugFileTail(path string, reserve int) {
	if reserve <= 0 {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size := info.Size()
	if size <= int64(reserve) {
		return
	}
	// 从「文件末尾往前 reserve 字节」附近开始读，找到第一个行边界作为裁剪起点。
	startOffset := size - int64(reserve)
	tail, err := readTailFromLineBoundary(path, startOffset)
	if err != nil || len(tail) == 0 {
		return
	}
	// 覆盖写回尾部内容（O_TRUNC 清空再写）。
	if err := os.WriteFile(path, tail, 0o644); err != nil {
		logger.Errorf("debug log rotate failed: trim %s: %v", path, err)
		return
	}
	logger.Infof("debug log rotated: %s trimmed from %d to %d bytes", path, size, len(tail))
}

// readTailFromLineBoundary 从 offset 附近开始读取，跳过第一个不完整的行，
// 返回完整的尾部行。用于裁剪时保证不截断半行 JSON。
func readTailFromLineBoundary(path string, offset int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	remaining, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	// 丢弃第一行（它很可能从中间被截断）。除非 offset=0（完整文件）。
	if offset > 0 {
		if nl := bytes.IndexByte(remaining, '\n'); nl >= 0 {
			remaining = remaining[nl+1:]
		}
	}
	return remaining, nil
}

// debugLogReserveBytesFor 根据上限计算保留尾部的字节数（上限的 10%，最少 MinDebugLogReserveBytes）。
func debugLogReserveBytesFor(maxBytes int) int {
	reserve := maxBytes / 10
	if reserve < configMinDebugLogReserveBytes {
		reserve = configMinDebugLogReserveBytes
	}
	return reserve
}

func (recorder *debugRecorder) debugDir(requestID string, conversationID string) string {
	if recorder == nil || strings.TrimSpace(recorder.historyRoot) == "" {
		return ""
	}
	conversationID = firstNonEmpty(strings.TrimSpace(conversationID), recorder.conversationIDForRequest(requestID))
	if conversationID != "" && conversationID != "unknown" {
		return filepath.Join(recorder.historyRoot, sanitizeArtifactName(conversationID), "debug")
	}
	requestID = firstNonEmpty(strings.TrimSpace(requestID), "unknown")
	return filepath.Join(recorder.historyRoot, "_debug", "orphan", sanitizeArtifactName(requestID))
}

func (recorder *debugRecorder) conversationIDForRequest(requestID string) string {
	if recorder == nil || recorder.broker == nil {
		return ""
	}
	stream, ok := recorder.broker.Get(requestID)
	if !ok || stream == nil {
		return ""
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return strings.TrimSpace(stream.ConversationID)
}

func agentClientMessageCase(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	switch message.GetMessage().(type) {
	case *agentv1.AgentClientMessage_RunRequest:
		return "run_request"
	case *agentv1.AgentClientMessage_PrewarmRequest:
		return "prewarm_request"
	case *agentv1.AgentClientMessage_ConversationAction:
		return "conversation_action"
	case *agentv1.AgentClientMessage_ExecClientMessage:
		return "exec_client_message"
	case *agentv1.AgentClientMessage_ExecClientControlMessage:
		return "exec_client_control_message"
	case *agentv1.AgentClientMessage_InteractionResponse:
		return "interaction_response"
	case *agentv1.AgentClientMessage_ClientHeartbeat:
		return "client_heartbeat"
	case *agentv1.AgentClientMessage_KvClientMessage:
		return "kv_client_message"
	default:
		return fmt.Sprintf("%T", message.GetMessage())
	}
}

func agentServerMessageCase(message *agentv1.AgentServerMessage) string {
	if message == nil {
		return ""
	}
	switch message.GetMessage().(type) {
	case *agentv1.AgentServerMessage_InteractionUpdate:
		return "interaction_update"
	case *agentv1.AgentServerMessage_ExecServerMessage:
		return "exec_server_message"
	case *agentv1.AgentServerMessage_ExecServerControlMessage:
		return "exec_server_control_message"
	case *agentv1.AgentServerMessage_ConversationCheckpointUpdate:
		return "conversation_checkpoint_update"
	case *agentv1.AgentServerMessage_KvServerMessage:
		return "kv_server_message"
	case *agentv1.AgentServerMessage_InteractionQuery:
		return "interaction_query"
	default:
		return fmt.Sprintf("%T", message.GetMessage())
	}
}

func conversationActionCase(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	action := message.GetConversationAction()
	if action == nil && message.GetRunRequest() != nil {
		action = message.GetRunRequest().GetAction()
	}
	if action == nil {
		return ""
	}
	return conversationActionKind(action)
}

func requestedModelDebugPayload(message *agentv1.AgentClientMessage) map[string]any {
	if message == nil {
		return nil
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return requestedModelPayload(runRequest.GetRequestedModel())
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		return requestedModelPayload(prewarm.GetRequestedModel())
	}
	return nil
}

func requestedModelPayload(model *agentv1.RequestedModel) map[string]any {
	if model == nil {
		return nil
	}
	parameters := make([]map[string]string, 0, len(model.GetParameters()))
	for _, parameter := range model.GetParameters() {
		if parameter == nil {
			continue
		}
		parameters = append(parameters, map[string]string{
			"id":    parameter.GetId(),
			"value": parameter.GetValue(),
		})
	}
	return map[string]any{
		"model_id":                         strings.TrimSpace(model.GetModelId()),
		"max_mode":                         model.GetMaxMode(),
		"built_in_model":                   model.GetBuiltInModel(),
		"is_variant_string_representation": model.GetIsVariantStringRepresentation(),
		"parameters":                       parameters,
	}
}

func protoJSONDebugPayload(message proto.Message) any {
	if message == nil {
		return nil
	}
	if size := proto.Size(message); size > maxDebugProtoPayloadBytes {
		summary := map[string]any{
			"proto_size":      size,
			"payload_omitted": true,
			"message_type":    fmt.Sprintf("%T", message),
		}
		if serverMessage, ok := message.(*agentv1.AgentServerMessage); ok {
			summary["message_case"] = agentServerMessageCase(serverMessage)
			if checkpoint := serverMessage.GetConversationCheckpointUpdate(); checkpoint != nil {
				summary["checkpoint"] = checkpointDebugStats(checkpoint)
			}
		}
		return summary
	}
	payload, err := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}.Marshal(message)
	if err != nil {
		return map[string]any{"marshal_error": err.Error()}
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return string(payload)
	}
	return decoded
}

func checkpointDebugStats(state *agentv1.ConversationStateStructure) map[string]any {
	if state == nil {
		return nil
	}
	return map[string]any{
		"proto_size":                 proto.Size(state),
		"root_prompt_messages_count": len(state.GetRootPromptMessagesJson()),
		"root_prompt_messages_bytes": byteSliceTotal(state.GetRootPromptMessagesJson()),
		"turns_count":                len(state.GetTurns()),
		"turns_bytes":                byteSliceTotal(state.GetTurns()),
		"summary_bytes":              len(state.GetSummary()),
		"summary_archive_bytes":      len(state.GetSummaryArchive()),
		"summary_archives_count":     len(state.GetSummaryArchives()),
		"pending_tool_calls_count":   len(state.GetPendingToolCalls()),
		"subagent_runs_count":        len(state.GetSubagentRunsByParentToolCallId()),
		"file_states_count":          len(state.GetFileStates()) + len(state.GetFileStatesV2()),
	}
}

func byteSliceTotal(items [][]byte) int {
	total := 0
	for _, item := range items {
		total += len(item)
	}
	return total
}

func inboundIntentDebugPayload(intent InboundIntent) map[string]any {
	payload := map[string]any{
		"kind":               strings.TrimSpace(intent.Kind),
		"request_id":         strings.TrimSpace(intent.RequestID),
		"conversation_id":    strings.TrimSpace(intent.ConversationID),
		"model_id":           strings.TrimSpace(intent.ModelID),
		"model_name":         strings.TrimSpace(intent.ModelName),
		"thinking_effort":    strings.TrimSpace(intent.ThinkingEffort),
		"mode":               intent.Mode.String(),
		"has_explicit_mode":  intent.HasExplicitMode,
		"mode_source":        string(intent.ModeSource),
		"starts_run":         intent.StartsRun,
		"subagent_type_name": strings.TrimSpace(intent.SubagentTypeName),
		"cancel_reason":      strings.TrimSpace(intent.CancelReason),
		"prewarm":            intent.Prewarm,
	}
	if len(intent.SubagentModelOverrides) > 0 {
		payload["subagent_model_overrides"] = subagentModelOverrideSummaries(intent.SubagentModelOverrides)
		payload["subagent_model_override_count"] = len(intent.SubagentModelOverrides)
	}
	if intent.ClientMessage != nil {
		payload["client_message"] = protoJSONDebugPayload(intent.ClientMessage)
	}
	if intent.ConversationState != nil {
		payload["conversation_state"] = protoJSONDebugPayload(intent.ConversationState)
	}
	if intent.UserMessage != nil {
		payload["user_message"] = protoJSONDebugPayload(intent.UserMessage)
	}
	if intent.RequestContext != nil {
		payload["request_context"] = protoJSONDebugPayload(intent.RequestContext)
	}
	if strings.TrimSpace(intent.IgnoredReason) != "" {
		payload["ignored_reason"] = strings.TrimSpace(intent.IgnoredReason)
		payload["ignored_empty_resume"] = strings.TrimSpace(intent.IgnoredReason) == "empty_resume_without_pending_continuation"
	}
	if intent.ExecClientMessage != nil {
		payload["exec_client_message"] = protoJSONDebugPayload(intent.ExecClientMessage)
	}
	if intent.ExecClientControlMessage != nil {
		payload["exec_client_control_message"] = protoJSONDebugPayload(intent.ExecClientControlMessage)
	}
	if intent.InteractionResponse != nil {
		payload["interaction_response"] = protoJSONDebugPayload(intent.InteractionResponse)
	}
	if intent.KVClientMessage != nil {
		payload["kv_client_message"] = protoJSONDebugPayload(intent.KVClientMessage)
	}
	return payload
}

func conversationActionKind(action *agentv1.ConversationAction) string {
	if action == nil {
		return ""
	}
	switch action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return "user_message_action"
	case *agentv1.ConversationAction_ResumeAction:
		return "resume_action"
	case *agentv1.ConversationAction_CancelAction:
		return "cancel_action"
	case *agentv1.ConversationAction_SummarizeAction:
		return "summarize_action"
	case *agentv1.ConversationAction_ShellCommandAction:
		return "shell_command_action"
	case *agentv1.ConversationAction_StartPlanAction:
		return "start_plan_action"
	case *agentv1.ConversationAction_ExecutePlanAction:
		return "execute_plan_action"
	case *agentv1.ConversationAction_AsyncAskQuestionCompletionAction:
		return "async_ask_question_completion_action"
	case *agentv1.ConversationAction_CancelSubagentAction:
		return "cancel_subagent_action"
	case *agentv1.ConversationAction_BackgroundTaskCompletionAction:
		return "background_task_completion_action"
	case *agentv1.ConversationAction_BackgroundShellAction:
		return "background_shell_action"
	case *agentv1.ConversationAction_BackgroundSubagentAction:
		return "background_subagent_action"
	default:
		return fmt.Sprintf("%T", action.GetAction())
	}
}
