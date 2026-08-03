package promptinject

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCommitMessageLanguage 验证 Git 提交文本本地化的语言字段读取：
// 未启用返回空、auto 返回前端解析的具体语言、具体语言原样返回、损坏配置按关闭处理。
func TestCommitMessageLanguage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompt-injection.json")

	manager := New()
	manager.SetPath(path)
	if got := manager.CommitMessageLanguage(); got != "" {
		t.Fatalf("未启用时 CommitMessageLanguage 应为空，得到 %q", got)
	}

	// auto：返回前端解析好的具体语言。
	if _, err := manager.Save(Config{CommitMessageEnabled: true, CommitMessageLanguage: "auto", CommitMessageLanguageResolved: "zh-cn"}); err != nil {
		t.Fatalf("保存配置失败: %v", err)
	}
	reloaded := New()
	reloaded.SetPath(path)
	if got := reloaded.CommitMessageLanguage(); got != "zh-cn" {
		t.Fatalf("auto 应返回解析后的 zh-cn，得到 %q", got)
	}

	// 具体语言：原样返回。
	if _, err := manager.Save(Config{CommitMessageEnabled: true, CommitMessageLanguage: "en-US"}); err != nil {
		t.Fatalf("保存配置失败: %v", err)
	}
	reloaded = New()
	reloaded.SetPath(path)
	if got := reloaded.CommitMessageLanguage(); got != "en-us" {
		t.Fatalf("en-US 应规范化为 en-us，得到 %q", got)
	}

	// 未启用：即使有语言也不返回。
	if _, err := manager.Save(Config{CommitMessageEnabled: false, CommitMessageLanguage: "zh-CN"}); err != nil {
		t.Fatalf("保存配置失败: %v", err)
	}
	reloaded = New()
	reloaded.SetPath(path)
	if got := reloaded.CommitMessageLanguage(); got != "" {
		t.Fatalf("未启用时 CommitMessageLanguage 应为空，得到 %q", got)
	}

	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("写入损坏配置失败: %v", err)
	}
	broken := New()
	broken.SetPath(path)
	if got := broken.CommitMessageLanguage(); got != "" {
		t.Fatalf("损坏配置下 CommitMessageLanguage 应为空，得到 %q", got)
	}
}

// TestSoftwareChineseEnabled 验证「软件使用中文化」开关可被独立读取（兼容旧字段）。
func TestSoftwareChineseEnabled(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompt-injection.json")

	manager := New()
	manager.SetPath(path)
	if manager.SoftwareChineseEnabled() {
		t.Fatal("默认配置下 SoftwareChineseEnabled 应为 false")
	}

	if _, err := manager.Save(Config{SoftwareChineseEnabled: true}); err != nil {
		t.Fatalf("保存配置失败: %v", err)
	}
	reloaded := New()
	reloaded.SetPath(path)
	if !reloaded.SoftwareChineseEnabled() {
		t.Fatal("开启 softwareChineseEnabled 后应返回 true")
	}
}