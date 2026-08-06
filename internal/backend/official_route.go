package backend

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
	"cursor/internal/backend/agent/protocol"
	"cursor/internal/backend/forwarder"
	"cursor/internal/backend/server"
	"cursor/internal/backend/server/upstream"
	"cursor/internal/logger"
)

// officialRequestTTL 官方模型请求登记的 TTL，超过后自动清理防泄漏。
const officialRequestTTL = 30 * time.Minute

// officialBidiAppendHandler 包装 BidiAppend 路由：每个携带模型的消息都做分流判定——
// 命中官方模型目录且官方账号已登录时登记 request_id 并端到端透传官方
// （api2.cursor.sh + 真实官方 token）；同 request 内后续出现非官方模型则撤销登记
// 回退本地 forwarder；不携带模型的消息（exec/heartbeat 等）不改变登记状态。
func (host *Host) officialBidiAppendHandler(inner http.Handler, deps upstream.Dependencies) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			inner.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		requestID, modelID := decodeBidiAppendRequestMeta(body)
		if len(body) < 200 {
			// 临时诊断：记录小 BidiAppend 的内容（心跳/重试/保活）
			logger.Infof("bidi small body_len=%d request_id=%q model=%q head=%x", len(body), requestID, modelID, body[:min(16, len(body))])
		}
		if requestID != "" && modelID != "" {
			if upstream.IsOfficialModel(modelID) &&
				host.controlPlaneAuth != nil && host.controlPlaneAuth.SignedIn() {
				// 命中官方模型：补登记（不限首条消息，resume/后续 run 也会带模型）。
				host.officialRequestIDs.Store(requestID, time.Now())
				logger.Infof("official model bidi routed request_id=%s model=%s -> api2.cursor.sh", requestID, modelID)
			} else if _, ok := host.officialRequestIDs.Load(requestID); ok {
				// 同 request 内出现非官方模型（或官方账号未登录）→ 撤销官方透传，
				// 避免误把本地模型请求转发官方（官方计费 + 4xx）。
				// 注意：modelID 为空的消息（exec/heartbeat 等）不触发撤销。
				host.officialRequestIDs.Delete(requestID)
				logger.Infof("official model bidi reverted request_id=%s model=%s -> local", requestID, modelID)
			}
		}
		if host.isOfficialRequest(requestID) {
			// 官方模型请求：立即向客户端返回空 BidiAppendResponse（受理确认），
			// 避免客户端等待官方处理（实测 5.9s）期间放弃建立 RunSSE 订阅而丢失
			// 回复流。官方 BidiAppend 的处理与回复经官方 RunSSE 推送，客户端建立
			// RunSSE 后由 officialRunSSEHandler 透传官方流式返回。
			// 官方 BidiAppend 自身的响应（空确认）直接丢弃。
			bodyCopy := append([]byte(nil), body...)
			go func() {
				// 用独立 context：客户端 BidiAppend 连接关闭后官方处理仍应继续
				// （否则取消会中断官方侧处理，RunSSE 无回复可推）。
				detachedReq := r.Clone(context.WithoutCancel(r.Context()))
				detachedReq.Body = io.NopCloser(bytes.NewReader(bodyCopy))
				discard := &discardResponseWriter{}
				if err := host.forwardToOfficial(discard, detachedReq, bodyCopy, deps, false); err != nil {
					logger.Errorf("official bidi async forward failed request_id=%s err=%v", requestID, err)
				}
			}()
			writeEmptyBidiAppendResponse(w)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// writeEmptyBidiAppendResponse 返回空 BidiAppendResponse（0 字节 proto），
// 与本地 forwarder 的受理响应一致，客户端据此继续建立 RunSSE 订阅。
func writeEmptyBidiAppendResponse(w http.ResponseWriter) {
	w.Header().Set("content-type", "application/proto")
	w.Header().Set("content-length", "0")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(nil)
}

// discardResponseWriter 丢弃所有响应（异步透传官方时无需回写客户端）。
type discardResponseWriter struct {
	header http.Header
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *discardResponseWriter) WriteHeader(int) {}

func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

// officialRunSSEHandler 包装 RunSSE 路由：request_id 命中官方模型登记时端到端
// 透传官方（SSE 流式），其余订阅交给本地 broker。
func (host *Host) officialRunSSEHandler(inner http.Handler, deps upstream.Dependencies) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			inner.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		requestID := decodeRunSSERequestID(body)
		if host.isOfficialRequest(requestID) {
			if err := host.forwardToOfficial(w, r, body, deps, false); err != nil {
				logger.Errorf("official runsse forward failed request_id=%s err=%v", requestID, err)
				// 转发失败且尚未写响应时，必须返回明确错误，否则客户端会挂起等待。
				http.Error(w, "official model forward failed", http.StatusBadGateway)
			}
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// isOfficialRequest 查询 request_id 是否已登记为官方模型请求。
// 命中时续期（活跃流的 TTL 顺延），超过 TTL 未活动则清理。
func (host *Host) isOfficialRequest(requestID string) bool {
	if requestID == "" {
		return false
	}
	value, ok := host.officialRequestIDs.Load(requestID)
	if !ok {
		return false
	}
	registeredAt, ok := value.(time.Time)
	if !ok || time.Since(registeredAt) > officialRequestTTL {
		host.officialRequestIDs.Delete(requestID)
		return false
	}
	host.officialRequestIDs.Store(requestID, time.Now())
	return true
}

// forwardToOfficial 把请求端到端透传官方 api2.cursor.sh（真实官方 token + checksum）。
// 复用 upstream.ForwardToUpstream 的流式拷贝，SSE/Connect 长连接均可透传。
// captureResponse 为 true 时把官方响应体交给 RefreshOfficialModelsFromResponse
// 刷新动态官方模型目录（仅 GetUsableModels 透传时启用）。
func (host *Host) forwardToOfficial(w http.ResponseWriter, r *http.Request, body []byte, deps upstream.Dependencies, captureResponse bool) error {
	if host.controlPlaneAuth == nil {
		return fmt.Errorf("Cursor 账号服务未初始化")
	}
	authorization, err := host.controlPlaneAuth.Authorization(r.Context())
	if err != nil {
		return err
	}
	targetURL := *r.URL
	targetURL.Scheme = "https"
	targetURL.Host = "api2.cursor.sh:443"
	reqCtx := &upstream.RequestContext{
		ResponseWriter: w,
		Request:        r,
		RawURL:         r.URL.String(),
		TargetURL:      &targetURL,
		Method:         strings.ToUpper(strings.TrimSpace(r.Method)),
		Headers:        r.Header.Clone(),
		ContentType:    strings.TrimSpace(r.Header.Get("content-type")),
		RequestBody:    body,
		Deps:           &deps,
	}
	options := upstream.ForwardOptions{
		BodyOverride: body,
		PatchHeaders: func(headers http.Header) {
			headers.Set("Authorization", authorization)
			headers.Set("x-cursor-checksum", upstream.BuildCursorChecksum(authorization))
		},
	}
	if captureResponse {
		options.CaptureResponse = func(responseBody []byte) {
			if err := upstream.RefreshOfficialModelsFromResponse(responseBody); err != nil {
				logger.Errorf("refresh official models failed: %v", err)
			}
		}
	}
	startedAt := time.Now()
	meta, err := upstream.ForwardToUpstream(reqCtx, options)
	elapsed := time.Since(startedAt)
	if err != nil {
		logger.Errorf("official forward failed path=%s body_len=%d elapsed=%s err=%v", r.URL.Path, len(body), elapsed, err)
		return err
	}
	logger.Infof("official forward done path=%s body_len=%d status=%d content_type=%q bytes=%d elapsed=%s", r.URL.Path, len(body), meta.StatusCode, meta.ContentType, meta.ResponseSize, elapsed)
	return nil
}

// hybridBidiHandler 按混合模式开关选择 BidiAppend 处理：开启时走官方模型分流
// 包装（官方模型透传），关闭时直接用本地 forwarder。
func hybridBidiHandler(hybridMode bool, host *Host, agentModule *forwarder.Module, routeDeps upstream.Dependencies) server.HandlerFunc {
	if !hybridMode {
		return server.HTTPHandlerAction(agentModule.LocalBidiHandler)
	}
	return server.HTTPHandlerAction(host.officialBidiAppendHandler(agentModule.LocalBidiHandler, routeDeps))
}

// hybridRunSSEHandler 按混合模式开关选择 RunSSE 处理。
func hybridRunSSEHandler(hybridMode bool, host *Host, agentModule *forwarder.Module, routeDeps upstream.Dependencies) server.HandlerFunc {
	if !hybridMode {
		return server.HTTPHandlerAction(agentModule.LocalRunSSE)
	}
	return server.HTTPHandlerAction(host.officialRunSSEHandler(agentModule.LocalRunSSE, routeDeps))
}

// hybridUsableModelsAction 按混合模式开关选择 GetUsableModels 处理：开启时透传
// 官方获取真实模型列表（并刷新动态官方目录），关闭时本地 mock（仅自定义模型）。
func hybridUsableModelsAction(hybridMode bool, host *Host, routeDeps upstream.Dependencies) server.HandlerFunc {
	mockAction := upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
		Name:          "usable_models",
		StatusCode:    http.StatusOK,
		MockProtoType: "aiserver.v1.GetUsableModelsResponse",
		MockBuilder:   upstream.UsableModelsMockBuilder,
	})
	if !hybridMode {
		return mockAction
	}
	return host.officialUsableModelsAction(mockAction, routeDeps)
}

