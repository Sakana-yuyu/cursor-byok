// exec_open_git.go 承载 Cursor SCM 执行桥：GitDiff 请求构造与 GetDiffResponse 摘要。
//
// 本地模式此前只能靠 Shell 跑 `git diff` 再解析文本；客户端其实已经注册了
// gitDiffRequest/gitDiffResponse handler，能直接回结构化 diff（每文件增删行数 + hunk）。
package execbridge

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

const (
	// gitDiffClientResponseLimit 是发给客户端的 max_response_bytes。这不是截断阈值：
	// 客户端在序列化后发现超限时直接抛 GetDiffResponseTooLarge，抛错路径不保证回包。
	// 把它压小只会把「diff 很大」变成「这次调用彻底没结果」，所以取一个足够宽的上限，
	// 真正的截断交给模型侧的 gitDiffReplayContentLimit。
	gitDiffClientResponseLimit = 30 * 1024 * replayKiB
	// gitDiffMaxUntrackedFiles 是请求里携带的未跟踪文件上限。客户端只有在这个值 > 0
	// 时才会把未跟踪文件并进 diff；留 0 会让「新建的文件」在 diff 里完全消失，
	// 而模型无法把这种缺席和「真的没有改动」区分开。
	gitDiffMaxUntrackedFiles = 100
	// gitDiffReplayContentLimit 是回放给模型的总字节上限。与 Grep 对齐：diff 和搜索一样
	// 是「广度优先」的结果，模型看不全时应该用 target_paths 缩小范围再查，而不是我们塞更多。
	gitDiffReplayContentLimit = 32 * replayKiB
	// gitDiffReplayFileLimit 是单个文件 hunk 文本的上限，防止一个巨型文件挤掉其余全部文件。
	gitDiffReplayFileLimit = 8 * replayKiB
	// gitDiffReplayFileCount 是列出的文件数上限。
	gitDiffReplayFileCount = 200
)

// gitDiffArgs 是 GitDiff 工具的模型侧参数。
type gitDiffArgs struct {
	BaseRef             string   `json:"base_ref,omitempty"`
	Ref                 string   `json:"ref,omitempty"`
	MergeBase           bool     `json:"merge_base,omitempty"`
	CommittedOnly       bool     `json:"committed_only,omitempty"`
	TargetPaths         []string `json:"target_paths,omitempty"`
	OutputFormat        string   `json:"output_format,omitempty"`
	UnifiedContextLines *int32   `json:"unified_context_lines,omitempty"`
	WorkingDirectory    string   `json:"working_directory,omitempty"`
}

// openGitDiff 构造 GitDiff 对应的执行桥请求。
func (bridge *Bridge) openGitDiff(openContext OpenExecContext, toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	input, err := decodeGitDiffArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode GitDiff args failed: %w", err)
	}
	cwd := strings.TrimSpace(input.WorkingDirectory)
	if cwd == "" {
		cwd = strings.TrimSpace(openContext.WorkspaceHint)
	}
	maxResponseBytes := int32(gitDiffClientResponseLimit)
	returnHeadSHA := true
	request := &agentv1.GetDiffRequest{
		Cwd:                 cwd,
		Ref:                 strings.TrimSpace(input.Ref),
		BaseRef:             strings.TrimSpace(input.BaseRef),
		MergeBase:           input.MergeBase,
		CommittedOnly:       input.CommittedOnly,
		TargetPaths:         trimmedStrings(input.TargetPaths),
		UnifiedContextLines: input.UnifiedContextLines,
		OutputFormat:        gitDiffOutputFormat(input.OutputFormat).Enum(),
		MaxResponseBytes:    &maxResponseBytes,
		MaxUntrackedFiles:   gitDiffMaxUntrackedFiles,
		// 空白改动默认会被客户端用 --ignore-space-change 丢掉，缩进重排这类真实改动
		// 就会静默消失；宁可多给，也不能让 diff 看起来是空的。
		IncludeSpaceChanges: true,
		// head_sha 与 has_uncommitted_changes 都挂在这个开关上，后者来自
		// `git status --porcelain=v1 --untracked-files=all`，是 diff 为空时唯一能
		// 独立佐证「工作区到底脏不脏」的信号。摘要已经会渲染它们。
		ReturnHeadSha:         &returnHeadSHA,
		SubmoduleRecurseDepth: 0,
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-git-diff-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_GitDiffRequest{
					GitDiffRequest: request,
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "git_diff",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

func decodeGitDiffArgs(raw []byte) (gitDiffArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return gitDiffArgs{}, err
	}
	decoded := gitDiffArgs{
		BaseRef:          readStringArg(args, "base_ref", "baseRef"),
		Ref:              readStringArg(args, "ref"),
		MergeBase:        readBoolArg(args, "merge_base", "mergeBase"),
		CommittedOnly:    readBoolArg(args, "committed_only", "committedOnly"),
		TargetPaths:      readStringSliceArg(args, "target_paths", "targetPaths"),
		OutputFormat:     readStringArg(args, "output_format", "outputFormat"),
		WorkingDirectory: readStringArg(args, "working_directory", "workingDirectory", "cwd"),
	}
	lines, found, err := runtimecore.ReadInt32Arg(args, "unified_context_lines", "unifiedContextLines")
	if err != nil {
		return gitDiffArgs{}, err
	}
	if found {
		decoded.UnifiedContextLines = &lines
	}
	return decoded, nil
}

// gitDiffOutputFormat 把模型给的字符串映射为协议枚举，未知值退回 file_diffs。
func gitDiffOutputFormat(name string) agentv1.GetDiffRequest_OutputFormat {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "name_status":
		return agentv1.GetDiffRequest_OUTPUT_FORMAT_NAME_STATUS
	case "name_status_and_numstat":
		return agentv1.GetDiffRequest_OUTPUT_FORMAT_NAME_STATUS_AND_NUMSTAT
	case "diffs_with_before_and_after":
		return agentv1.GetDiffRequest_OUTPUT_FORMAT_DIFFS_WITH_BEFORE_AND_AFTER
	default:
		return agentv1.GetDiffRequest_OUTPUT_FORMAT_FILE_DIFFS
	}
}

