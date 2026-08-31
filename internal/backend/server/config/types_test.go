package config

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLoadDoesNotBackfillLocalGoalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := []byte("backendListenAddr: 127.0.0.1:18090\nproxyListenAddr: 127.0.0.1:18080\n")
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := NewStore(path, "").Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.BackendListenAddr != "127.0.0.1:18090" {
		t.Fatalf("BackendListenAddr = %q, want legacy value", got.BackendListenAddr)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if yamlHasKey(persisted, "goal") {
		t.Fatal("local goal config must not be backfilled after switching to Cursor native /goal")
	}
}

func TestSkillScanDefaultsToEmptyEnabledSkills(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.SkillMCPScan.EnabledSkills) != 0 {
		t.Fatalf("EnabledSkills = %v, want empty opt-in whitelist", cfg.SkillMCPScan.EnabledSkills)
	}
}

func TestLegacyDisabledSkillsDoNotBecomeEnabledSkills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := []byte("backendListenAddr: 127.0.0.1:18090\nproxyListenAddr: 127.0.0.1:18080\nskillMcpScan:\n  enabled: true\n  disabledSkills:\n    old-skill: true\n")
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := NewStore(path, "").Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.SkillMCPScan.EnabledSkills) != 0 {
		t.Fatalf("legacy disabledSkills must not migrate into enabledSkills: %v", got.SkillMCPScan.EnabledSkills)
	}
}

func TestNormalizeModelAdapterConfigsDeduplicatesChannelsAndSetsContext(t *testing.T) {
	first := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}
	duplicate := first
	duplicate.DisplayName = "GPT-5.6 Luna duplicate"
	first.GroupName = "OAI 供应商"
	duplicate.GroupName = first.GroupName

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, duplicate})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 1", len(got))
	}
	if got[0].ContextWindowTokens != 272_000 {
		t.Errorf("ContextWindowTokens = %d, want 272000", got[0].ContextWindowTokens)
	}
	if got[0].GroupName != "OAI 供应商" {
		t.Errorf("GroupName = %q, want %q", got[0].GroupName, "OAI 供应商")
	}
}

func TestNormalizeModelAdapterConfigsKeepsSameChannelInDifferentGroups(t *testing.T) {
	base := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}
	first := base
	first.GroupName = "供应商 A"
	second := base
	second.GroupName = "供应商 B"

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, second})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 2", len(got))
	}
	if got[0].GroupName != "供应商 A" || got[1].GroupName != "供应商 B" {
		t.Errorf("group names = [%q, %q], want [供应商 A, 供应商 B]", got[0].GroupName, got[1].GroupName)
	}
}

func TestDefaultConfigMirrorCaptureDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MirrorCapture.Enabled {
		t.Fatal("mirrorCapture must default to disabled")
	}
	if len(cfg.MirrorCapture.Hosts) != 3 {
		t.Fatalf("default mirror hosts = %d, want 3 (openai/anthropic/gemini)", len(cfg.MirrorCapture.Hosts))
	}
}

func TestNormalizeConfigKeepsMirrorCapture(t *testing.T) {
	input := DefaultConfig()
	input.MirrorCapture = MirrorCaptureConfig{
		Enabled: true,
		Hosts:   []string{"custom.example.com"},
	}
	got, err := NormalizeConfig(input)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if !got.MirrorCapture.Enabled {
		t.Fatal("MirrorCapture.Enabled must survive NormalizeConfig")
	}
	if len(got.MirrorCapture.Hosts) != 1 || got.MirrorCapture.Hosts[0] != "custom.example.com" {
		t.Fatalf("MirrorCapture.Hosts = %v, want [custom.example.com]", got.MirrorCapture.Hosts)
	}
}

func TestManagerMirrorCaptureAccessors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := DefaultConfig()
	// 空 Hosts 应回落 DefaultMirrorHosts；默认 Enabled=false。
	cfg.MirrorCapture = MirrorCaptureConfig{Enabled: false, Hosts: nil}
	store := NewStore(path, "")
	if _, err := store.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	manager, err := NewManager(context.Background(), store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if manager.MirrorCaptureEnabled(context.Background()) {
		t.Fatal("MirrorCaptureEnabled must default to false")
	}
	if hosts := manager.MirrorCaptureHosts(); len(hosts) != 3 {
		t.Fatalf("MirrorCaptureHosts() = %v, want fallback DefaultMirrorHosts (len 3)", hosts)
	}

	// manager.Save 更新 current 后热加载接口应同步读到新值。
	cfg.MirrorCapture = MirrorCaptureConfig{Enabled: true, Hosts: []string{"api.openai.com"}}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save(2) error = %v", err)
	}
	if !manager.MirrorCaptureEnabled(context.Background()) {
		t.Fatal("MirrorCaptureEnabled must reflect updated config")
	}
	if hosts := manager.MirrorCaptureHosts(); len(hosts) != 1 || hosts[0] != "api.openai.com" {
		t.Fatalf("MirrorCaptureHosts() = %v, want [api.openai.com]", hosts)
	}
}

func TestDefaultConfigMirrorCaptureProtocolFidelityDisabledByDefault(t *testing.T) {
	if DefaultConfig().MirrorCapture.ProtocolFidelity {
		t.Fatal("mirrorCapture.protocolFidelity must default to disabled")
	}
}

