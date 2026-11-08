package modeladapter

import (
	"context"
	"fmt"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

func TestChannelHealthStaleSuccessDoesNotClearNewerFailure(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	observed := router.channelHealthVersion(channel)
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	router.clearChannelFailure(channel, observed)

	router.healthMu.Lock()
	_, exists := router.healthByChannel[channelHealthMapKey(channel)]
	router.healthMu.Unlock()
	if !exists {
		t.Fatal("stale success cleared a newer channel failure")
	}
}

func TestChannelHealthRetainsLaterDeadlineAcrossConcurrentFailures(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	router.recordChannelFailure(channel, fmt.Errorf("provider status=401"))
	router.healthMu.Lock()
	longDeadline := router.healthByChannel[channelHealthMapKey(channel)].cooldownUntil
	router.healthMu.Unlock()

	router.recordChannelFailure(channel, fmt.Errorf("provider status=503"))
	router.healthMu.Lock()
	retainedDeadline := router.healthByChannel[channelHealthMapKey(channel)].cooldownUntil
	router.healthMu.Unlock()
	if retainedDeadline.Before(longDeadline) {
		t.Fatalf("short failure shortened cooldown from %s to %s", longDeadline, retainedDeadline)
	}

	observed := router.channelHealthVersion(channel)
	router.clearChannelFailure(channel, observed)
	router.healthMu.Lock()
	_, exists := router.healthByChannel[channelHealthMapKey(channel)]
	router.healthMu.Unlock()
	if exists {
		t.Fatal("success that observed the current failure did not clear cooldown")
	}
}

func TestChannelHealthIgnoresCooldownAfterMaterialConfigurationChange(t *testing.T) {
	oldChannel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://old.example/v1", APIKey: "sk-old", Model: "model-a"}
	newChannel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://new.example/v1", APIKey: "sk-new", Model: "model-a"}
	router := NewRouter(nil)
	router.recordChannelFailure(oldChannel, fmt.Errorf("provider status=401"))

	selected := router.preferHealthyChannel([]*legacyruntime.ResolvedChannel{newChannel})
	if selected != newChannel {
		t.Fatalf("repaired channel was not selected: %#v", selected)
	}
	router.healthMu.Lock()
	_, newExists := router.healthByChannel[channelHealthMapKey(newChannel)]
	_, oldExists := router.healthByChannel[channelHealthMapKey(oldChannel)]
	router.healthMu.Unlock()
	if newExists || !oldExists {
		t.Fatal("configuration-scoped health records were applied to the wrong channel revision")
	}
}

func TestOldConfigurationFailureCannotOverwriteCurrentCooldown(t *testing.T) {
	oldChannel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://old.example/v1", APIKey: "sk-old", Model: "model-a"}
	newChannel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://new.example/v1", APIKey: "sk-new", Model: "model-a"}
	router := NewRouter(&diagnosticsResolverStub{channels: []*legacyruntime.ResolvedChannel{newChannel}})
	router.recordChannelFailure(newChannel, fmt.Errorf("provider status=401"))
	router.healthMu.Lock()
	currentDeadline := router.healthByChannel[channelHealthMapKey(newChannel)].cooldownUntil
	router.healthMu.Unlock()

	router.recordChannelFailure(oldChannel, fmt.Errorf("provider status=503"))
	router.healthMu.Lock()
	retainedDeadline := router.healthByChannel[channelHealthMapKey(newChannel)].cooldownUntil
	_, oldExists := router.healthByChannel[channelHealthMapKey(oldChannel)]
	router.healthMu.Unlock()
	if retainedDeadline != currentDeadline || !oldExists {
		t.Fatalf("old configuration failure corrupted current health: retained=%s current=%s oldExists=%v", retainedDeadline, currentDeadline, oldExists)
	}
	snapshot := router.ProviderDiagnostics(context.Background())
	if len(snapshot.Channels) != 1 || snapshot.Channels[0].HealthState != ProviderChannelHealthCooldown {
		t.Fatalf("current configuration cooldown disappeared from diagnostics: %#v", snapshot)
	}
}

func TestChannelConfigurationFingerprintIncludesAnthropicAuthMode(t *testing.T) {
	legacy := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", Model: "claude-test", AnthropicAuthMode: "legacy_dual"}
	bearer := *legacy
	bearer.AnthropicAuthMode = "bearer"
	if channelConfigurationFingerprint(legacy) == channelConfigurationFingerprint(&bearer) {
		t.Fatal("Anthropic auth mode did not change channel fingerprint")
	}
}

func TestChannelConfigurationFingerprintIncludesEnableFlags(t *testing.T) {
	enabled := &legacyruntime.ResolvedChannel{ID: "channel-a", CustomHeadersEnabled: true, CustomHeadersJSON: `{"Authorization":"Bearer secret"}`}
	disabled := *enabled
	disabled.CustomHeadersEnabled = false
	if channelConfigurationFingerprint(enabled) == channelConfigurationFingerprint(&disabled) {
		t.Fatal("custom-header enablement did not change channel fingerprint")
	}
}

func TestChannelConfigurationFingerprintDoesNotExposeCredentialMaterial(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", BaseURL: "https://user:password@example.com?token=secret", APIKey: "sk-secret"}
	fingerprint := channelConfigurationFingerprint(channel)
	if len(fingerprint) != 64 || fingerprint == "" {
		t.Fatalf("unexpected fingerprint %q", fingerprint)
	}
	if fingerprint == channel.APIKey || fingerprint == channel.BaseURL {
		t.Fatal("configuration fingerprint exposed raw credential material")
	}
}

// cooldownRemaining 读取渠道当前冷却的剩余时长；无记录时返回 0。
func cooldownRemaining(t *testing.T, router *Router, channel *legacyruntime.ResolvedChannel) time.Duration {
	t.Helper()
	router.healthMu.Lock()
	defer router.healthMu.Unlock()
	entry, ok := router.healthByChannel[channelHealthMapKey(channel)]
	if !ok {
		return 0
	}
	if remaining := time.Until(entry.cooldownUntil); remaining > 0 {
		return remaining
	}
	return 0
}

func TestRateLimitCooldownEscalatesWithoutRetryAfter(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	want := []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		30 * time.Minute, // 1min<<5 = 32min 封顶到 30min
		30 * time.Minute,
	}
	for i, expected := range want {
		router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
		got := cooldownRemaining(t, router, channel)
		if got < expected-2*time.Second || got > expected+2*time.Second {
			t.Fatalf("failure #%d cooldown = %s, want ~%s", i+1, got, expected)
		}
	}
}

