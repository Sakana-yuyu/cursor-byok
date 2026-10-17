// apply_tool 把 ext_tool 的抽取产物 proto/from_extensions/*.proto 转成编译输入 proto/*.proto。
//
// 它做四件事，全部跨平台（早先 build/Taskfile.yml 里的 cp + perl 只能在 macOS/Linux 跑，
// 而且那条 sync:proto 任务全仓库零引用，导致抽取产物和编译输入长期漂移）：
//
//  1. 拷贝 agent_v1.proto / aiserver_v1.proto。
//  2. 把 Cursor 自带的 go_package 改写成本仓库的模块路径。
//  3. 按一张显式修复表还原 ext_tool 的类型解析回退。
//  4. 补入经过审计的旧会话与仓库状态类型，以及本地模型元数据字段，并验证完整依赖。
//
// 关于第 3 点：旧版抽取产物曾把部分 length-delimited 消息字段退化成 bytes。
// 修复表仅兼容这些已经审计的旧声明；新静态紧凑描述应直接提供正确消息类型。
// 两种输入都必须精确匹配字段声明和嵌套目标路径，其他变化直接失败，防止静默降级。
package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
)

//go:embed compatibility/*.proto.inc
var compatibilitySources embed.FS

// compatibilityTypes 限定兼容类型的精确范围，不从旧文件自动合并其他协议。
var compatibilityTypes = map[string][]string{
	"agent_v1.proto":    {"AgentConversationTurn", "AgentConversationTurnStructure", "ConversationState", "ConversationSummary", "ConversationTurn", "ConversationTurnStructure", "ConversationSummaryArchive", "ShellConversationTurn", "ShellConversationTurnStructure", "ShellOutput"},
	"aiserver_v1.proto": {"BatchRepositoryStatusRequest", "BatchRepositoryStatusResponse", "RepositoryStatusRequest", "RepositoryStatusResponse"},
}

type compatibilityField struct {
	name, scalar, declaration string
	no                        int32
}

var compatibilityModelFields = []compatibilityField{
	{"supports_auto_context", "TYPE_BOOL", "optional bool supports_auto_context = 11;", 11},
	{"auto_context_max_tokens", "TYPE_INT32", "optional int32 auto_context_max_tokens = 12;", 12},
	{"context_token_limit", "TYPE_INT32", "optional int32 context_token_limit = 15;", 15},
	{"context_token_limit_for_max_mode", "TYPE_INT32", "optional int32 context_token_limit_for_max_mode = 16;", 16},
}

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
	// Message 是字段所属 message 的文件内路径；嵌套类型使用点分隔。
	Message string
	// Degraded 是抽取产物中退化后的字段声明（按去空白后的整行精确匹配）。
	Degraded string
	// Restored 是还原后的字段声明。
	Restored string
	// TargetMessage 是还原后引用的 message 路径，必须精确存在于同一文件。
	TargetMessage string
}

// extractionRepairs 保留旧版已审计退化声明的兼容路径，当前紧凑描述产物全部应命中已正确分支。
// 旧 aiserver 产物还曾缺少 CursorPositionAction、ReadFileNotFound、SelectedFile；
// 当前产物已直接恢复这三个目标及真实字段类型，因此不追加旧版不存在的修复目标。
var extractionRepairs = []repair{
	{"agent_v1.proto", "ComputerUseAction", "bytes cursor_position = 11;", "CursorPositionAction cursor_position = 11;", "CursorPositionAction"},
	{"agent_v1.proto", "ReadResult", "bytes file_not_found = 4;", "ReadFileNotFound file_not_found = 4;", "ReadFileNotFound"},
	{"agent_v1.proto", "SelectedContext", "repeated bytes files = 4;", "repeated SelectedFile files = 4;", "SelectedFile"},

	{"aiserver_v1.proto", "ComposerCapabilityRequest.DiffReviewCapability", "repeated bytes diffs = 2;", "repeated SimpleFileDiff diffs = 2;", "ComposerCapabilityRequest.DiffReviewCapability.SimpleFileDiff"},
	{"aiserver_v1.proto", "ContextItem.CmdKQueryHistory", "bytes selection = 3;", "ContextItem.CmdKSelection selection = 3;", "ContextItem.CmdKSelection"},
	{"aiserver_v1.proto", "ContextItem", "bytes cmd_k_selection = 4;", "CmdKSelection cmd_k_selection = 4;", "ContextItem.CmdKSelection"},
	{"aiserver_v1.proto", "FSGetMultiFileContentsResponse", "repeated bytes files = 1;", "repeated FileRetrieved files = 1;", "FileRetrieved"},
	{"aiserver_v1.proto", "RescrapeDocsRequestV2", "bytes new_doc_req = 1;", "NewDocumentationRequest new_doc_req = 1;", "NewDocumentationRequest"},
	{"aiserver_v1.proto", "ShellArgs", "optional bytes output_notification = 18;", "optional ShellOutputNotificationConfig output_notification = 18;", "ShellOutputNotificationConfig"},
	{"aiserver_v1.proto", "StreamCmdKRequest.BranchDiff", "repeated bytes file_diffs = 1;", "repeated FileDiff file_diffs = 1;", "StreamCmdKRequest.BranchDiff.FileDiff"},
	{"aiserver_v1.proto", "SubmitLogsRequest", "repeated bytes logs = 1;", "repeated ClientLogEntry logs = 1;", "ClientLogEntry"},
	{"aiserver_v1.proto", "UsageEventDetails", "bytes ai_review_accepted_comment = 5;", "AiReviewAcceptedComment ai_review_accepted_comment = 5;", "UsageEventDetails.AiReviewAcceptedComment"},
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
	type output struct{ source, target, text string }
	var outputs []output
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
		text, err = applyCompatibility(text, rewrite.File)
		if err != nil {
			fail("%s: compatibility: %v", rewrite.File, err)
		}
		outputs = append(outputs, output{source, target, text})
	}
	if err := os.MkdirAll(*to, 0o755); err != nil {
		fail("create %s: %v", *to, err)
	}
	for _, out := range outputs {
		if err := os.WriteFile(out.target, []byte(out.text), 0o644); err != nil {
			fail("write %s: %v", out.target, err)
		}
		fmt.Printf("applied %s -> %s\n", out.source, out.target)
	}
	fmt.Printf("extraction repairs: applied=%d already-correct=%d total=%d\n", applied, alreadyCorrect, len(extractionRepairs))
}

