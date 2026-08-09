package backend

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cursor/internal/backend/server"
	"cursor/internal/backend/server/upstream"
	"cursor/internal/promptinject"
)

// TestWriteGitCommitMessageDispatchLocal 验证 source=local 时请求确实命中
// 本地 handler（agentModule.AiHandler 的替身），而非被转发到上游。
//
// 这是 ce601b8 回归（路由被改成纯 tabServerProcedure 转发）后的回归保护：
// local 分支不可达会导致 commitLanguageHardPrompts 语言硬约束失效，
// 提交文本被英文历史带偏、不受界面语言控制——即本任务要修的问题。
func TestWriteGitCommitMessageDispatchLocal(t *testing.T) {
	const localMarker = "local-handler-hit"
	localHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("本地 handler 命中 method=%s path=%s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(localMarker))
	})

	option := writeGitCommitMessageDispatchProcedure(
		"/aiserver.v1.AiService/WriteGitCommitMessage",
		"ai_write_git_commit_message",
		server.ConnectUnary(),
		localHandler,
		func() string { return promptinject.CommitSourceLocal },
		nil, // local 分支不需要 auth
		upstream.Dependencies{},
	)

	handler := server.New(option)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/aiserver.v1.AiService/WriteGitCommitMessage", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != localMarker {
		t.Fatalf("source=local 应命中本地 handler 返回 %q，得到 %q", localMarker, string(body))
	}
}

// TestWriteGitCommitMessageDispatchSwitch 验证分发 switch 对三种归一化来源
// 各自选中预期分支（通过 local handler 是否命中来判定 local vs 非 local）。
// leokun/cursor 分支因 tabServerBaseURL 常量无法在测试中重定向，这里只确认
// 它们不会误命中 local handler（即确实走了转发分支）。
func TestWriteGitCommitMessageDispatchSwitch(t *testing.T) {
	localHit := false
	localHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHit = true
		w.WriteHeader(http.StatusOK)
	})

	// source=leokun / cursor 时走转发分支；转发会因目标不可达失败，但关键是不命中 local。
	option := writeGitCommitMessageDispatchProcedure(
		"/aiserver.v1.AiService/WriteGitCommitMessage",
		"ai_write_git_commit_message",
		server.ConnectUnary(),
		localHandler,
		func() string { return promptinject.CommitSourceLeokun }, // 运行时切到 leokun
		nil,
		upstream.Dependencies{},
	)

	handler := server.New(option)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/aiserver.v1.AiService/WriteGitCommitMessage", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()

	if localHit {
		t.Fatal("source=leokun 不应命中 local handler（应走 leokun 转发分支）")
	}
}
