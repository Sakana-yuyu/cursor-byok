// openai_stream_chat_compat.go 承载 chat completions 流解析侧的厂商兼容处理：
// MiniMax 风格的对象型工具参数深合并，以及 DeepSeek 模板特殊 token 的缓冲式
// 剥离。两者均为响应侧纯文本/JSON 处理，不触碰请求构造，不影响前缀缓存。
package modeladapter

import (
	"bytes"
	"encoding/json"
	"strings"

	"cursor/internal/logger"
)

// deepMergeJSONObject 把 src 递归深合并进 dst：嵌套对象逐字段合并，
// 数组与标量整体替换。用于 MiniMax 等把工具调用参数按 JSON 对象分片下发的
// 供应商——旧逻辑固定按字符串拼接会导致终态 unmarshal 失败断流。
func deepMergeJSONObject(dst, src map[string]any) {
	for key, value := range src {
		if subDst, ok := dst[key].(map[string]any); ok {
			if subSrc, ok := value.(map[string]any); ok {
				deepMergeJSONObject(subDst, subSrc)
				continue
			}
		}
		dst[key] = value
	}
}

// openai 工具参数分片形态标记（按 tool call index 记录，用于混合形态防护）。
const (
	openAIToolShardModeString = "string"
	openAIToolShardModeObject = "object"
)

// decodeJSONObjectArgs 解码对象型工具参数分片。使用 json.Decoder + UseNumber
// 保留数字字面量（json.Number），避免大整数经 float64 往返丢失精度；null 分片
// 归一化为空对象。
func decodeJSONObjectArgs(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var obj map[string]any
	if err := decoder.Decode(&obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return map[string]any{}, nil
	}
	return obj, nil
}

// absorbStringArgsIntoObject 处理分片形态混用（同一 tool call 先字符串后对象）：
// 把先前按字符串分片累积的内容整体解析为 JSON 对象深合并进 merged；无法解析时
// 告警并丢弃先前内容——不静默、不断流，此后以对象分片为准继续累积。
func absorbStringArgsIntoObject(modelCallID string, toolIndex int, prior string, merged map[string]any) {
	trimmed := strings.TrimSpace(prior)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return
	}
	priorObj, err := decodeJSONObjectArgs([]byte(trimmed))
	if err != nil || len(priorObj) == 0 {
		logger.Warn("openai chat 工具参数分片形态混用：先前字符串分片无法解析为 JSON 对象，已忽略",
			"model_call_id", modelCallID, "tool_index", toolIndex, "bytes", len(prior), "err", err)
		return
	}
	deepMergeJSONObject(merged, priorObj)
}

const (
	// deepSeekTokenOpenPrefix / deepSeekTokenCloseSuffix 是 DeepSeek 模板特殊
	// token 的定界符：<｜ ... ｜>（全角竖线 U+FF5C），如
	// <｜begin▁of▁sentence｜>、<｜end▁of▁sentence｜>、<｜User｜> 等。
	deepSeekTokenOpenPrefix  = "<｜"
	deepSeekTokenCloseSuffix = "｜>"
	// deepSeekTokenMaxBytes 单个特殊标记的最大字节长度；缓冲超过该上限仍未
	// 见闭合定界符时视为普通文本放行，防止异常输出导致缓冲无限增长。
	deepSeekTokenMaxBytes = 128
)

// deepSeekSpecialTokenStripper 以缓冲方式从流式文本中剥离 DeepSeek 模板特殊
// token。标记可能被 SSE 分片从任意字节处拆开：Feed 保留可能是半截标记前缀的
// 短缓冲，Flush 在流结束时放行残余。惰性激活：仅当观察到 "<｜" 前缀才开始
// 缓冲（deepseek kind 可在构造时直接激活），普通供应商零开销直通。
type deepSeekSpecialTokenStripper struct {
	buf    []byte
	active bool
}

// newDeepSeekSpecialTokenStripper 构建剥离器；preactive 表示已知是 deepseek
// kind，从一开始就启用缓冲（覆盖首个分片即被拆开的标记）。
func newDeepSeekSpecialTokenStripper(preactive bool) *deepSeekSpecialTokenStripper {
	return &deepSeekSpecialTokenStripper{active: preactive}
}

// Feed 处理一个增量分片，返回可安全下发的已剥离文本。
func (s *deepSeekSpecialTokenStripper) Feed(text string) string {
	if text == "" {
		return ""
	}
	if !s.active {
		// 自动兼容：任何流里出现 "<｜" 模式即激活，无需显式配置 kind。
		if !strings.Contains(text, deepSeekTokenOpenPrefix) {
			return text
		}
		s.active = true
	}
	s.buf = append(s.buf, text...)
	return s.drain(false)
}

// Flush 在流结束时放行缓冲中剩余文本（可能是被流截断的半截标记）。
func (s *deepSeekSpecialTokenStripper) Flush() string {
	if len(s.buf) == 0 {
		return ""
	}
	tail := string(s.buf)
	s.buf = nil
	return tail
}

// drain 从缓冲中剥离所有完整标记，返回确定无需再缓冲的前缀文本；
// final 表示流已结束（此时不再等待可能的半截标记）。
func (s *deepSeekSpecialTokenStripper) drain(final bool) string {
	var out []byte
	for len(s.buf) > 0 {
		start := bytes.IndexByte(s.buf, '<')
		if start < 0 {
			out = append(out, s.buf...)
			s.buf = s.buf[:0]
			break
		}
		out = append(out, s.buf[:start]...)
		rest := s.buf[start:]
		if !hasDeepSeekTokenOpenPrefix(rest) {
			out = append(out, rest...)
			s.buf = s.buf[:0]
			break
		}
		end := bytes.Index(rest, []byte(deepSeekTokenCloseSuffix))
		if end >= 0 {
			// 完整标记：整体剥离（含定界符），继续处理剩余缓冲。
			s.buf = rest[end+len(deepSeekTokenCloseSuffix):]
			continue
		}
		if final || len(rest) >= deepSeekTokenMaxBytes {
			// 流结束或超长未闭合：不是合法标记，按普通文本放行。
			out = append(out, rest...)
			s.buf = s.buf[:0]
			break
		}
		// 半截标记：保留短缓冲等待后续分片拼合。
		s.buf = rest
		break
	}
	return string(out)
}

// hasDeepSeekTokenOpenPrefix 判断 buf 是否以 "<｜" 开头；允许定界符自身被
// 分片拆开（此时只要已到达的字节与前缀对齐即视为潜在标记开头）。
func hasDeepSeekTokenOpenPrefix(buf []byte) bool {
	openBytes := []byte(deepSeekTokenOpenPrefix)
	n := min(len(buf), len(openBytes))
	return bytes.Equal(openBytes[:n], buf[:n])
}
