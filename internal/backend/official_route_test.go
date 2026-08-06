package backend

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
	"cursor/internal/backend/server/upstream"
)

func encodeBidiAppendBody(t *testing.T, requestID string, message *agentv1.AgentClientMessage) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	appendReq := &aiserverv1.BidiAppendRequest{
		RequestId:   &aiserverv1.BidiRequestId{RequestId: requestID},
		AppendSeqno: 1,
		Data:        hex.EncodeToString(data),
	}
	body, err := proto.Marshal(appendReq)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDecodeBidiAppendRequestMeta(t *testing.T) {
	t.Run("run_request_official_model", func(t *testing.T) {
		message := &agentv1.AgentClientMessage{
			Message: &agentv1.AgentClientMessage_RunRequest{
				RunRequest: &agentv1.AgentRunRequest{
					ConversationId: proto.String("conv-1"),
					RequestedModel: &agentv1.RequestedModel{ModelId: "claude-sonnet-4-5"},
				},
			},
		}
		body := encodeBidiAppendBody(t, "req-1", message)
		requestID, modelID := decodeBidiAppendRequestMeta(body)
		if requestID != "req-1" {
			t.Fatalf("requestID: got %q, want req-1", requestID)
		}
		if modelID != "claude-sonnet-4-5" {
			t.Fatalf("modelID: got %q, want claude-sonnet-4-5", modelID)
		}
	})

	t.Run("custom_model", func(t *testing.T) {
		message := &agentv1.AgentClientMessage{
			Message: &agentv1.AgentClientMessage_RunRequest{
				RunRequest: &agentv1.AgentRunRequest{
					ConversationId: proto.String("conv-2"),
					RequestedModel: &agentv1.RequestedModel{ModelId: "deepseek-v4-flash"},
				},
			},
		}
		body := encodeBidiAppendBody(t, "req-2", message)
		requestID, modelID := decodeBidiAppendRequestMeta(body)
		if requestID != "req-2" {
			t.Fatalf("requestID: got %q", requestID)
		}
		if modelID != "deepseek-v4-flash" {
			t.Fatalf("modelID: got %q", modelID)
		}
	})

	t.Run("non_run_request_message", func(t *testing.T) {
		// exec_client_message 消息无模型字段。
		message := &agentv1.AgentClientMessage{
			Message: &agentv1.AgentClientMessage_ExecClientMessage{
				ExecClientMessage: &agentv1.ExecClientMessage{},
			},
		}
		body := encodeBidiAppendBody(t, "req-3", message)
		requestID, modelID := decodeBidiAppendRequestMeta(body)
		if requestID != "req-3" {
			t.Fatalf("requestID: got %q", requestID)
		}
		if modelID != "" {
			t.Fatalf("modelID: got %q, want empty", modelID)
		}
	})

	t.Run("invalid_body", func(t *testing.T) {
		requestID, modelID := decodeBidiAppendRequestMeta([]byte("not-proto"))
		if requestID != "" || modelID != "" {
			t.Fatalf("expected empty meta for invalid body, got %q/%q", requestID, modelID)
		}
	})

	t.Run("gzip_compressed_body", func(t *testing.T) {
		// Cursor 客户端 BidiAppend 请求体可能 gzip 压缩（Content-Encoding: gzip）。
		// 回归：外层路由判定须自行解压，否则 request_id/model 解析为空，
		// 官方模型请求落入本地渠道报 "model channel not available"。
		message := &agentv1.AgentClientMessage{
			Message: &agentv1.AgentClientMessage_RunRequest{
				RunRequest: &agentv1.AgentRunRequest{
					ConversationId: proto.String("conv-gz"),
					RequestedModel: &agentv1.RequestedModel{ModelId: "composer-2.5"},
				},
			},
		}
		body := encodeBidiAppendBody(t, "req-gz", message)
		var buf bytes.Buffer
		gzWriter := gzip.NewWriter(&buf)
		if _, err := gzWriter.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := gzWriter.Close(); err != nil {
			t.Fatal(err)
		}
		requestID, modelID := decodeBidiAppendRequestMeta(buf.Bytes())
		if requestID != "req-gz" {
			t.Fatalf("requestID: got %q, want req-gz", requestID)
		}
		if modelID != "composer-2.5" {
			t.Fatalf("modelID: got %q, want composer-2.5", modelID)
		}
	})
}

