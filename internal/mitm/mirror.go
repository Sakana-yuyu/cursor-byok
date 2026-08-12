package mitm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
	agentprotocol "cursor/internal/backend/agent/protocol"
	"cursor/internal/logger"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// MirrorCaptureConfig 提供镜像记录所需的配置。
type MirrorCaptureConfig interface {
	MirrorCaptureEnabled(ctx context.Context) bool
	MirrorCaptureHosts() []string
}

const (
	mirrorLogSubdir            = "_debug/mirror"
	mirrorLogFilename          = "official.raw.jsonl"
	mirrorTimelineLogFilename  = "protocol.timeline.jsonl"
	mirrorBodyMaxBytes         = 128 * 1024
	mirrorResponseMaxBytes     = 1024 * 1024
	mirrorConnectFrameMaxBytes = mirrorResponseMaxBytes
)

// mirrorSensitiveHeaders 记录时一律抹掉的敏感头。
var mirrorSensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"cookie":              true,
	"set-cookie":          true,
}

// mirrorRecord 是 official.raw.jsonl 的一行。
type mirrorRecord struct {
	TS            time.Time            `json:"ts"`
	ExchangeID    string               `json:"exchangeId,omitempty"`
	Phase         mirrorPhase          `json:"phase"`
	Host          string               `json:"host"`
	Model         string               `json:"model,omitempty"`
	Method        string               `json:"method,omitempty"`
	URL           string               `json:"url,omitempty"`
	Status        int                  `json:"status,omitempty"`
	Headers       map[string]string    `json:"headers,omitempty"`
	Body          string               `json:"body,omitempty"`
	BodyEncoding  string               `json:"bodyEncoding,omitempty"`
	BodyBytes     *int                 `json:"bodyBytes,omitempty"`
	BodySHA256    string               `json:"bodySHA256,omitempty"`
	BodyBase64    *string              `json:"bodyBase64,omitempty"`
	Protocol      *mirrorProtocol      `json:"protocol,omitempty"`
	ProtocolFrame *mirrorProtocolFrame `json:"protocolFrame,omitempty"`
	Truncated     bool                 `json:"truncated,omitempty"`
}

// mirrorProtocol 是隔离保真记录中的协议结构摘要，不保存请求正文或稳定标识原文。
type mirrorProtocol struct {
	RequestIDHash     string `json:"requestIdHash,omitempty"`
	AppendSeqno       *int64 `json:"appendSeqno,omitempty"`
	ClientMessageKind string `json:"clientMessageKind,omitempty"`
	AgentMode         string `json:"agentMode,omitempty"`
	Multitask         bool   `json:"multitask,omitempty"`
	SubagentAction    string `json:"subagentAction,omitempty"`
	DecodeError       string `json:"decodeError,omitempty"`
}

// mirrorProtocolFrame 是隔离 RunSSE 流中的一个完整协议帧。
// frameBase64 保留线上字节，摘要只保存结构字段而不展开服务端消息内容。
type mirrorProtocolFrame struct {
	Direction          string `json:"direction"`
	Sequence           int    `json:"sequence"`
	FrameEncoding      string `json:"frameEncoding"`
	FrameBytes         int    `json:"frameBytes"`
	FrameSHA256        string `json:"frameSHA256"`
	FrameBase64        string `json:"frameBase64"`
	ConnectFlags       *uint8 `json:"connectFlags,omitempty"`
	ConnectCompression string `json:"connectCompression,omitempty"`
	ServerMessageKind  string `json:"serverMessageKind,omitempty"`
	ServerDetailKind   string `json:"serverDetailKind,omitempty"`
	ExecMessageKind    string `json:"execMessageKind,omitempty"`
	SubagentAction     string `json:"subagentAction,omitempty"`
	StreamContentKind  string `json:"streamContentKind,omitempty"`
	StreamDeltaBytes   *int   `json:"streamDeltaBytes,omitempty"`
	StreamDeltaSHA256  string `json:"streamDeltaSHA256,omitempty"`
	Terminal           bool   `json:"terminal,omitempty"`
	DecodeError        string `json:"decodeError,omitempty"`
}

