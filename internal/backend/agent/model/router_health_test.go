package modeladapter

import (
	"context"
	"fmt"
	"testing"

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
