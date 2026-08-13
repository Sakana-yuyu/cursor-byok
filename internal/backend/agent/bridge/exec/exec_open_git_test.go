package execbridge

import (
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestOpenGitDiffBuildsRequestWithClientSideCap(t *testing.T) {
	bridge := NewBridge()
	serverMessage, pending, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecore.ToolInvocation{
		CallID:   "call-1",
		ToolName: "GitDiff",
		ArgsJSON: []byte(`{"base_ref":"main","merge_base":true,"target_paths":["a.go"," "],"output_format":"name_status_and_numstat","unified_context_lines":7}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GitDiff): %v", err)
	}
	if pending.ExecKind != "git_diff" {
		t.Fatalf("exec kind = %q", pending.ExecKind)
	}
	request := serverMessage.GetExecServerMessage().GetGitDiffRequest()
	if request == nil {
		t.Fatal("git diff request arm is not selected")
	}
	if request.GetCwd() != "/ws" {
		t.Errorf("cwd = %q, want the workspace hint", request.GetCwd())
	}
	if request.GetBaseRef() != "main" || !request.GetMergeBase() {
		t.Errorf("base_ref = %q merge_base = %t", request.GetBaseRef(), request.GetMergeBase())
	}
	if got := request.GetTargetPaths(); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("target_paths = %#v, want blank entries dropped", got)
	}
	if request.GetOutputFormat() != agentv1.GetDiffRequest_OUTPUT_FORMAT_NAME_STATUS_AND_NUMSTAT {
		t.Errorf("output_format = %v", request.GetOutputFormat())
	}
	if request.GetUnifiedContextLines() != 7 {
		t.Errorf("unified_context_lines = %d", request.GetUnifiedContextLines())
	}
	if request.GetMaxResponseBytes() != gitDiffClientResponseLimit {
		t.Errorf("max_response_bytes = %d, want %d", request.GetMaxResponseBytes(), gitDiffClientResponseLimit)
	}
}

// TestOpenGitDiffRequestsChangeEvidenceThatCannotBeSilentlyDropped 锁定三个默认值：
// 未跟踪文件、空白改动和 head/dirty 元数据。它们缺席时 diff 会「看起来是空的」，
// 而模型无从区分「真的没有改动」和「这类改动被请求参数排除了」。
func TestOpenGitDiffRequestsChangeEvidenceThatCannotBeSilentlyDropped(t *testing.T) {
	bridge := NewBridge()
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecore.ToolInvocation{
		CallID: "call-evidence", ToolName: "GitDiff", ArgsJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GitDiff): %v", err)
	}
	request := serverMessage.GetExecServerMessage().GetGitDiffRequest()
	if request.GetMaxUntrackedFiles() != gitDiffMaxUntrackedFiles {
		t.Errorf("max_untracked_files = %d, want %d (new files must not vanish from the diff)", request.GetMaxUntrackedFiles(), gitDiffMaxUntrackedFiles)
	}
	if !request.GetIncludeSpaceChanges() {
		t.Error("include_space_changes = false: whitespace-only edits would be dropped without any notice")
	}
	if !request.GetReturnHeadSha() {
		t.Error("return_head_sha = false: head_sha and has_uncommitted_changes would always be absent")
	}
	// patch id 需要客户端额外跑一次 git patch-id，模型侧没有用途。
	if request.GetComputePatchId() {
		t.Error("compute_patch_id = true, want false")
	}
	// merge_base 默认必须关：客户端只有在模型显式要求时才该去解析 merge base。
	if request.GetMergeBase() {
		t.Error("merge_base = true by default, want false")
	}
	// 上下文行数交给模型；不设默认值，客户端才会省略 -U 走 git 默认。
	if request.UnifiedContextLines != nil {
		t.Errorf("unified_context_lines = %d, want unset", request.GetUnifiedContextLines())
	}
}

// TestOpenGitDiffResponseCapMatchesObservedClientBehavior 锁定 max_response_bytes 的量级。
// 客户端在超限时抛错而不是截断，所以这个值是「多大算失败」而不是「截断到多大」；
// 模型侧真正的截断层是 gitDiffReplayContentLimit。
func TestOpenGitDiffResponseCapMatchesObservedClientBehavior(t *testing.T) {
	if gitDiffClientResponseLimit < 30*1024*replayKiB {
		t.Fatalf("gitDiffClientResponseLimit = %d, want at least 30 MiB", gitDiffClientResponseLimit)
	}
}

func TestOpenGitDiffDefaultsToFileDiffs(t *testing.T) {
	bridge := NewBridge()
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecore.ToolInvocation{
		CallID: "call-2", ToolName: "GitDiff", ArgsJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GitDiff): %v", err)
	}
	request := serverMessage.GetExecServerMessage().GetGitDiffRequest()
	if request.GetOutputFormat() != agentv1.GetDiffRequest_OUTPUT_FORMAT_FILE_DIFFS {
		t.Errorf("output_format = %v, want file diffs", request.GetOutputFormat())
	}
}

func TestSummarizeGitDiffResponseReportsCounts(t *testing.T) {
	headSHA := "abc123"
	uncommitted := true
	summary := summarizeGitDiffResponse(&agentv1.GetDiffResponse{
		HeadSha:               &headSHA,
		HasUncommittedChanges: &uncommitted,
		Diff: &agentv1.GitDiff{Diffs: []*agentv1.FileDiff{
			{From: "old.go", To: "new.go", Added: 3, Removed: 1, Chunks: []*agentv1.FileDiff_Chunk{
				{Content: "@@ -1,2 +1,4 @@", Lines: []string{"+added", "-removed"}},
			}},
		}},
	})
	for _, want := range []string{"files=1 +3 -1", "head_sha=abc123", "has_uncommitted_changes=true", "new.go +3 -1", "renamed from old.go", "@@ -1,2 +1,4 @@", "+added"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary)
		}
	}
}

func TestSummarizeGitDiffResponseEmpty(t *testing.T) {
	if got := summarizeGitDiffResponse(&agentv1.GetDiffResponse{}); got != "git diff empty" {
		t.Errorf("summary = %q", got)
	}
	if got := summarizeGitDiffResponse(nil); got != "git diff result missing" {
		t.Errorf("summary = %q", got)
	}
}

func TestSummarizeGitDiffResponseTruncatesFileCountAndTotalBytes(t *testing.T) {
	files := make([]*agentv1.FileDiff, 0, gitDiffReplayFileCount+25)
	for index := 0; index < gitDiffReplayFileCount+25; index++ {
		files = append(files, &agentv1.FileDiff{
			To: strings.Repeat("d", 40) + "/file.go", Added: 1,
			Chunks: []*agentv1.FileDiff_Chunk{{Content: "@@", Lines: []string{strings.Repeat("+x", 4096)}}},
		})
	}
	summary := summarizeGitDiffResponse(&agentv1.GetDiffResponse{Diff: &agentv1.GitDiff{Diffs: files}})
	if len(summary) > gitDiffReplayContentLimit {
		t.Fatalf("summary is %d bytes, want at most %d", len(summary), gitDiffReplayContentLimit)
	}
	if !strings.Contains(summary, "target_paths") {
		t.Errorf("truncated summary must tell the model how to narrow the query:\n%s", summary[len(summary)-400:])
	}
}

func TestSummarizeGitDiffResponseTruncatesSingleHugeFile(t *testing.T) {
	// One enormous file must not be able to consume the whole budget; the per-file
	// cap keeps room for the remaining entries.
	huge := &agentv1.FileDiff{To: "huge.go", Added: 1, Chunks: []*agentv1.FileDiff_Chunk{
		{Content: "@@", Lines: []string{strings.Repeat("+y", gitDiffReplayContentLimit)}},
	}}
	small := &agentv1.FileDiff{To: "small.go", Added: 1, Chunks: []*agentv1.FileDiff_Chunk{{Content: "@@", Lines: []string{"+z"}}}}
	summary := summarizeGitDiffResponse(&agentv1.GetDiffResponse{Diff: &agentv1.GitDiff{Diffs: []*agentv1.FileDiff{huge, small}}})
	if !strings.Contains(summary, "small.go +1 -0") {
		t.Errorf("the per-file cap must leave room for later files:\n%s", summary)
	}
	if !strings.Contains(summary, "[truncated: GitDiff file huge.go") {
		t.Errorf("the huge file must carry an explicit truncation notice:\n%s", summary[:600])
	}
}

func TestApplyGitDiffResultIsTerminalWithoutToolCall(t *testing.T) {
	bridge := NewBridge()
	_, pending, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecore.ToolInvocation{
		CallID: "call-3", ToolName: "GitDiff", ArgsJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GitDiff): %v", err)
	}
	applied, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id: pending.MessageID, ExecId: pending.ExecID,
		Message: &agentv1.ExecClientMessage_GitDiffResponse{GitDiffResponse: &agentv1.GetDiffResponse{}},
	}, pending)
	if err != nil {
		t.Fatalf("ApplyExecClientMessage(GitDiff): %v", err)
	}
	if !applied.IsTerminal {
		t.Error("git diff result must be terminal")
	}
	// ToolCall.oneof has no git diff arm, so the forwarder falls back to the
	// payload-only completion path.
	if applied.ToolCall != nil {
		t.Errorf("git diff must not synthesize a ToolCall: %#v", applied.ToolCall)
	}
	if strings.TrimSpace(applied.ToolResultPayload) == "" {
		t.Error("git diff must produce a tool result payload")
	}
}