// mirrorTimelineRecord 是隔离协议索引的一行，不承载任何原始协议字节或正文。
type mirrorTimelineRecord struct {
	TS                 time.Time `json:"ts"`
	RequestIDHash      string    `json:"requestIdHash"`
	ExchangeID         string    `json:"exchangeId"`
	Direction          string    `json:"direction"`
	Sequence           int       `json:"sequence"`
	EventKind          string    `json:"eventKind"`
	ClientMessageKind  string    `json:"clientMessageKind,omitempty"`
	AgentMode          string    `json:"agentMode,omitempty"`
	Multitask          bool      `json:"multitask,omitempty"`
	SubagentAction     string    `json:"subagentAction,omitempty"`
	ConnectCompression string    `json:"connectCompression,omitempty"`
	ServerMessageKind  string    `json:"serverMessageKind,omitempty"`
	ServerDetailKind   string    `json:"serverDetailKind,omitempty"`
	ExecMessageKind    string    `json:"execMessageKind,omitempty"`
	StreamContentKind  string    `json:"streamContentKind,omitempty"`
	StreamDeltaBytes   *int      `json:"streamDeltaBytes,omitempty"`
	StreamDeltaSHA256  string    `json:"streamDeltaSHA256,omitempty"`
	Terminal           bool      `json:"terminal,omitempty"`
	DecodeError        string    `json:"decodeError,omitempty"`
}

type mirrorPhase string

const (
	mirrorPhaseRequest           mirrorPhase = "request"
	mirrorPhaseResponseStart     mirrorPhase = "response_start"
	mirrorPhaseResponseChunk     mirrorPhase = "response_chunk"
	mirrorPhaseResponseTruncated mirrorPhase = "response_truncated"
)

// mirrorExchange 只在代理进程内关联同一镜像 HTTP 交换的各条记录。
type mirrorExchange struct {
	mu               sync.Mutex
	id               string
	model            string
	requestIDHash    string
	timelineSequence int
}

// mirrorRecorder 把镜像请求/响应追加写入 <historyRoot>/_debug/mirror/official.raw.jsonl。
// 记录失败只记日志，绝不阻断代理直通。
type mirrorRecorder struct {
	historyRoot      string
	protocolFidelity bool
	mu               sync.Mutex
	file             *os.File
	timelineFile     *os.File
}

func newMirrorRecorder(historyRoot string) *mirrorRecorder {
	return &mirrorRecorder{historyRoot: historyRoot}
}

func newProtocolFidelityMirrorRecorder(historyRoot string) *mirrorRecorder {
	return &mirrorRecorder{historyRoot: historyRoot, protocolFidelity: true}
}

func (r *mirrorRecorder) ensureFile() error {
	if r == nil || r.historyRoot == "" {
		return nil
	}
	if r.file != nil {
		return nil
	}
	dir := filepath.Join(r.historyRoot, mirrorLogSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, mirrorLogFilename), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	r.file = f
	return nil
}

func (r *mirrorRecorder) ensureTimelineFile() error {
	if r == nil || r.historyRoot == "" || !r.protocolFidelity {
		return nil
	}
	if r.timelineFile != nil {
		return nil
	}
	dir := filepath.Join(r.historyRoot, mirrorLogSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, mirrorTimelineLogFilename), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	r.timelineFile = f
	return nil
}

// Close 关闭底层日志文件，释放句柄。用于服务关闭或测试清理。
func (r *mirrorRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil && r.timelineFile == nil {
		return nil
	}
	var err error
	if r.file != nil {
		err = r.file.Close()
		r.file = nil
	}
	if r.timelineFile != nil {
		if timelineErr := r.timelineFile.Close(); err == nil {
			err = timelineErr
		}
		r.timelineFile = nil
	}
	return err
}

// recordRequest 记录一次镜像请求；读出的 body 会重建回 req.Body 供直通。
func (r *mirrorRecorder) recordRequest(host string, req *http.Request) {
	r.recordExchangeRequest(host, nil, req)
}