// applyRepair 在 message 块内把退化声明替换回真实类型；已经正确时返回 changed=false。
func applyRepair(text string, item repair) (string, bool, error) {
	if _, _, err := messageBlock(text, item.TargetMessage); err != nil {
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
	type block struct {
		path       string
		start, end int
	}
	var stack, candidates []block
	for _, match := range regexp.MustCompile(`(?m)^[ \t]*message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`).FindAllStringSubmatchIndex(text, -1) {
		start := match[0]
		for len(stack) > 0 && stack[len(stack)-1].end <= start {
			stack = stack[:len(stack)-1]
		}
		short := text[match[2]:match[3]]
		path := short
		if len(stack) > 0 {
			path = stack[len(stack)-1].path + "." + short
		}
		depth := 1
		end := match[1]
		for end < len(text) && depth > 0 {
			switch text[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end++
		}
		if depth != 0 {
			return 0, 0, fmt.Errorf("message %s is not closed", path)
		}
		item := block{path, start, end}
		stack = append(stack, item)
		if path == name || (!strings.Contains(name, ".") && short == name) {
			candidates = append(candidates, item)
		}
	}
	if !strings.Contains(name, ".") && len(candidates) > 1 {
		var top []block
		for _, item := range candidates {
			if item.path == name {
				top = append(top, item)
			}
		}
		if len(top) == 1 {
			candidates = top
		}
	}
	if len(candidates) != 1 {
		return 0, 0, fmt.Errorf("message %s has %d exact candidates", name, len(candidates))
	}
	return candidates[0].start, candidates[0].end, nil
}

// applyCompatibility 只补显式缺失顶层类型；完整解析确保兼容片段依赖仍然成立。
func applyCompatibility(text, file string) (string, error) {
	names, ok := compatibilityTypes[file]
	if !ok {
		return "", fmt.Errorf("未登记兼容文件: %s", file)
	}
	data, err := compatibilitySources.ReadFile("compatibility/" + file + ".inc")
	if err != nil {
		return "", err
	}
	// 先做未链接语法解析，顶层身份不依赖缩进，也不会把 ShellOutputNotification 当作 ShellOutput。
	parser := protoparse.Parser{Accessor: protoparse.FileContentsFromMap(map[string]string{file: text})}
	syntaxFiles, err := parser.ParseFilesButDoNotLink(file)
	if err != nil {
		return "", fmt.Errorf("兼容输入语法校验失败: %w", err)
	}
	topLevel := make(map[string]bool)
	for _, msg := range syntaxFiles[0].GetMessageType() {
		topLevel[msg.GetName()] = true
	}
	for _, name := range names {
		if topLevel[name] {
			continue
		}
		start, end, err := messageBlock(string(data), name)
		if err != nil {
			return "", err
		}
		text = strings.TrimRight(text, "\r\n") + "\n\n// 本仓库保留的旧协议兼容类型。\n" + string(data[start:end]) + "\n"
	}
	fd, err := parseCompatibilityInput(text, file)
	if err != nil {
		return "", err
	}
	if file == "agent_v1.proto" {
		var model *desc.MessageDescriptor
		for _, msg := range fd.GetMessageTypes() {
			if msg.GetName() == "ModelDetails" {
				model = msg
				break
			}
		}
		if model == nil {
			return "", fmt.Errorf("agent.v1.ModelDetails 缺失，无法验证本地模型元数据字段")
		}
		var missing []string
		for _, expected := range compatibilityModelFields {
			var byName, byNumber *desc.FieldDescriptor
			for _, field := range model.GetFields() {
				if field.GetName() == expected.name {
					byName = field
				}
				if field.GetNumber() == expected.no {
					byNumber = field
				}
			}
			if byName != nil || byNumber != nil {
				if byName == nil || byNumber != byName || byName.GetType().String() != expected.scalar || byName.IsRepeated() || !byName.AsFieldDescriptorProto().GetProto3Optional() {
					return "", fmt.Errorf("ModelDetails.%s 与兼容字段号 %d、类型或 optional 语义冲突", expected.name, expected.no)
				}
				continue
			}
			missing = append(missing, "  "+expected.declaration)
		}
		if len(missing) > 0 {
			_, end, err := messageBlock(text, "ModelDetails")
			if err != nil {
				return "", err
			}
			text = text[:end-1] + "  // 本地模型选择与模拟响应仍使用的元数据字段。\n" + strings.Join(missing, "\n") + "\n" + text[end-1:]
		}
	}
	if _, err := parseCompatibilityInput(text, file); err != nil {
		return "", err
	}
	return text, nil
}

func parseCompatibilityInput(text, file string) (*desc.FileDescriptor, error) {
	parser := protoparse.Parser{Accessor: protoparse.FileContentsFromMap(map[string]string{file: text}), LookupImport: desc.LoadFileDescriptor}
	files, err := parser.ParseFiles(file)
	if err != nil {
		return nil, fmt.Errorf("兼容协议依赖或字段校验失败: %w", err)
	}
	return files[0], nil
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
