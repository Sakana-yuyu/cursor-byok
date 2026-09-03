package forwarder

import (
	"fmt"
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

func TestProviderDefaultMaxOutputTokensUsesSafeFallback(t *testing.T) {
	if providerDefaultMaxOutputTokens != 4096 {
		t.Fatalf("providerDefaultMaxOutputTokens = %d, want 4096", providerDefaultMaxOutputTokens)
	}
}

func TestMaxTokensRecoveryRecognizesProviderLimit(t *testing.T) {
	const message = "openai adapter status=400 body=max_tokens (65536) exceeds limit (4096). This protects against Neurons quota abuse."

	tests := []struct {
		name      string
		err       error
		wantMatch bool
		wantLimit int
	}{
		{
			name:      "structured exact error",
			err:       &modeladapter.HTTPStatusError{StatusCode: 400, Message: message},
			wantMatch: true,
			wantLimit: 4096,
		},
		{
			name:      "wrapped exact error",
			err:       fmt.Errorf("channel failed: %w", &modeladapter.HTTPStatusError{StatusCode: 400, Message: message}),
			wantMatch: true,
			wantLimit: 4096,
		},
		{
			name:      "non 400",
			err:       &modeladapter.HTTPStatusError{StatusCode: 500, Message: message},
			wantMatch: false,
			wantLimit: 4096,
		},
		{
			name:      "unrelated 400",
			err:       &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid api key"},
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMaxTokensExceededError(tt.err); got != tt.wantMatch {
				t.Fatalf("isMaxTokensExceededError() = %v, want %v", got, tt.wantMatch)
			}
			if got := parseMaxTokensLimitFromError(tt.err); got != tt.wantLimit {
				t.Fatalf("parseMaxTokensLimitFromError() = %d, want %d", got, tt.wantLimit)
			}
		})
	}
}