// recordExchangeRequest 记录带本地交换关联信息的镜像请求。
func (r *mirrorRecorder) recordExchangeRequest(host string, exchange *mirrorExchange, req *http.Request) {
	if r == nil || req == nil {
		return
	}
	rec := mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseRequest,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
		Method:     req.Method,
		URL:        sanitizeMirrorRequestURL(req),
		Headers:    sanitizeHeaders(req.Header),
	}
	var body []byte
	if req.Body != nil {
		readBody, err := io.ReadAll(io.LimitReader(req.Body, mirrorBodyMaxBytes+1))
		if err != nil {
			// 直通语义优先于记录：读失败时保持 req.Body 原样（不重建），
			// 避免把截断的部分 body 重建后直通给上游；不写记录也不动 body。
			logger.Errorf("mirror record read request body failed: %v", err)
			return
		}
		body = readBody
		recordedBody := body
		if len(body) > mirrorBodyMaxBytes {
			// 记录端截断不影响直通：继续读完剩余部分，重建完整 body 给上游。
			rest, restErr := io.ReadAll(req.Body)
			if restErr != nil {
				logger.Errorf("mirror record drain request body failed: %v", restErr)
			}
			body = append(body, rest...)
			rec.Truncated = true
			recordedBody = body[:mirrorBodyMaxBytes]
		}
		r.setRecordBody(&rec, recordedBody)
		r.setBidiProtocolSummary(&rec, req, recordedBody)
		r.setRunSSERequestSummary(&rec, req, recordedBody)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	if rec.Model == "" {
		rec.Model = mirrorRequestModel(host, req, body)
	}
	if exchange != nil {
		exchange.setModel(rec.Model)
	}
	r.write(rec)
	r.recordExchangeRequestTimeline(exchange, req, rec.Protocol)
}

// recordResponseStart 记录一次镜像响应的起始信息（ts/host/status/脱敏 headers），body 为空。
// 响应体内容由 recordResponseChunk 逐 chunk 追加。
func (r *mirrorRecorder) recordResponseStart(host string, resp *http.Response) {
	r.recordExchangeResponseStart(host, nil, resp)
}

func (r *mirrorRecorder) recordExchangeResponseStart(host string, exchange *mirrorExchange, resp *http.Response) {
	if r == nil || resp == nil {
		return
	}
	r.write(mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseResponseStart,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
		Status:     resp.StatusCode,
		Headers:    sanitizeHeaders(resp.Header),
	})
}

// recordResponseTruncated 写一条 truncated 收尾记录，标记该响应后续 body 因超限未记录。
func (r *mirrorRecorder) recordResponseTruncated(host string) {
	r.recordExchangeResponseTruncated(host, nil)
}

func (r *mirrorRecorder) recordExchangeResponseTruncated(host string, exchange *mirrorExchange) {
	if r == nil {
		return
	}
	r.write(mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseResponseTruncated,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
		Truncated:  true,
	})
}

// recordResponseChunk 记录一次镜像响应的一个流式 chunk。
func (r *mirrorRecorder) recordResponseChunk(host string, chunk []byte) {
	r.recordExchangeResponseChunk(host, nil, chunk)
}

func (r *mirrorRecorder) recordExchangeResponseChunk(host string, exchange *mirrorExchange, chunk []byte) {
	if r == nil || len(chunk) == 0 {
		return
	}
	rec := mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseResponseChunk,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
	}
	r.setRecordBody(&rec, chunk)
	r.write(rec)
}

// recordExchangeResponseProtocolFrame 将完整的 RunSSE 协议帧作为独立事件写入。
// 底层读取块绝不作为协议事件记录，避免网络缓冲行为污染后续协议对照。
func (r *mirrorRecorder) recordExchangeResponseProtocolFrame(host string, exchange *mirrorExchange, frame mirrorProtocolFrame) {
	if r == nil || !r.protocolFidelity || frame.FrameBytes == 0 {
		return
	}
	r.write(mirrorRecord{
		TS:            time.Now(),
		ExchangeID:    mirrorExchangeID(exchange),
		Phase:         mirrorPhaseResponseChunk,
		Host:          host,
		Model:         mirrorExchangeModel(exchange),
		ProtocolFrame: &frame,
	})
	r.recordExchangeResponseTimeline(exchange, frame)
}

// setRecordBody 仅在隔离保真模式中使用 Base64，避免 protobuf 非 UTF-8 字节经 JSON 字符串替换。
func (r *mirrorRecorder) setRecordBody(rec *mirrorRecord, body []byte) {
	if rec == nil {
		return
	}
	if r == nil || !r.protocolFidelity {
		rec.Body = string(body)
		return
	}
	sum := sha256.Sum256(body)
	bodyBytes := len(body)
	bodyBase64 := base64.StdEncoding.EncodeToString(body)
	rec.BodyEncoding = "base64"
	rec.BodyBytes = &bodyBytes
	rec.BodySHA256 = hex.EncodeToString(sum[:])
	rec.BodyBase64 = &bodyBase64
}

