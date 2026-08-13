// apply_tool 把 ext_tool 的抽取产物 proto/from_extensions/*.proto 转成编译输入 proto/*.proto。
//
// 它做三件事，全部跨平台（早先 build/Taskfile.yml 里的 cp + perl 只能在 macOS/Linux 跑，
// 而且那条 sync:proto 任务全仓库零引用，导致抽取产物和编译输入长期漂移）：
//
//  1. 拷贝 agent_v1.proto / aiserver_v1.proto。
//  2. 把 Cursor 自带的 go_package 改写成本仓库的模块路径。
//  3. 按一张显式修复表还原 ext_tool 的类型解析回退。
//
// 关于第 3 点：ext_tool 从 Cursor 扩展 bundle 还原 proto 时，遇到无法确定具体类型的
// length-delimited 字段会退化成 bytes。wire 层两者等价，但 Go 侧会丢掉类型、被迫手工
// Marshal/Unmarshal。修复表只收录「原编译 proto 里本来就是 message、新抽取产物退化成
// bytes、且目标 message 在新产物里依然存在」的字段；目标 message 消失时直接失败，
// 这样 Cursor 真的删掉某个类型时同步会响亮地报错，而不是静默降级。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// goPackageRewrite 描述一次 go_package 改写。
type goPackageRewrite struct {
	// File 是文件名，from/to 目录下同名。
	File string
	// From 是 Cursor 抽取产物里的 go_package 值。
	From string
	// To 是本仓库使用的 go_package 值。
	To string
}

var goPackageRewrites = []goPackageRewrite{
	{"agent_v1.proto", "react-admin/cursor-server/gen/agent/v1;agentv1", "cursor/gen/agentv1;agentv1"},
	{"aiserver_v1.proto", "react-admin/cursor-server/gen/aiserver/v1;aiserverv1", "cursor/gen/aiserverv1;aiserverv1"},
}

// repair 描述一次字段类型还原。
type repair struct {
	// File 是 proto 文件名。
	File string
	// Message 是字段所属 message 的短名；顶层与嵌套同名时取顶层。
	Message string
	// Degraded 是抽取产物中退化后的字段声明（按去空白后的整行精确匹配）。
	Degraded string
	// Restored 是还原后的字段声明。
	Restored string
	// TargetMessage 是还原后引用的 message 短名，必须存在于同一文件。
	TargetMessage string
}

// extractionRepairs 是当前抽取产物需要的全部类型还原。
// 每一条都对应一次 `buf breaking` 报出的 "changed type from message to bytes"。
//
// 有三处 buf 报出的同类退化**故意**不在表里：aiserver_v1.proto 的
// ComputerUseAction.cursor_position、ReadResult.file_not_found、SelectedContext.files。
// 这三个字段的目标 message（CursorPositionAction / ReadFileNotFound / SelectedFile）在新版
// aiserver_v1 抽取产物里整个消失了，没有可以还原成的类型；它们在 agent_v1 里仍然存在，
// 也已经在下表前三条里还原。本仓库 gen/ 之外没有任何代码引用这三个 aiserverv1 符号
// （本地模式全部走 agentv1），所以留作 bytes 无实际影响。
var extractionRepairs = []repair{
	{"agent_v1.proto", "ComputerUseAction", "bytes cursor_position = 11;", "CursorPositionAction cursor_position = 11;", "CursorPositionAction"},
	{"agent_v1.proto", "ReadResult", "bytes file_not_found = 4;", "ReadFileNotFound file_not_found = 4;", "ReadFileNotFound"},
	{"agent_v1.proto", "SelectedContext", "repeated bytes files = 4;", "repeated SelectedFile files = 4;", "SelectedFile"},

	{"aiserver_v1.proto", "DiffReviewCapability", "repeated bytes diffs = 2;", "repeated SimpleFileDiff diffs = 2;", "SimpleFileDiff"},
	{"aiserver_v1.proto", "CmdKQueryHistory", "bytes selection = 3;", "ContextItem.CmdKSelection selection = 3;", "CmdKSelection"},
	{"aiserver_v1.proto", "ContextItem", "bytes cmd_k_selection = 4;", "CmdKSelection cmd_k_selection = 4;", "CmdKSelection"},
	{"aiserver_v1.proto", "FSGetMultiFileContentsResponse", "repeated bytes files = 1;", "repeated FileRetrieved files = 1;", "FileRetrieved"},
	{"aiserver_v1.proto", "RescrapeDocsRequestV2", "bytes new_doc_req = 1;", "NewDocumentationRequest new_doc_req = 1;", "NewDocumentationRequest"},
	{"aiserver_v1.proto", "ShellArgs", "optional bytes output_notification = 18;", "optional ShellOutputNotificationConfig output_notification = 18;", "ShellOutputNotificationConfig"},
	{"aiserver_v1.proto", "BranchDiff", "repeated bytes file_diffs = 1;", "repeated FileDiff file_diffs = 1;", "FileDiff"},
	{"aiserver_v1.proto", "SubmitLogsRequest", "repeated bytes logs = 1;", "repeated ClientLogEntry logs = 1;", "ClientLogEntry"},
	{"aiserver_v1.proto", "UsageEventDetails", "bytes ai_review_accepted_comment = 5;", "AiReviewAcceptedComment ai_review_accepted_comment = 5;", "AiReviewAcceptedComment"},
}