func TestRateLimitCooldownPrefersRetryAfterOverBackoff(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	// 上游给了 Retry-After=2min：优先于指数退避（首败退避本应为 1min）
	router.recordChannelFailure(channel, &HTTPStatusError{
		StatusCode: 429,
		Message:    "provider status=429",
		Headers:    map[string][]string{"Retry-After": {"120"}},
	})
	if got := cooldownRemaining(t, router, channel); got < 110*time.Second || got > 130*time.Second {
		t.Fatalf("Retry-After cooldown = %s, want ~2m", got)
	}
}

func TestRateLimitFailureCountForgetsAfterWindow(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	router.healthMu.Lock()
	if failures := router.healthByChannel[channelHealthMapKey(channel)].failures; failures != 2 {
		router.healthMu.Unlock()
		t.Fatalf("failures = %d, want 2", failures)
	}
	// 遗忘窗口外的旧失败：计数应从 1 重新开始
	entry := router.healthByChannel[channelHealthMapKey(channel)]
	entry.lastFailureAt = time.Now().Add(-rateLimitForgetWindow - time.Minute)
	router.healthByChannel[channelHealthMapKey(channel)] = entry
	router.healthMu.Unlock()

	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	router.healthMu.Lock()
	failures := router.healthByChannel[channelHealthMapKey(channel)].failures
	router.healthMu.Unlock()
	if failures != 1 {
		t.Fatalf("failures after forget window = %d, want 1", failures)
	}
}

func TestQuotaExhaustedCooldownLongerThanAuthFailure(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	router.recordChannelFailure(channel, fmt.Errorf("provider status=402"))
	quota := cooldownRemaining(t, router, channel)
	if quota < channelFailureQuotaCooldown-2*time.Second || quota > channelFailureQuotaCooldown+2*time.Second {
		t.Fatalf("402 cooldown = %s, want ~%s", quota, channelFailureQuotaCooldown)
	}

	other := &legacyruntime.ResolvedChannel{ID: "channel-b", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-b", Model: "model-a"}
	router.recordChannelFailure(other, fmt.Errorf("provider status=401"))
	auth := cooldownRemaining(t, router, other)
	if auth < 9*time.Minute || auth > 11*time.Minute {
		t.Fatalf("401 cooldown = %s, want ~10m", auth)
	}
}

func TestSuccessClearsFailureCount(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	observed := router.channelHealthVersion(channel)
	router.clearChannelFailure(channel, observed)
	router.healthMu.Lock()
	_, exists := router.healthByChannel[channelHealthMapKey(channel)]
	router.healthMu.Unlock()
	if exists {
		t.Fatal("success did not clear failure state")
	}
	// 清零后的下一次失败应从退避序列起点开始
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	if got := cooldownRemaining(t, router, channel); got < 58*time.Second || got > 62*time.Second {
		t.Fatalf("cooldown after reset = %s, want ~1m", got)
	}
}

func TestNonCooldownFailureDoesNotBlockLaterCooldown(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "channel-a", Provider: "openai", BaseURL: "https://provider.example/v1", APIKey: "sk-a", Model: "model-a"}
	router := NewRouter(nil)
	// 400 永久错误不产生冷却
	router.recordChannelFailure(channel, fmt.Errorf("provider status=400"))
	if got := cooldownRemaining(t, router, channel); got != 0 {
		t.Fatalf("400 error produced cooldown %s, want 0", got)
	}
	// 随后的 429 正常产生指数退避冷却
	router.recordChannelFailure(channel, fmt.Errorf("provider status=429"))
	if got := cooldownRemaining(t, router, channel); got < 58*time.Second || got > 62*time.Second {
		t.Fatalf("429 cooldown after non-cooldown failure = %s, want ~1m", got)
	}
}