// setBidiProtocolSummary 尽力解析 BidiAppend 的外层和内层 Agent protobuf。
// 解析结果只供隔离协议对齐使用，原始 ID、prompt、路径和 protobuf JSON 都不写入摘要。
func (r *mirrorRecorder) setBidiProtocolSummary(rec *mirrorRecord, req *http.Request, body []byte) {
	if r == nil || !r.protocolFidelity || rec == nil || !isBidiAppendRequest(req) {
		return
	}
	summary := &mirrorProtocol{}
	rec.Protocol = summary
	if rec.Truncated {
		summary.DecodeError = "body_truncated"
		return
	}
	if encoding := strings.TrimSpace(req.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		summary.DecodeError = "unsupported_content_encoding"
		return
	}
	appendRequest := &aiserverv1.BidiAppendRequest{}
	if err := proto.Unmarshal(body, appendRequest); err != nil {
		summary.DecodeError = "bidi_append_unmarshal_failed"
		return
	}
	requestID := agentprotocol.ReadAppendRequestID(appendRequest)
	if requestID != "" {
		sum := sha256.Sum256([]byte(requestID))
		summary.RequestIDHash = hex.EncodeToString(sum[:])
	}
	appendSeqno := appendRequest.GetAppendSeqno()
	summary.AppendSeqno = &appendSeqno
	clientMessage, clientMessageKind, _, err := agentprotocol.DecodeBidiAppendAgentClientMessage(appendRequest.GetData(), appendRequest.GetDataBinary())
	if err != nil {
		summary.DecodeError = "agent_client_unmarshal_failed"
		return
	}
	summary.ClientMessageKind = clientMessageKind
	summary.AgentMode = mirrorAgentMode(clientMessage)
	summary.Multitask = summary.AgentMode == agentv1.AgentMode_AGENT_MODE_MULTITASK.String()
	summary.SubagentAction = mirrorSubagentAction(clientMessage)
}

// setRunSSERequestSummary 读取 RunSSE Connect 请求中的 BidiRequestId，只保留哈希。
func (r *mirrorRecorder) setRunSSERequestSummary(rec *mirrorRecord, req *http.Request, body []byte) {
	if r == nil || !r.protocolFidelity || rec == nil || !isRunSSERequest(req) {
		return
	}
	summary := &mirrorProtocol{}
	rec.Protocol = summary
	if rec.Truncated {
		summary.DecodeError = "body_truncated"
		return
	}
	if encoding := strings.TrimSpace(req.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		summary.DecodeError = "unsupported_content_encoding"
		return
	}
	requestID, decodeError := mirrorRunSSERequestID(req, body)
	if decodeError != "" {
		summary.DecodeError = decodeError
		return
	}
	sum := sha256.Sum256([]byte(requestID))
	summary.RequestIDHash = hex.EncodeToString(sum[:])
}

func isBidiAppendRequest(req *http.Request) bool {
	return req != nil && req.URL != nil && req.URL.Path == "/aiserver.v1.BidiService/BidiAppend"
}

func isRunSSERequest(req *http.Request) bool {
	return req != nil && req.URL != nil && req.URL.Path == "/agent.v1.AgentService/RunSSE"
}

func mirrorRunSSERequestID(req *http.Request, body []byte) (string, string) {
	contentType := ""
	if req != nil {
		contentType = strings.ToLower(strings.TrimSpace(strings.Split(req.Header.Get("Content-Type"), ";")[0]))
	}
	if contentType != "application/connect+proto" {
		return "", "runsse_request_content_type_unsupported"
	}
	if len(body) < 5 {
		return "", "runsse_request_frame_incomplete"
	}
	flags := body[0]
	payloadLength := int(binary.BigEndian.Uint32(body[1:5]))
	if payloadLength > mirrorConnectFrameMaxBytes || len(body) != 5+payloadLength {
		return "", "runsse_request_frame_invalid"
	}
	payload := body[5:]
	if flags&0x01 != 0 {
		var err error
		payload, err = mirrorDecompressConnectPayload(payload, req.Header.Get("Connect-Content-Encoding"))
		if err != nil {
			return "", err.Error()
		}
	}
	if flags&0x02 != 0 {
		return "", "runsse_request_end_stream"
	}
	requestID := &aiserverv1.BidiRequestId{}
	if err := proto.Unmarshal(payload, requestID); err != nil || strings.TrimSpace(requestID.GetRequestId()) == "" {
		return "", "runsse_request_unmarshal_failed"
	}
	return strings.TrimSpace(requestID.GetRequestId()), ""
}