func TestDecodeRunSSERequestID(t *testing.T) {
	msg := &aiserverv1.BidiRequestId{RequestId: "sse-req-1"}
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeRunSSERequestID(body); got != "sse-req-1" {
		t.Fatalf("got %q, want sse-req-1", got)
	}
	if got := decodeRunSSERequestID([]byte("garbage")); got != "" {
		t.Fatalf("expected empty for garbage, got %q", got)
	}
}

func TestExtractRequestedModelIDFromMessage(t *testing.T) {
	message := &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_PrewarmRequest{
			PrewarmRequest: &agentv1.PrewarmRequest{
				RequestedModel: &agentv1.RequestedModel{ModelId: "gpt-5"},
			},
		},
	}
	if got := extractRequestedModelIDFromMessage(message); got != "gpt-5" {
		t.Fatalf("got %q, want gpt-5", got)
	}
	if got := extractRequestedModelIDFromMessage(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

// fakeAuthProvider 测试用 AuthorizationProvider。
type fakeAuthProvider struct {
	signedIn      bool
	authorization string
}

func (f *fakeAuthProvider) SignedIn() bool {
	return f.signedIn
}

func (f *fakeAuthProvider) Authorization(ctx context.Context) (string, error) {
	if !f.signedIn {
		return "", fmt.Errorf("not signed in")
	}
	return f.authorization, nil
}

// fakeHTTPClient 返回 401 的假 HTTP 客户端，避免测试真实出网到 api2.cursor.sh。
type fakeHTTPClient struct{}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// errHTTPClient 始终返回错误的假 HTTP 客户端（测试转发失败路径）。
type errHTTPClient struct{}

func (f *errHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("simulated upstream failure")
}

// officialTestDeps 返回带假 HTTP 客户端（不透网）的 Dependencies。
func officialTestDeps() upstream.Dependencies {
	return upstream.Dependencies{HTTPClient: &fakeHTTPClient{}}
}

// buildTestHost 构造测试用 Host（带 officialRequestIDs）。
func buildTestHost() *Host {
	return &Host{
		officialRequestIDs: sync.Map{},
	}
}

func TestOfficialBidiAppendRegistration(t *testing.T) {
	body := encodeBidiAppendBody(t, "req-official", &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_RunRequest{
			RunRequest: &agentv1.AgentRunRequest{
				ConversationId: proto.String("conv-1"),
				RequestedModel: &agentv1.RequestedModel{ModelId: "claude-sonnet-4-5"},
			},
		},
	})
	localBody := encodeBidiAppendBody(t, "req-local", &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_RunRequest{
			RunRequest: &agentv1.AgentRunRequest{
				ConversationId: proto.String("conv-2"),
				RequestedModel: &agentv1.RequestedModel{ModelId: "deepseek-v4-flash"},
			},
		},
	})
	noModelBody := encodeBidiAppendBody(t, "req-nomodel", &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_ExecClientMessage{
			ExecClientMessage: &agentv1.ExecClientMessage{},
		},
	})

	t.Run("official_model_registers", func(t *testing.T) {
		host := buildTestHost()
		host.controlPlaneAuth = &fakeAuthProvider{signedIn: true}
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("should not reach local") })
		handler := host.officialBidiAppendHandler(inner, officialTestDeps())
		req := httptest.NewRequest(http.MethodPost, "/bidi", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !host.isOfficialRequest("req-official") {
			t.Fatal("expected request registered as official")
		}
	})

	t.Run("custom_model_goes_local", func(t *testing.T) {
		host := buildTestHost()
		host.controlPlaneAuth = &fakeAuthProvider{signedIn: true}
		reachedLocal := false
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedLocal = true })
		handler := host.officialBidiAppendHandler(inner, officialTestDeps())
		req := httptest.NewRequest(http.MethodPost, "/bidi", bytes.NewReader(localBody))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !reachedLocal {
			t.Fatal("expected local handler for custom model")
		}
		if host.isOfficialRequest("req-local") {
			t.Fatal("custom model must not be registered official")
		}
	})

	t.Run("not_signed_in_does_not_register", func(t *testing.T) {
		host := buildTestHost()
		host.controlPlaneAuth = &fakeAuthProvider{signedIn: false}
		reachedLocal := false
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedLocal = true })
		handler := host.officialBidiAppendHandler(inner, officialTestDeps())
		req := httptest.NewRequest(http.MethodPost, "/bidi", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !reachedLocal {
			t.Fatal("expected local fallback when not signed in")
		}
		if host.isOfficialRequest("req-official") {
			t.Fatal("must not register official when not signed in")
		}
	})

	t.Run("non_model_message_keeps_registration", func(t *testing.T) {
		host := buildTestHost()
		host.controlPlaneAuth = &fakeAuthProvider{signedIn: true}
		// 先登记官方，再发无模型消息，不应撤销。
		host.officialRequestIDs.Store("req-nomodel", time.Now())
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		handler := host.officialBidiAppendHandler(inner, officialTestDeps())
		req := httptest.NewRequest(http.MethodPost, "/bidi", bytes.NewReader(noModelBody))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		_ = rec
		if !host.isOfficialRequest("req-nomodel") {
			t.Fatal("registration must survive non-model messages")
		}
	})

	t.Run("revert_to_local_on_custom_model", func(t *testing.T) {
		host := buildTestHost()
		host.controlPlaneAuth = &fakeAuthProvider{signedIn: true}
		// 先登记官方（同一 requestID），后续出现本地模型 → 撤销并走本地。
		host.officialRequestIDs.Store("req-local", time.Now())
		reachedLocal := false
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedLocal = true })
		handler := host.officialBidiAppendHandler(inner, officialTestDeps())
		req := httptest.NewRequest(http.MethodPost, "/bidi", bytes.NewReader(localBody))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !reachedLocal {
			t.Fatal("expected local handler after revert")
		}
		if host.isOfficialRequest("req-local") {
			t.Fatal("expected registration removed after revert")
		}
	})

	t.Run("official_forward_async_immediate_ack", func(t *testing.T) {
		host := buildTestHost()
		host.controlPlaneAuth = &fakeAuthProvider{signedIn: true, authorization: "fake-token"}
		// 已登记官方 → 立即受理（200 空响应），异步透传失败仅记日志不阻塞客户端。
		host.officialRequestIDs.Store("req-fail", time.Now())
		inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("should not reach local") })
		deps := upstream.Dependencies{HTTPClient: &errHTTPClient{}}
		handler := host.officialBidiAppendHandler(inner, deps)
		req := httptest.NewRequest(http.MethodPost, "/bidi", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 immediate ack, got %d", rec.Code)
		}
		if len(rec.Body.Bytes()) != 0 {
			t.Fatalf("expected empty ack body, got %d bytes", len(rec.Body.Bytes()))
		}
	})
}

func TestIsOfficialRequestTTL(t *testing.T) {
	host := buildTestHost()
	host.officialRequestIDs.Store("req-ttl", time.Now().Add(-31*time.Minute))
	if host.isOfficialRequest("req-ttl") {
		t.Fatal("expected expired request to be cleaned")
	}
	if _, ok := host.officialRequestIDs.Load("req-ttl"); ok {
		t.Fatal("expected expired entry deleted")
	}
	// 有效登记应命中并续期。
	host.officialRequestIDs.Store("req-live", time.Now().Add(-20*time.Minute))
	if !host.isOfficialRequest("req-live") {
		t.Fatal("expected live request to match")
	}
	value, _ := host.officialRequestIDs.Load("req-live")
	registeredAt, _ := value.(time.Time)
	if time.Since(registeredAt) > time.Minute {
		t.Fatal("expected TTL renewed on hit")
	}
}
