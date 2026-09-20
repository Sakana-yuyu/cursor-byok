package interaction

import (
	"testing"

	runtimecore "cursor/internal/backend/agent/core"
)

// TestWebSearchAcceptsQueryAsSearchTermAlias 锁定移植自上游 fc1e96d6 的别名兼容：
// Claude 系模型常按 Claude Code 习惯发送 query 而非 search_term，
// 交互查询构造必须接受别名，而不是报 `web search search_term is required`。
func TestWebSearchAcceptsQueryAsSearchTermAlias(t *testing.T) {
	for _, argsJSON := range []string{
		`{"search_term":"lmarena leaderboard"}`,
		`{"query":"lmarena leaderboard"}`,
	} {
		bridge := NewBridge()
		serverMessage, _, err := bridge.OpenQuery(runtimecore.ToolInvocation{
			CallID:   "call-1",
			ToolName: "WebSearch",
			ArgsJSON: []byte(argsJSON),
		})
		if err != nil {
			t.Fatalf("OpenQuery(WebSearch, %s) error = %v", argsJSON, err)
		}
		args := serverMessage.GetInteractionQuery().GetWebSearchRequestQuery().GetArgs()
		if args == nil {
			t.Fatalf("web search args arm is not selected for %s", argsJSON)
		}
		if args.GetSearchTerm() != "lmarena leaderboard" {
			t.Fatalf("argsJSON %s search_term = %q, want lmarena leaderboard", argsJSON, args.GetSearchTerm())
		}
	}
}