func mirrorAgentMode(message *agentv1.AgentClientMessage) string {
	if message == nil || message.GetRunRequest() == nil || message.GetRunRequest().GetConversationState() == nil {
		return ""
	}
	mode := message.GetRunRequest().GetConversationState().GetMode()
	if mode == agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return ""
	}
	return mode.String()
}

func mirrorSubagentAction(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	if action := mirrorConversationSubagentAction(message.GetConversationAction()); action != "" {
		return action
	}
	if message.GetRunRequest() == nil {
		return ""
	}
	return mirrorConversationSubagentAction(message.GetRunRequest().GetAction())
}

func mirrorConversationSubagentAction(action *agentv1.ConversationAction) string {
	if action == nil {
		return ""
	}
	switch action.GetAction().(type) {
	case *agentv1.ConversationAction_BackgroundSubagentAction:
		return "background"
	case *agentv1.ConversationAction_CancelSubagentAction:
		return "cancel"
	default:
		return ""
	}
}

// mirrorRunSSEFrameDecoder 在 tee 的记录副本上按协议边界重组 RunSSE 响应。
// 它只观察字节，不会修改转发给 Cursor 的响应流。
type mirrorRunSSEFrameDecoder struct {
	encoding string
	codec    string
	buffer   []byte
	sequence int
	emit     func(mirrorProtocolFrame)
	closed   bool
}