func trimmedStrings(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			trimmed = append(trimmed, text)
		}
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}

// summarizeGitDiffResponse 把结构化 diff 转成模型可读文本。
//
// 截断分三层：单文件 hunk 文本上限、文件条目数上限、整体字节上限。
// 三层提示都写在输出开头而不是结尾——整体截断砍掉的正是结尾，
// 提示放在末尾就会连同被截断的内容一起消失，模型只会看到半截 diff。
func summarizeGitDiffResponse(response *agentv1.GetDiffResponse) string {
	if response == nil {
		return "git diff result missing"
	}
	header := make([]string, 0, 4)
	if sha := strings.TrimSpace(response.GetHeadSha()); sha != "" {
		header = append(header, "head_sha="+sha)
	}
	if response.HasUncommittedChanges != nil {
		header = append(header, fmt.Sprintf("has_uncommitted_changes=%t", response.GetHasUncommittedChanges()))
	}
	if patchID := strings.TrimSpace(response.GetPatchId()); patchID != "" {
		header = append(header, "patch_id="+patchID)
	}

	files := response.GetDiff().GetDiffs()
	if len(files) == 0 {
		if len(header) > 0 {
			return "git diff empty (" + strings.Join(header, " ") + ")"
		}
		return "git diff empty"
	}

	addedTotal, removedTotal := 0, 0
	for _, file := range files {
		addedTotal += int(file.GetAdded())
		removedTotal += int(file.GetRemoved())
	}
	header = append([]string{fmt.Sprintf("git diff files=%d +%d -%d", len(files), addedTotal, removedTotal)}, header...)

	shown := files
	if len(shown) > gitDiffReplayFileCount {
		shown = shown[:gitDiffReplayFileCount]
	}
	var bodyBuilder strings.Builder
	for index, file := range shown {
		if index > 0 {
			bodyBuilder.WriteString("\n\n")
		}
		bodyBuilder.WriteString(summarizeGitFileDiff(file))
	}
	if submodules := summarizeGitSubmoduleDiffs(response.GetSubmoduleDiffs()); submodules != "" {
		bodyBuilder.WriteString("\n\n")
		bodyBuilder.WriteString(submodules)
	}
	body := bodyBuilder.String()

	notices := make([]string, 0, 2)
	if len(files) > len(shown) {
		notices = append(notices, fmt.Sprintf("[truncated: listed %d of %d files]", len(shown), len(files)))
	}
	const guidance = "[hint: scope the query with target_paths, or use output_format=\"name_status_and_numstat\" for an overview]"

	prefix := strings.Join(header, " ") + "\n"
	truncatedPrefix := prefix + strings.Join(append(notices, guidance), "\n") + "\n\n"
	budget := gitDiffReplayContentLimit - len(truncatedPrefix)
	if budget < replayKiB {
		budget = replayKiB
	}
	truncatedBody := truncateReplayText("GitDiff", body, budget)
	if len(notices) == 0 && truncatedBody == body {
		return prefix + "\n" + body
	}
	return truncatedPrefix + truncatedBody
}

func summarizeGitFileDiff(file *agentv1.FileDiff) string {
	if file == nil {
		return ""
	}
	path := strings.TrimSpace(file.GetTo())
	from := strings.TrimSpace(file.GetFrom())
	if path == "" {
		path = from
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s +%d -%d", path, file.GetAdded(), file.GetRemoved())
	if from != "" && from != path {
		fmt.Fprintf(&builder, " (renamed from %s)", from)
	}
	if file.GetIsGenerated() {
		builder.WriteString(" (generated)")
	}
	body := gitFileDiffBody(file)
	if body == "" {
		return builder.String()
	}
	builder.WriteString("\n")
	builder.WriteString(truncateReplayText("GitDiff file "+path, body, gitDiffReplayFileLimit))
	return builder.String()
}

func gitFileDiffBody(file *agentv1.FileDiff) string {
	chunks := file.GetChunks()
	if len(chunks) == 0 {
		return ""
	}
	var builder strings.Builder
	for index, chunk := range chunks {
		if chunk == nil {
			continue
		}
		if index > 0 {
			builder.WriteString("\n")
		}
		if content := strings.TrimRight(chunk.GetContent(), "\n"); content != "" {
			builder.WriteString(content)
			builder.WriteString("\n")
		} else {
			fmt.Fprintf(&builder, "@@ -%d,%d +%d,%d @@\n", chunk.GetOldStart(), chunk.GetOldLines(), chunk.GetNewStart(), chunk.GetNewLines())
		}
		for _, line := range chunk.GetLines() {
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

func summarizeGitSubmoduleDiffs(submodules []*agentv1.GetDiffResponse_SubmoduleDiff) string {
	if len(submodules) == 0 {
		return ""
	}
	entries := make([]string, 0, len(submodules))
	for _, submodule := range submodules {
		if submodule == nil {
			continue
		}
		state := fmt.Sprintf("files=%d", len(submodule.GetDiff().GetDiffs()))
		if submodule.GetErrored() {
			state = "errored"
		}
		entries = append(entries, fmt.Sprintf("%s (%s)", strings.TrimSpace(submodule.GetRelativePath()), state))
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Strings(entries)
	return "submodules: " + strings.Join(entries, ", ")
}
