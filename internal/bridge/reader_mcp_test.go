package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReaderMCPScriptCandidates(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\test`)
	candidates := readerMCPScriptCandidates()
	if len(candidates) != 3 {
		t.Fatalf("应返回 3 个候选, got %d", len(candidates))
	}
	for _, c := range candidates {
		if c == "" || !filepath.IsAbs(c) {
			t.Errorf("候选应为绝对路径: %q", c)
		}
	}
}

func TestDetectVisionReaderScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	script := filepath.Join(home, ".claude", "skills", "image-see", "scripts", "vision_mcp_server.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := detectVisionReaderScript()
	if err != nil {
		t.Fatalf("detectVisionReaderScript error: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(script) {
		t.Errorf("got %q, want %q", got, script)
	}
}

func TestDetectVisionReaderScriptMissing(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	if _, err := detectVisionReaderScript(); err == nil {
		t.Fatalf("脚本不存在时应报错")
	}
}

func TestReadWriteCursorMCPServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	servers := map[string]any{
		"vision-reader": map[string]any{
			"command": "python",
			"args":    []any{"C:/x/vision_mcp_server.py"},
			"env":     map[string]any{"IMAGE_SEE_MODEL": "gpt-5.6-luna"},
		},
	}
	if err := writeCursorMCPServers(path, servers); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readCursorMCPServers(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, ok := got["vision-reader"]; !ok {
		t.Fatalf("vision-reader 缺失: %#v", got)
	}
	// 文件不存在 → 空映射
	empty, err := readCursorMCPServers(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("缺失文件应返回空映射: %#v", empty)
	}
}

func TestEnableReaderMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	script := filepath.Join(home, ".claude", "skills", "image-see", "scripts", "vision_mcp_server.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &ProxyService{}
	const url = "https://gw.example.com/v1"
	const key = "sk-secret"
	const model = "gpt-5.6-luna"

	result, err := svc.EnableReaderMCP(url, key, model)
	if err != nil {
		t.Fatalf("EnableReaderMCP error: %v", err)
	}
	if result.Identifier != readerMCPIdentifier {
		t.Errorf("Identifier = %q, want %q", result.Identifier, readerMCPIdentifier)
	}
	if !result.WasAdded {
		t.Errorf("首次启用 WasAdded 应为 true")
	}

	mcpPath := filepath.Join(home, ".cursor", "mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("mcp.json 应存在: %v", err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("mcp.json 解析失败: %v", err)
	}
	entry := doc.MCPServers[readerMCPIdentifier]
	if entry == nil {
		t.Fatalf("mcp.json 缺少 %s: %s", readerMCPIdentifier, data)
	}
	if entry["command"] != "python" {
		t.Errorf("command = %v", entry["command"])
	}
	env, _ := entry["env"].(map[string]any)
	if env["IMAGE_SEE_BASE_URL"] != url || env["IMAGE_SEE_API_KEY"] != key || env["IMAGE_SEE_MODEL"] != model {
		t.Errorf("env 异常: %#v", env)
	}

	// 重复启用 → 更新而非新增；model 空回退默认
	result2, err := svc.EnableReaderMCP("https://gw2.example.com/v1", "sk2", "")
	if err != nil {
		t.Fatalf("EnableReaderMCP(second) error: %v", err)
	}
	if result2.WasAdded {
		t.Errorf("重复启用 WasAdded 应为 false")
	}
	data2, _ := os.ReadFile(mcpPath)
	var doc2 struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(data2, &doc2)
	env2, _ := doc2.MCPServers[readerMCPIdentifier]["env"].(map[string]any)
	if env2["IMAGE_SEE_BASE_URL"] != "https://gw2.example.com/v1" {
		t.Errorf("重复启用应更新 url: %#v", env2)
	}
	if env2["IMAGE_SEE_MODEL"] != "gpt-5.6-luna" {
		t.Errorf("model 空应回退默认: %#v", env2)
	}
}

func TestEnableReaderMCPMissingScript(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	svc := &ProxyService{}
	if _, err := svc.EnableReaderMCP("https://gw/v1", "", ""); err == nil {
		t.Fatalf("脚本缺失时应报错")
	}
}