func newMirrorRunSSEFrameDecoder(resp *http.Response, emit func(mirrorProtocolFrame)) *mirrorRunSSEFrameDecoder {
	if !isRunSSEResponse(resp) || emit == nil {
		return nil
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	encoding := ""
	switch contentType {
	case "application/connect+proto":
		encoding = "connect"
	case "text/event-stream":
		// Cursor 当前会为 Connect 二进制帧标记 text/event-stream，
		// 先保留最小缓冲区探测帧头，无法确认时才按文本 SSE 处理。
		encoding = "pending"
	default:
		return nil
	}
	return &mirrorRunSSEFrameDecoder{
		encoding: encoding,
		codec:    strings.TrimSpace(resp.Header.Get("Connect-Content-Encoding")),
		emit:     emit,
	}
}

func isRunSSEResponse(resp *http.Response) bool {
	return resp != nil && resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.Path == "/agent.v1.AgentService/RunSSE"
}

func (d *mirrorRunSSEFrameDecoder) Write(chunk []byte) {
	if d == nil || d.closed || len(chunk) == 0 {
		return
	}
	d.buffer = append(d.buffer, chunk...)
	if d.encoding == "connect" {
		d.writeConnectFrames()
		return
	}
	if d.encoding == "pending" {
		if len(d.buffer) < 5 {
			return
		}
		if mirrorConnectFrameHeaderValid(d.buffer) && !mirrorConnectFrameComplete(d.buffer) {
			return
		}
		if mirrorConnectFrameComplete(d.buffer) {
			d.encoding = "connect"
			d.writeConnectFrames()
			return
		}
		d.encoding = "sse"
	}
	d.writeSSEFrames()
}

func mirrorConnectFrameHeaderValid(buffer []byte) bool {
	if len(buffer) < 5 {
		return false
	}
	flags := buffer[0]
	if flags&^uint8(0x03) != 0 {
		return false
	}
	payloadLength := int(binary.BigEndian.Uint32(buffer[1:5]))
	return payloadLength <= mirrorConnectFrameMaxBytes
}

func mirrorConnectFrameComplete(buffer []byte) bool {
	if !mirrorConnectFrameHeaderValid(buffer) {
		return false
	}
	payloadLength := int(binary.BigEndian.Uint32(buffer[1:5]))
	return len(buffer) >= 5+payloadLength
}

func (d *mirrorRunSSEFrameDecoder) writeConnectFrames() {
	for len(d.buffer) >= 5 {
		flags := d.buffer[0]
		payloadLength := int(binary.BigEndian.Uint32(d.buffer[1:5]))
		if payloadLength > mirrorConnectFrameMaxBytes {
			d.emitFrame(append([]byte(nil), d.buffer[:5]...), &flags, false, "connect_frame_length_invalid", mirrorProtocolFrame{})
			d.buffer = nil
			return
		}
		frameLength := 5 + payloadLength
		if len(d.buffer) < frameLength {
			return
		}
		frame := append([]byte(nil), d.buffer[:frameLength]...)
		d.buffer = d.buffer[frameLength:]
		summary, terminal, decodeError := d.decodeConnectServerMessage(flags, frame[5:])
		d.emitFrame(frame, &flags, terminal, decodeError, summary)
	}
}

func (d *mirrorRunSSEFrameDecoder) writeSSEFrames() {
	for {
		boundary := mirrorSSEFrameBoundary(d.buffer)
		if boundary == 0 {
			return
		}
		frame := append([]byte(nil), d.buffer[:boundary]...)
		d.buffer = d.buffer[boundary:]
		d.emitFrame(frame, nil, mirrorSSEFrameTerminal(frame), "sse_server_message_unavailable", mirrorProtocolFrame{})
	}
}

func mirrorSSEFrameBoundary(buffer []byte) int {
	lfBoundary := bytes.Index(buffer, []byte("\n\n"))
	crlfBoundary := bytes.Index(buffer, []byte("\r\n\r\n"))
	if lfBoundary < 0 && crlfBoundary < 0 {
		return 0
	}
	if lfBoundary < 0 {
		return crlfBoundary + 4
	}
	if crlfBoundary < 0 || lfBoundary < crlfBoundary {
		return lfBoundary + 2
	}
	return crlfBoundary + 4
}

func (d *mirrorRunSSEFrameDecoder) decodeConnectServerMessage(flags uint8, payload []byte) (mirrorProtocolFrame, bool, string) {
	frame := mirrorProtocolFrame{ConnectCompression: "identity"}
	decoded := payload
	if flags&0x01 != 0 {
		frame.ConnectCompression = strings.ToLower(strings.TrimSpace(d.codec))
		if frame.ConnectCompression == "" {
			frame.ConnectCompression = "gzip"
		}
		var err error
		decoded, err = mirrorDecompressConnectPayload(payload, d.codec)
		if err != nil {
			return frame, false, err.Error()
		}
	}
	if flags&0x02 != 0 {
		return frame, true, ""
	}
	message := &agentv1.AgentServerMessage{}
	if err := proto.Unmarshal(decoded, message); err != nil {
		return frame, false, "agent_server_unmarshal_failed"
	}
	frame.ServerMessageKind = mirrorActiveOneofName(message)
	if interaction := message.GetInteractionUpdate(); interaction != nil {
		frame.ServerDetailKind = mirrorActiveOneofName(interaction)
		frame.StreamContentKind = frame.ServerDetailKind
		if payload := mirrorInteractionUpdatePayload(interaction); len(payload) > 0 {
			deltaBytes := len(payload)
			sum := sha256.Sum256(payload)
			frame.StreamDeltaBytes = &deltaBytes
			frame.StreamDeltaSHA256 = hex.EncodeToString(sum[:])
		}
	}
	if execMessage := message.GetExecServerMessage(); execMessage != nil {
		frame.ExecMessageKind = mirrorActiveOneofName(execMessage)
		frame.SubagentAction = mirrorExecSubagentAction(frame.ExecMessageKind)
	}
	if controlMessage := message.GetExecServerControlMessage(); controlMessage != nil {
		frame.ServerDetailKind = mirrorActiveOneofName(controlMessage)
	}
	if kvMessage := message.GetKvServerMessage(); kvMessage != nil {
		frame.ServerDetailKind = mirrorActiveOneofName(kvMessage)
	}
	if query := message.GetInteractionQuery(); query != nil {
		frame.ServerDetailKind = mirrorActiveOneofName(query)
	}
	return frame, false, ""
}

func mirrorExecSubagentAction(kind string) string {
	switch kind {
	case "subagent_args":
		return "create"
	case "force_background_subagent_args":
		return "background"
	case "subagent_await_args":
		return "await"
	default:
		return ""
	}
}

func mirrorInteractionUpdatePayload(message *agentv1.InteractionUpdate) []byte {
	if message == nil || message.GetMessage() == nil {
		return nil
	}
	reflect := message.ProtoReflect()
	oneofs := reflect.Descriptor().Oneofs()
	for index := 0; index < oneofs.Len(); index++ {
		field := reflect.WhichOneof(oneofs.Get(index))
		if field != nil && field.Kind() == protoreflect.MessageKind {
			return marshalMirrorProtoMessage(reflect.Get(field).Message().Interface())
		}
	}
	return nil
}

func marshalMirrorProtoMessage(message proto.Message) []byte {
	if message == nil {
		return nil
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return nil
	}
	return payload
}

func mirrorSSEFrameTerminal(frame []byte) bool {
	return bytes.Contains(frame, []byte("data: [DONE]"))
}

func mirrorDecompressConnectPayload(payload []byte, codec string) ([]byte, error) {
	if codec != "" && !strings.EqualFold(codec, "gzip") {
		return nil, mirrorProtocolDecodeError("connect_compression_unsupported")
	}
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, mirrorProtocolDecodeError("connect_gzip_unmarshal_failed")
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, mirrorConnectFrameMaxBytes+1))
	if err != nil || len(decoded) > mirrorConnectFrameMaxBytes {
		return nil, mirrorProtocolDecodeError("connect_gzip_unmarshal_failed")
	}
	return decoded, nil
}

