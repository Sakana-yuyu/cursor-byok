package protocol

import (
	"encoding/hex"
	"testing"

	"cursor/gen/agentv1"

	"google.golang.org/protobuf/proto"
)

func agentHeartbeatBytes(t *testing.T) []byte {
	t.Helper()
	message := &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_ClientHeartbeat{
			ClientHeartbeat: &agentv1.ClientHeartbeat{},
		},
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("proto.Marshal error = %v", err)
	}
	return payload
}

// TestDecodeBidiAppendAgentClientMessageDataBinary 验证新版客户端只发 data_binary
// 原始字节时能被正确解码并回放为 canonical hex。
func TestDecodeBidiAppendAgentClientMessageDataBinary(t *testing.T) {
	payload := agentHeartbeatBytes(t)
	message, kind, canonicalHex, err := DecodeBidiAppendAgentClientMessage("", payload)
	if err != nil {
		t.Fatalf("DecodeBidiAppendAgentClientMessage() error = %v", err)
	}
	if message == nil || message.GetClientHeartbeat() == nil {
		t.Fatal("message = nil or missing client heartbeat")
	}
	if kind != "client_heartbeat" {
		t.Fatalf("kind = %q, want client_heartbeat", kind)
	}
	if canonicalHex != hex.EncodeToString(payload) {
		t.Fatalf("canonicalHex mismatch")
	}
}

// TestDecodeBidiAppendAgentClientMessageHexLegacy 验证旧版 hex data 路径仍然工作。
func TestDecodeBidiAppendAgentClientMessageHexLegacy(t *testing.T) {
	payload := agentHeartbeatBytes(t)
	message, kind, _, err := DecodeBidiAppendAgentClientMessage(hex.EncodeToString(payload), nil)
	if err != nil {
		t.Fatalf("DecodeBidiAppendAgentClientMessage() error = %v", err)
	}
	if message == nil || message.GetClientHeartbeat() == nil {
		t.Fatal("message = nil or missing client heartbeat")
	}
	if kind != "client_heartbeat" {
		t.Fatalf("kind = %q, want client_heartbeat", kind)
	}
}

// TestDecodeBidiAppendAgentClientMessageConflict 验证 data 与 data_binary 同时存在
// 且内容不一致时被拒绝，避免按字段优先级静默吞掉冲突。
func TestDecodeBidiAppendAgentClientMessageConflict(t *testing.T) {
	payload := agentHeartbeatBytes(t)
	_, _, _, err := DecodeBidiAppendAgentClientMessage(hex.EncodeToString(payload), []byte("different-payload"))
	if err == nil {
		t.Fatal("DecodeBidiAppendAgentClientMessage() = nil error, want conflict rejection")
	}
}

// TestBidiAppendDebugData 验证 debug 表示优先使用 data_binary 的 hex。
func TestBidiAppendDebugData(t *testing.T) {
	payload := agentHeartbeatBytes(t)
	if got := BidiAppendDebugData("", payload); got != hex.EncodeToString(payload) {
		t.Fatalf("BidiAppendDebugData(binary) = %q, want hex payload", got)
	}
	if got := BidiAppendDebugData("abc123", nil); got != "abc123" {
		t.Fatalf("BidiAppendDebugData(hex) = %q, want abc123", got)
	}
}
