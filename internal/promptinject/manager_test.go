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

// TestCommitMessageSource 验证 Git 提交文本生成来源字段：
// 默认/空/未知值回退 local；leokun、cursor 原样（小写）返回；来源独立于语言开关。
func TestCommitMessageSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompt-injection.json")

	cases := []struct {
		name  string
		store string
		want  string
	}{
		{name: "默认空值回退 local", store: "", want: CommitSourceLocal},
		{name: "未知值回退 local", store: "fr-fr", want: CommitSourceLocal},
		{name: "auto 回退 local", store: "auto", want: CommitSourceLocal},
		{name: "leokun 保留", store: "leokun", want: CommitSourceLeokun},
		{name: "cursor 保留", store: "cursor", want: CommitSourceCursor},
		{name: "大写 LEOKUN 归一化为 leokun", store: "LEOKUN", want: CommitSourceLeokun},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			manager := New()
			manager.SetPath(path)
			if _, err := manager.Save(Config{CommitMessageSource: tc.store}); err != nil {
				t.Fatalf("保存配置失败: %v", err)
			}
			reloaded := New()
			reloaded.SetPath(path)
			if got := reloaded.CommitMessageSource(); got != tc.want {
				t.Fatalf("CommitMessageSource(store=%q) 期望 %q，得到 %q", tc.store, tc.want, got)
			}
		})
	}

	// 损坏配置回退 local。
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("写入损坏配置失败: %v", err)
	}
	broken := New()
	broken.SetPath(path)
	if got := broken.CommitMessageSource(); got != CommitSourceLocal {
		t.Fatalf("损坏配置下 CommitMessageSource 应回退 local，得到 %q", got)
	}
}

// TestNormalizeCommitMessageSource 验证导出的归一化函数纯函数行为。
func TestNormalizeCommitMessageSource(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", CommitSourceLocal},
		{"local", CommitSourceLocal},
		{"LOCAL", CommitSourceLocal},
		{"leokun", CommitSourceLeokun},
		{"Leokun", CommitSourceLeokun},
		{"cursor", CommitSourceCursor},
		{"  cursor  ", CommitSourceCursor},
		{"unknown", CommitSourceLocal},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := NormalizeCommitMessageSource(tc.in); got != tc.want {
				t.Fatalf("NormalizeCommitMessageSource(%q) 期望 %q，得到 %q", tc.in, tc.want, got)
			}
		})
	}
}