type mirrorProtocolDecodeError string

func (e mirrorProtocolDecodeError) Error() string { return string(e) }

func mirrorActiveOneofName(message proto.Message) string {
	if message == nil {
		return ""
	}
	oneofs := message.ProtoReflect().Descriptor().Oneofs()
	for index := 0; index < oneofs.Len(); index++ {
		if field := message.ProtoReflect().WhichOneof(oneofs.Get(index)); field != nil {
			return string(field.Name())
		}
	}
	return ""
}

func (d *mirrorRunSSEFrameDecoder) emitFrame(frame []byte, flags *uint8, terminal bool, decodeError string, summary mirrorProtocolFrame) {
	if d == nil || len(frame) == 0 || d.emit == nil {
		return
	}
	sum := sha256.Sum256(frame)
	summary.Direction = "response"
	summary.Sequence = d.sequence
	summary.FrameEncoding = d.encoding
	summary.FrameBytes = len(frame)
	summary.FrameSHA256 = hex.EncodeToString(sum[:])
	summary.FrameBase64 = base64.StdEncoding.EncodeToString(frame)
	summary.ConnectFlags = flags
	summary.Terminal = terminal
	summary.DecodeError = decodeError
	d.emit(summary)
	d.sequence++
}

func (d *mirrorRunSSEFrameDecoder) Close() {
	if d == nil || d.closed {
		return
	}
	d.closed = true
	if len(d.buffer) == 0 {
		return
	}
	decodeError := "sse_frame_incomplete"
	if d.encoding == "connect" || (d.encoding == "pending" && mirrorConnectFrameHeaderValid(d.buffer)) {
		decodeError = "connect_frame_incomplete"
	}
	d.emitFrame(append([]byte(nil), d.buffer...), nil, false, decodeError, mirrorProtocolFrame{})
	d.buffer = nil
}

func (r *mirrorRecorder) recordExchangeRequestTimeline(exchange *mirrorExchange, req *http.Request, summary *mirrorProtocol) {
	if r == nil || !r.protocolFidelity || exchange == nil || summary == nil || summary.RequestIDHash == "" {
		return
	}
	eventKind := ""
	if isBidiAppendRequest(req) {
		eventKind = "bidi_append"
	} else if isRunSSERequest(req) {
		eventKind = "runsse_request"
	}
	if eventKind == "" {
		return
	}
	exchange.setRequestIDHash(summary.RequestIDHash)
	r.writeTimeline(mirrorTimelineRecord{
		TS:                time.Now(),
		RequestIDHash:     summary.RequestIDHash,
		ExchangeID:        mirrorExchangeID(exchange),
		Direction:         "request",
		Sequence:          exchange.nextTimelineSequence(),
		EventKind:         eventKind,
		ClientMessageKind: summary.ClientMessageKind,
		AgentMode:         summary.AgentMode,
		Multitask:         summary.Multitask,
		SubagentAction:    summary.SubagentAction,
		DecodeError:       summary.DecodeError,
	})
}

func (r *mirrorRecorder) recordExchangeResponseTimeline(exchange *mirrorExchange, frame mirrorProtocolFrame) {
	if r == nil || !r.protocolFidelity || exchange == nil {
		return
	}
	requestIDHash := exchange.requestIDHashValue()
	if requestIDHash == "" {
		return
	}
	r.writeTimeline(mirrorTimelineRecord{
		TS:                 time.Now(),
		RequestIDHash:      requestIDHash,
		ExchangeID:         mirrorExchangeID(exchange),
		Direction:          frame.Direction,
		Sequence:           exchange.nextTimelineSequence(),
		EventKind:          "runsse_" + frame.FrameEncoding,
		SubagentAction:     frame.SubagentAction,
		ConnectCompression: frame.ConnectCompression,
		ServerMessageKind:  frame.ServerMessageKind,
		ServerDetailKind:   frame.ServerDetailKind,
		ExecMessageKind:    frame.ExecMessageKind,
		StreamContentKind:  frame.StreamContentKind,
		StreamDeltaBytes:   frame.StreamDeltaBytes,
		StreamDeltaSHA256:  frame.StreamDeltaSHA256,
		Terminal:           frame.Terminal,
		DecodeError:        frame.DecodeError,
	})
}