func main() {
	from := flag.String("from", filepath.Join("proto", "from_extensions"), "ext_tool 抽取产物目录")
	to := flag.String("to", "proto", "protoc 编译输入目录")
	flag.Parse()

	repairsByFile := map[string][]repair{}
	for _, item := range extractionRepairs {
		repairsByFile[item.File] = append(repairsByFile[item.File], item)
	}

	applied, alreadyCorrect := 0, 0
	for _, rewrite := range goPackageRewrites {
		source := filepath.Join(*from, rewrite.File)
		target := filepath.Join(*to, rewrite.File)

		body, err := os.ReadFile(source)
		if err != nil {
			fail("read %s: %v", source, err)
		}
		text := string(body)

		fromOption := fmt.Sprintf("option go_package = %q;", rewrite.From)
		toOption := fmt.Sprintf("option go_package = %q;", rewrite.To)
		switch {
		case strings.Contains(text, fromOption):
			text = strings.Replace(text, fromOption, toOption, 1)
		case strings.Contains(text, toOption):
		default:
			fail("%s: neither the upstream nor the local go_package option was found", source)
		}

		for _, item := range repairsByFile[rewrite.File] {
			next, changed, err := applyRepair(text, item)
			if err != nil {
				fail("%s: %s.%s: %v", rewrite.File, item.Message, fieldName(item.Restored), err)
			}
			text = next
			if changed {
				applied++
			} else {
				alreadyCorrect++
			}
		}

		if err := os.WriteFile(target, []byte(text), 0o644); err != nil {
			fail("write %s: %v", target, err)
		}
		fmt.Printf("applied %s -> %s\n", source, target)
	}
	fmt.Printf("extraction repairs: applied=%d already-correct=%d total=%d\n", applied, alreadyCorrect, len(extractionRepairs))
}

// applyRepair 在 message 块内把退化声明替换回真实类型；已经正确时返回 changed=false。
func applyRepair(text string, item repair) (string, bool, error) {
	if !regexp.MustCompile(`(?m)^\s*message ` + regexp.QuoteMeta(item.TargetMessage) + ` \{`).MatchString(text) {
		return "", false, fmt.Errorf("target message %q no longer exists; audit this field by hand", item.TargetMessage)
	}
	start, end, err := messageBlock(text, item.Message)
	if err != nil {
		return "", false, err
	}
	block := text[start:end]
	if declCount(block, item.Restored) == 1 {
		return text, false, nil
	}
	if count := declCount(block, item.Degraded); count != 1 {
		return "", false, fmt.Errorf("expected exactly one %q inside the message, found %d", item.Degraded, count)
	}
	return text[:start] + replaceDecl(block, item.Degraded, item.Restored) + text[end:], true, nil
}

// messageBlock 返回 message 块的字节区间。短名在文件里唯一时直接采用；
// 顶层与嵌套同名时（例如 ContextItem 与 DeepSearchSubagentReturnValue.ContextItem）
// 取顶层那一个，其余歧义一律报错。
func messageBlock(text string, name string) (int, int, error) {
	matches := regexp.MustCompile(`(?m)^[ \t]*message `+regexp.QuoteMeta(name)+` \{`).FindAllStringIndex(text, -1)
	if len(matches) > 1 {
		topLevel := regexp.MustCompile(`(?m)^message `+regexp.QuoteMeta(name)+` \{`).FindAllStringIndex(text, -1)
		if len(topLevel) != 1 {
			return 0, 0, fmt.Errorf("`message %s {` is ambiguous: %d matches, %d of them top-level", name, len(matches), len(topLevel))
		}
		matches = topLevel
	}
	if len(matches) != 1 {
		return 0, 0, fmt.Errorf("expected exactly one `message %s {`, found %d", name, len(matches))
	}
	start := matches[0][0]
	depth := 0
	for index := matches[0][1] - 1; index < len(text); index++ {
		switch text[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return start, index + 1, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("message %s is not closed", name)
}

// declCount 统计块内与给定字段声明完全相同的行数（忽略缩进）。
func declCount(block string, decl string) int {
	count := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(strings.TrimSuffix(line, "\r")) == decl {
			count++
		}
	}
	return count
}

// replaceDecl 保留原缩进与行尾替换字段声明。
func replaceDecl(block string, from string, to string) string {
	lines := strings.Split(block, "\n")
	for index, line := range lines {
		content := strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(content) != from {
			continue
		}
		lineBreak := ""
		if content != line {
			lineBreak = "\r"
		}
		indent := content[:len(content)-len(strings.TrimLeft(content, " \t"))]
		lines[index] = indent + to + lineBreak
	}
	return strings.Join(lines, "\n")
}

func fieldName(decl string) string {
	parts := strings.Fields(decl)
	for index, part := range parts {
		if part == "=" && index > 0 {
			return parts[index-1]
		}
	}
	return decl
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "proto apply failed: "+format+"\n", args...)
	os.Exit(1)
}