// officialUsableModelsAction 包装 GetUsableModels 路由：官方账号已登录时透传
// 官方获取真实模型列表（同时刷新动态官方目录），未登录或透传失败时回退本地 mock
// （仅自定义模型）。
func (host *Host) officialUsableModelsAction(inner server.HandlerFunc, deps upstream.Dependencies) server.HandlerFunc {
	return func(ctx *server.Context) error {
		if ctx == nil || ctx.Request == nil {
			return inner(ctx)
		}
		if host.controlPlaneAuth == nil || !host.controlPlaneAuth.SignedIn() {
			return inner(ctx)
		}
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return inner(ctx)
		}
		ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
		if err := host.forwardToOfficial(ctx.Writer, ctx.Request, body, deps, true); err != nil {
			logger.Errorf("official usable models forward failed err=%v", err)
			return inner(ctx)
		}
		return nil
	}
}

// decodeBidiAppendRequestMeta 从 BidiAppend 请求体解析 request_id 与首个
// run_request/prewarm 的模型 ID（兼容 binary proto 与 JSON）。
func decodeBidiAppendRequestMeta(body []byte) (requestID string, modelID string) {
	// Cursor 客户端可能对请求体 gzip 压缩（Content-Encoding: gzip）：connect
	// handler（inner）会自动解压，但外层路由判定读的是原始 body，须先解压
	// 才能解析出 request_id/model，否则判定失败导致官方模型请求落入本地渠道。
	body = maybeGunzipBidiBody(body)
	appendReq := &aiserverv1.BidiAppendRequest{}
	if err := unmarshalConnectBody(body, appendReq); err != nil {
		return "", ""
	}
	requestID = strings.TrimSpace(protocol.ReadAppendRequestID(appendReq))
	if requestID == "" {
		return "", ""
	}
	message, _, err := protocol.DecodeAgentClientMessage(appendReq.GetData())
	if err != nil || message == nil {
		return requestID, ""
	}
	return requestID, extractRequestedModelIDFromMessage(message)
}

