package client

import (
	"strings"
	"testing"
)

func TestRedactProbeBucketKeyForLog(t *testing.T) {
	t.Run("masks the api key segment", func(t *testing.T) {
		key := "openai|https://provider.example/v1|sk-AbCdEfGh12345678Wxyz"
		got := redactProbeBucketKeyForLog(key)
		if strings.Contains(got, "sk-AbCdEfGh12345678Wxyz") {
			t.Fatalf("plaintext api key leaked: %q", got)
		}
		if !strings.HasPrefix(got, "openai|https://provider.example/v1|") {
			t.Fatalf("provider and base url should stay readable, got %q", got)
		}
	})

	t.Run("keeps short prefix and suffix of the key", func(t *testing.T) {
		got := redactProbeBucketKeyForLog("openai|https://provider.example/v1|sk-AbCdEfGh12345678Wxyz")
		secret := strings.Split(got, "|")[2]
		if !strings.Contains(secret, "sk-A") || !strings.Contains(secret, "Wxyz") {
			t.Fatalf("want masked key keeping head/tail, got %q", secret)
		}
		if !strings.Contains(secret, "***") {
			t.Fatalf("want explicit mask marker, got %q", secret)
		}
	})

	t.Run("masks short secrets entirely", func(t *testing.T) {
		got := redactProbeBucketKeyForLog("openai|https://provider.example/v1|short")
		if strings.Contains(got, "short") {
			t.Fatalf("short secret must be fully masked, got %q", got)
		}
	})

	t.Run("leaves keys without secret segment untouched", func(t *testing.T) {
		got := redactProbeBucketKeyForLog("openai|https://provider.example/v1")
		if got != "openai|https://provider.example/v1" {
			t.Fatalf("two-segment key should pass through, got %q", got)
		}
	})
}
