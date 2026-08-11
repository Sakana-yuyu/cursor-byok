package forwarder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func TestTextDeltaDebugFieldsHashesWithoutCopyingText(t *testing.T) {
	fields := textDeltaDebugFields("call-1", 2, 3, "private response")
	if fields["model_call_id"] != "call-1" || fields["provider_pass"] != 2 || fields["delta_count"] != 3 {
		t.Fatalf("unexpected identity fields: %#v", fields)
	}
	if fields["delta_bytes"] != len([]byte("private response")) || fields["delta_sha256"] == "" {
		t.Fatalf("unexpected summary fields: %#v", fields)
	}
	for key, value := range fields {
		if strings.Contains(key, "text") || strings.Contains(fmt.Sprint(value), "private response") {
			t.Fatalf("debug fields leaked text: %#v", fields)
		}
	}
}

func TestTextDeltaDebugFieldsReturnsNilForEmptyText(t *testing.T) {
	if fields := textDeltaDebugFields("call-1", 2, 3, ""); fields != nil {
		t.Fatalf("empty text fields = %#v, want nil", fields)
	}
}

func TestNextTextDeltaCountIgnoresEmptyText(t *testing.T) {
	if got := nextTextDeltaCount(4, ""); got != 4 {
		t.Fatalf("empty text count = %d, want 4", got)
	}
	if got := nextTextDeltaCount(4, "visible"); got != 5 {
		t.Fatalf("visible text count = %d, want 5", got)
	}
}

func TestRunSSEMessageDebugFieldsRecordsTextDigestWithoutMessagePayload(t *testing.T) {
	const text = "hello"
	sum := sha256.Sum256([]byte(text))
	fields := runSSEMessageDebugFields(7, buildTextDeltaMessage(text))

	if fields["cursor"] != 7 || fields["message_case"] != "interaction_update" {
		t.Fatalf("unexpected message identity: %#v", fields)
	}
	if fields["text_delta_bytes"] != len(text) || fields["text_delta_sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected text digest: %#v", fields)
	}
	if _, exists := fields["message"]; exists {
		t.Fatalf("normal RunSSE debug fields must not contain full message: %#v", fields)
	}
	for key, value := range fields {
		if strings.Contains(fmt.Sprint(value), text) {
			t.Fatalf("RunSSE debug fields leaked text at %s: %#v", key, fields)
		}
	}
}

func TestRunSSEMessageDebugFieldsOmitsDigestForNonTextMessage(t *testing.T) {
	fields := runSSEMessageDebugFields(3, &agentv1.AgentServerMessage{})
	if _, exists := fields["text_delta_bytes"]; exists {
		t.Fatalf("non-text message unexpectedly contains text digest: %#v", fields)
	}
}