func (r *mirrorRecorder) writeTimeline(rec mirrorTimelineRecord) {
	line, err := json.Marshal(rec)
	if err != nil {
		logger.Errorf("mirror timeline marshal failed: %v", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureTimelineFile(); err != nil {
		logger.Errorf("mirror timeline open failed: %v", err)
		return
	}
	if _, err := r.timelineFile.Write(append(line, '\n')); err != nil {
		logger.Errorf("mirror timeline write failed: %v", err)
	}
}

func mirrorExchangeID(exchange *mirrorExchange) string {
	if exchange == nil {
		return ""
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	return exchange.id
}

func mirrorExchangeModel(exchange *mirrorExchange) string {
	if exchange == nil {
		return ""
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	return exchange.model
}

func (exchange *mirrorExchange) setRequestIDHash(requestIDHash string) {
	if exchange == nil || requestIDHash == "" {
		return
	}
	exchange.mu.Lock()
	exchange.requestIDHash = requestIDHash
	exchange.mu.Unlock()
}

func (exchange *mirrorExchange) setModel(model string) {
	if exchange == nil {
		return
	}
	exchange.mu.Lock()
	exchange.model = model
	exchange.mu.Unlock()
}

func (exchange *mirrorExchange) requestIDHashValue() string {
	if exchange == nil {
		return ""
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	return exchange.requestIDHash
}

func (exchange *mirrorExchange) nextTimelineSequence() int {
	if exchange == nil {
		return 0
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	sequence := exchange.timelineSequence
	exchange.timelineSequence++
	return sequence
}

// mirrorRequestModel 从已读取的请求体或 Gemini URL 尽力提取模型名。
// 解析失败属于不可信请求输入，保留空元数据但不影响官方请求直通。
func mirrorRequestModel(host string, req *http.Request, body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	if len(body) > 0 && json.Unmarshal(body, &payload) == nil {
		if model := strings.TrimSpace(payload.Model); model != "" {
			return model
		}
	}
	if !strings.EqualFold(strings.TrimSpace(host), "generativelanguage.googleapis.com") || req == nil || req.URL == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] != "models" {
			continue
		}
		model := strings.SplitN(parts[index+1], ":", 2)[0]
		return strings.TrimSpace(model)
	}
	return ""
}

func (r *mirrorRecorder) write(rec mirrorRecord) {
	line, err := json.Marshal(rec)
	if err != nil {
		logger.Errorf("mirror record marshal failed: %v", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureFile(); err != nil {
		logger.Errorf("mirror record open failed: %v", err)
		return
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		logger.Errorf("mirror record write failed: %v", err)
	}
}

// sanitizeMirrorRequestURL 只生成用于本地镜像记录的 URL 副本。
// 直通请求继续使用 req.URL，避免记录脱敏意外改变官方 API 语义。
func sanitizeMirrorRequestURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	copyURL := *req.URL
	values := copyURL.Query()
	for key := range values {
		if isMirrorSensitiveQueryKey(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
	copyURL.RawQuery = values.Encode()
	copyURL.Fragment = ""
	copyURL.RawFragment = ""
	return mirrorURLString(&copyURL)
}

func isMirrorSensitiveQueryKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "key", "api_key", "apikey", "token", "access_token", "refresh_token", "secret", "signature", "sig", "password", "pass":
		return true
	default:
		return false
	}
}

func mirrorURLString(value *url.URL) string {
	if value == nil {
		return ""
	}
	text := value.String()
	if strings.TrimSpace(text) == "" {
		return "/"
	}
	return text
}

func sanitizeHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		lower := strings.ToLower(k)
		if mirrorSensitiveHeaders[lower] {
			out[lower] = "[REDACTED]"
			continue
		}
		if len(vs) > 0 {
			out[lower] = vs[0]
		}
	}
	return out
}
