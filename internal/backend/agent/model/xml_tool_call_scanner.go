// xml_tool_call_scanner.go 实现 in-band XML 工具协议的响应侧：对 OpenAI chat
// completions 的文本增量做流式扫描，把模型输出的 <tool_call> 文本块转换回与
// 原生 tool_calls 同构的 ToolLikeCompleted 事件（forwarder 无感知）。
//
// 行为约定（与 xml_tool_protocol.go 请求侧严格互逆）：
//   - 命中 <tool_call> 开标签后停止向客户端透传文本，累积至 </tool_call>；
//   - 块内 JSON 先严格 parse；失败用 repairOpenAIToolArgsEscapes 风格修复再试；
//     仍失败则整块降级为普通文本透传并记 warning（fail-open，不丢内容）；
//   - 模型输出中的 <tool_result>...</tool_result> 视为伪造结果，整体剥离并记
//     warning（防止模型自答自问假成功）；
//   - 流结束时未闭合的半截 <tool_call> 块按普通文本放行。
package modeladapter

import (
	"encoding/json"
	"fmt"
	"strings"

	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

type xmlScannerState int

const (
	xmlScannerStateText = iota // 普通文本透传状态
	xmlScannerStateCall        // 正在累积一个 <tool_call> 块体
	xmlScannerStateResult      // 正在剥离一个伪造的 <tool_result> 块
)

// xmlScannerEvent 是扫描器的单个输出：要么是一段可透传文本，要么是一个已完成
// 的工具调用。两者互斥。
type xmlScannerEvent struct {
	Text string
	Call *runtimecore.ToolInvocation
}

// xmlToolCallScanner 是跨 chunk 边界安全的增量扫描器。Feed 可被任意切分的
// delta 调用；Flush 在流结束时调用一次，放行所有残留缓冲。
type xmlToolCallScanner struct {
	modelCallID string
	// advertisedTools 是本次请求声明的工具名映射（provider 名 → 规范名）。
	// xml 模式下请求体不携带原生 tools，req.ToolAdmission 为空映射，因此
	// 直接从 req.Tools 构建这份映射用于名称解析与未声明工具拒绝。
	advertisedTools map[string]string

	state      xmlScannerState
	pending    string // 当前状态下的待判定文本
	block      string // stateCall 下已累积的块体
	resultHold int    // stateResult 下已剥离的字节数累计（用于告警）

	seq int
}

func newXMLToolCallScanner(modelCallID string, advertisedTools map[string]string) *xmlToolCallScanner {
	return &xmlToolCallScanner{modelCallID: modelCallID, advertisedTools: advertisedTools}
}

// Feed 输入一段增量文本，返回本次产生的透传文本/完成调用序列。
func (s *xmlToolCallScanner) Feed(delta string) []xmlScannerEvent {
	if delta == "" {
		return nil
	}
	var events []xmlScannerEvent
	s.pending += delta
	for {
		switch s.state {
		case xmlScannerStateCall:
			idx := strings.Index(s.pending, xmlToolCallCloseTag)
			if idx < 0 {
				s.block += s.pending
				s.pending = ""
				return events
			}
			s.block += s.pending[:idx]
			s.pending = s.pending[idx+len(xmlToolCallCloseTag):]
			s.state = xmlScannerStateText
			if event, ok := s.completeCall(s.block); ok {
				events = append(events, event)
			} else {
				// 解析失败：整块降级为普通文本透传（fail-open）。
				events = append(events, xmlScannerEvent{Text: xmlToolCallOpenTag + s.block + xmlToolCallCloseTag})
			}
			s.block = ""
		case xmlScannerStateResult:
			idx := strings.Index(s.pending, xmlToolResultCloseTag)
			if idx < 0 {
				s.resultHold += len(s.pending)
				s.pending = ""
				return events
			}
			s.resultHold += idx + len(xmlToolResultCloseTag)
			s.pending = s.pending[idx+len(xmlToolResultCloseTag):]
			logger.Warnf("已剥离模型伪造的 <tool_result> 块（%d 字节），防止自答自问假成功", s.resultHold)
			s.resultHold = 0
			s.state = xmlScannerStateText
		default: // xmlScannerStateText
			callIdx := strings.Index(s.pending, xmlToolCallOpenTag)
			resultIdx := strings.Index(s.pending, xmlToolResultOpenTag)
			switch {
			case callIdx >= 0 && (resultIdx < 0 || callIdx < resultIdx):
				events = append(events, xmlScannerEvent{Text: s.emitReady(callIdx)})
				s.pending = s.pending[callIdx+len(xmlToolCallOpenTag):]
				s.block = ""
				s.state = xmlScannerStateCall
			case resultIdx >= 0:
				events = append(events, xmlScannerEvent{Text: s.emitReady(resultIdx)})
				s.pending = s.pending[resultIdx:]
				s.resultHold = 0
				s.state = xmlScannerStateResult
			default:
				events = append(events, xmlScannerEvent{Text: s.emitReady(len(s.pending))})
				return events
			}
		}
	}
}

// emitReady 从 pending 中取前 keep 字节作为可透传文本发出，但保留可能是标签
// 前缀的尾部（跨 chunk 拆开的 "<tool_c..." 等待后续 delta 判定）。仅在
// stateText 下调用；调用后 pending 已被裁剪到保留段。
func (s *xmlToolCallScanner) emitReady(keep int) string {
	text := s.pending[:keep]
	s.pending = s.pending[keep:]
	holdback := trailingTagPrefixLength(text, xmlToolCallOpenTag)
	if extra := trailingTagPrefixLength(text, xmlToolResultOpenTag); extra > holdback {
		holdback = extra
	}
	if holdback == 0 {
		return text
	}
	s.pending = text[len(text)-holdback:] + s.pending
	return text[:len(text)-holdback]
}

// Flush 在流结束时调用：未闭合的半截 <tool_call> 块按普通文本放行；仍在剥离中
// 的未闭合 <tool_result> 块丢弃并记 warning；剩余缓冲全部透传。
func (s *xmlToolCallScanner) Flush() []xmlScannerEvent {
	var events []xmlScannerEvent
	switch s.state {
	case xmlScannerStateCall:
		logger.Warnf("XML 工具协议：<tool_call> 块未闭合，按普通文本放行")
		if text := xmlToolCallOpenTag + s.block + s.pending; text != "" {
			events = append(events, xmlScannerEvent{Text: text})
		}
	case xmlScannerStateResult:
		logger.Warnf("已丢弃流结束时仍未闭合的伪造 <tool_result> 块（%d 字节）", s.resultHold)
		if text := s.pending; text != "" {
			events = append(events, xmlScannerEvent{Text: text})
		}
	default:
		if text := s.emitReady(len(s.pending)); text != "" {
			events = append(events, xmlScannerEvent{Text: text})
		}
	}
	s.state = xmlScannerStateText
	s.pending = ""
	s.block = ""
	s.resultHold = 0
	return events
}

// completeCall 解析一个完整的 tool_call 块体并构造工具调用事件：
// 先严格 parse；失败用 repairOpenAIToolArgsEscapes 风格修复再试（含字符串包裹
// 的 arguments 与多对象拼接恢复）；仍失败返回 false，由调用方降级为文本透传。
func (s *xmlToolCallScanner) completeCall(body string) (xmlScannerEvent, bool) {
	text := strings.TrimSpace(body)
	var payload struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		repaired := repairOpenAIToolArgsEscapes(text)
		if err2 := json.Unmarshal([]byte(repaired), &payload); err2 != nil {
			logger.Warnf("XML 工具协议：tool_call 块 JSON 解析失败，降级为普通文本：%v", err2)
			return xmlScannerEvent{}, false
		}
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		logger.Warnf("XML 工具协议：tool_call 块缺少 name 字段，降级为普通文本")
		return xmlScannerEvent{}, false
	}
	if len(s.advertisedTools) > 0 {
		canonical, declared := s.advertisedTools[name]
		if !declared {
			// 未在目录中声明的工具名：不派发也不静默吞掉，整块按文本放行。
			logger.Warnf("XML 工具协议：模型调用了未声明的工具 %q，降级为普通文本", name)
			return xmlScannerEvent{}, false
		}
		name = canonical
	}
	argsJSON, ok := xmlNormalizeToolArgs(payload.Arguments)
	if !ok {
		logger.Warnf("XML 工具协议：工具 %s 的 arguments 不是合法 JSON 对象，降级为普通文本", name)
		return xmlScannerEvent{}, false
	}
	s.seq++
	callID := namespaceToolCallID(s.modelCallID, fmt.Sprintf("xmltool-%d", s.seq))
	return xmlScannerEvent{
		Call: &runtimecore.ToolInvocation{
			CallID:   callID,
			ToolName: name,
			ArgsJSON: argsJSON,
		},
	}, true
}

// xmlNormalizeToolArgs 把 arguments 归一化为紧凑 JSON 对象字节：
// 空/缺失归一化为 "{}"；接受对象或「字符串包裹的对象」（弱模型常见形态，
// 修复路径）；其余形态失败返回 false。
func xmlNormalizeToolArgs(raw json.RawMessage) ([]byte, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []byte("{}"), true
	}
	candidates := []string{trimmed}
	// 字符串包裹的 JSON 对象："arguments":"{\"a\":1}"
	var wrapped string
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		inner := strings.TrimSpace(wrapped)
		if inner != "" {
			candidates = append(candidates, inner)
		}
	}
	for _, candidate := range candidates {
		var value map[string]any
		if err := json.Unmarshal([]byte(candidate), &value); err == nil && value != nil {
			compacted, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				continue
			}
			return compacted, true
		}
	}
	return nil, false
}