// maybeGunzipBidiBody 检测 gzip 魔数（0x1f 0x8b）并解压；非 gzip 或解压失败
// 时原样返回，调用方按原逻辑继续（inner connect handler 会自行解压）。
func maybeGunzipBidiBody(body []byte) []byte {
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return body
	}
	return decoded
}

// decodeRunSSERequestID 从 RunSSE 请求体解析 request_id（BidiRequestId）。
func decodeRunSSERequestID(body []byte) string {
	msg := &aiserverv1.BidiRequestId{}
	if err := unmarshalConnectBody(body, msg); err != nil {
		return ""
	}
	return strings.TrimSpace(protocol.ReadBidiRequestID(msg))
}

// unmarshalConnectBody 兼容 Connect 的 binary proto 与 JSON 请求体。
// 注意：不能对 proto 二进制做 TrimSpace——二进制尾部任意字节可能是
// ASCII 空白（0x20 等），会被误删导致 "invalid wire-format data"。
func unmarshalConnectBody(body []byte, message proto.Message) error {
	if len(body) == 0 {
		return fmt.Errorf("empty request body")
	}
	if body[0] == '{' {
		options := protojson.UnmarshalOptions{DiscardUnknown: true}
		return options.Unmarshal(body, message)
	}
	return proto.Unmarshal(body, message)
}

// extractRequestedModelIDFromMessage 从 AgentClientMessage 提取模型 ID
// （run_request 或 prewarm 的 requested_model.model_id / model_details.model_id）。
func extractRequestedModelIDFromMessage(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		if model := runRequest.GetRequestedModel(); model != nil {
			return strings.TrimSpace(model.GetModelId())
		}
		if details := runRequest.GetModelDetails(); details != nil {
			return strings.TrimSpace(details.GetModelId())
		}
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		if model := prewarm.GetRequestedModel(); model != nil {
			return strings.TrimSpace(model.GetModelId())
		}
		if details := prewarm.GetModelDetails(); details != nil {
			return strings.TrimSpace(details.GetModelId())
		}
	}
	return ""
}