func TestNormalizeConfigKeepsMirrorCaptureProtocolFidelity(t *testing.T) {
	input := DefaultConfig()
	input.MirrorCapture = MirrorCaptureConfig{Enabled: true, ProtocolFidelity: true, Hosts: []string{"custom.example.com"}}
	got, err := NormalizeConfig(input)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if !got.MirrorCapture.ProtocolFidelity {
		t.Fatal("MirrorCapture.ProtocolFidelity must survive NormalizeConfig")
	}
}

// TestResolveMirrorCaptureHosts 固定「保真开关决定是否并入 Cursor relay 域名」这条规则：
// 关闭时的域名集合必须与既有行为逐项一致，开启时才扩大到协议帧所在的 relay 入口。
func TestResolveMirrorCaptureHosts(t *testing.T) {
	off := ResolveMirrorCaptureHosts(MirrorCaptureConfig{Enabled: true})
	if len(off) != len(DefaultMirrorHosts) {
		t.Fatalf("fidelity off hosts = %v, want %v", off, DefaultMirrorHosts)
	}
	for index, host := range DefaultMirrorHosts {
		if off[index] != host {
			t.Fatalf("fidelity off hosts = %v, want %v", off, DefaultMirrorHosts)
		}
	}

	on := ResolveMirrorCaptureHosts(MirrorCaptureConfig{Enabled: true, ProtocolFidelity: true})
	for _, relayHost := range CursorRelayMirrorHosts {
		if !containsMirrorHost(on, relayHost) {
			t.Fatalf("fidelity on hosts = %v, want to contain %q", on, relayHost)
		}
	}
	if len(on) != len(DefaultMirrorHosts)+len(CursorRelayMirrorHosts) {
		t.Fatalf("fidelity on hosts = %v, want %d entries", on, len(DefaultMirrorHosts)+len(CursorRelayMirrorHosts))
	}

	// 大小写、空白与重复项都必须被归一化掉，否则 isMirrorHost 的小写比较会漏匹配。
	messy := ResolveMirrorCaptureHosts(MirrorCaptureConfig{
		ProtocolFidelity: true,
		Hosts:            []string{" API2.Cursor.SH ", "api.openai.com", "api.openai.com", ""},
	})
	if len(messy) != 1+len(CursorRelayMirrorHosts) {
		t.Fatalf("normalized hosts = %v, want %d entries", messy, 1+len(CursorRelayMirrorHosts))
	}
	if !containsMirrorHost(messy, "api2.cursor.sh") || !containsMirrorHost(messy, "api.openai.com") {
		t.Fatalf("normalized hosts = %v", messy)
	}
}

func TestManagerMirrorCaptureProtocolFidelityHotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := DefaultConfig()
	store := NewStore(path, "")
	if _, err := store.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	manager, err := NewManager(context.Background(), store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if manager.MirrorCaptureProtocolFidelity() {
		t.Fatal("MirrorCaptureProtocolFidelity must default to false")
	}
	if containsMirrorHost(manager.MirrorCaptureHosts(), "api2.cursor.sh") {
		t.Fatal("relay hosts must stay out of the mirror list while fidelity is off")
	}

	cfg.MirrorCapture.ProtocolFidelity = true
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save(2) error = %v", err)
	}
	if !manager.MirrorCaptureProtocolFidelity() {
		t.Fatal("MirrorCaptureProtocolFidelity must reflect updated config")
	}
	if !containsMirrorHost(manager.MirrorCaptureHosts(), "api2.cursor.sh") {
		t.Fatalf("MirrorCaptureHosts() = %v, want relay hosts merged in", manager.MirrorCaptureHosts())
	}
}

func containsMirrorHost(hosts []string, want string) bool {
	for _, host := range hosts {
		if host == want {
			return true
		}
	}
	return false
}

func TestNormalizeListenAddrRejectsNonLoopbackByDefault(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8787", "192.168.1.1:8787", "[::]:8787"} {
		cfg := DefaultConfig()
		cfg.BackendListenAddr = addr
		if _, err := NormalizeConfig(cfg); err == nil {
			t.Fatalf("NormalizeConfig(%q) error = nil, want non-loopback rejection", addr)
		}
	}
}

func TestNormalizeListenAddrAllowsLoopbackHosts(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8787", "[::1]:8787", "localhost:8787"} {
		cfg := DefaultConfig()
		cfg.BackendListenAddr = addr
		got, err := NormalizeConfig(cfg)
		if err != nil {
			t.Fatalf("NormalizeConfig(%q) error = %v", addr, err)
		}
		host, _, splitErr := net.SplitHostPort(got.BackendListenAddr)
		if splitErr != nil {
			t.Fatalf("SplitHostPort(%q) error = %v", got.BackendListenAddr, splitErr)
		}
		if !isLoopbackListenHost(host) {
			t.Fatalf("normalized host %q for input %q is not loopback", host, addr)
		}
	}
}

func TestNormalizeListenAddrAllowsNonLoopbackWithOptIn(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowNonLoopbackListen = true
	cfg.BackendListenAddr = "0.0.0.0:8787"
	cfg.ProxyListenAddr = "192.168.0.5:8788"
	got, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if got.BackendListenAddr != "0.0.0.0:8787" {
		t.Fatalf("BackendListenAddr = %q, want 0.0.0.0:8787", got.BackendListenAddr)
	}
	if got.ProxyListenAddr != "192.168.0.5:8788" {
		t.Fatalf("ProxyListenAddr = %q, want 192.168.0.5:8788", got.ProxyListenAddr)
	}
}
