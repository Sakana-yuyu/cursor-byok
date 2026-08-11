package forwarder

import (
	"fmt"
	"strings"
	"testing"
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
