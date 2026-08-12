package forwarder

import (
	"strings"
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/i18n"
)

func newGrokMultiAgentError(statusCode int, body string) error {
	statusErr := &modeladapter.HTTPStatusError{
		StatusCode: statusCode,
		Message:    "openai adapter status=" + itoa(statusCode) + " body=" + body,
		Body:       body,
	}
	return providerTerminalError{cause: &modeladapter.ChannelError{
		Cause:    statusErr,
		Provider: "openai",
		BaseURL:  "https://api.x.ai/v1",
		Model:    "grok-4.20-multi-agent-0309",
	}}
}

// itoa avoids importing strconv solely for the test helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestKnownProviderErrorHintForLocaleGrokMultiAgent(t *testing.T) {
	const body = "Client-side tools for multi-agent models require beta access"
	err := newGrokMultiAgentError(400, body)

	cases := []struct {
		locale string
		substr string
	}{
		{i18n.LocaleZhCN, "Grok 多代理变体"},
		{i18n.LocaleEnUS, "multi-agent variant"},
		{i18n.LocaleJaJP, "マルチエージェント"},
		{i18n.LocaleRuRU, "многоагентный"},
	}
	for _, tc := range cases {
		t.Run(tc.locale, func(t *testing.T) {
			hint := knownProviderErrorHintForLocale(tc.locale, err)
			if hint == "" {
				t.Fatalf("expected non-empty hint for locale %s", tc.locale)
			}
			if !strings.Contains(hint, tc.substr) {
				t.Fatalf("locale %s: expected hint containing %q, got %q", tc.locale, tc.substr, hint)
			}
			if !strings.Contains(hint, "grok-4.5") {
				t.Fatalf("locale %s: hint should recommend an alternative model, got %q", tc.locale, hint)
			}
		})
	}
}

func TestKnownProviderErrorHintBodyVariantBetaAccess(t *testing.T) {
	// body 只含 beta access 措辞（无显式 "client-side tools"）也应命中。
	err := newGrokMultiAgentError(400, "multi-agent models require beta access")
	if hint := knownProviderErrorHintForLocale(i18n.LocaleEnUS, err); hint == "" {
		t.Fatal("expected hint when body mentions beta access for multi-agent models")
	}
}

func TestKnownProviderErrorHintGrok45NoHit(t *testing.T) {
	// grok-4.5 支持 client-side tools，普通 400 不应命中。
	statusErr := &modeladapter.HTTPStatusError{StatusCode: 400, Message: "bad request", Body: "bad request"}
	err := providerTerminalError{cause: &modeladapter.ChannelError{
		Cause:    statusErr,
		Provider: "openai",
		BaseURL:  "https://api.x.ai/v1",
		Model:    "grok-4.5",
	}}
	if hint := knownProviderErrorHintForLocale(i18n.LocaleZhCN, err); hint != "" {
		t.Fatalf("expected no hint for grok-4.5, got %q", hint)
	}
}

func TestKnownProviderErrorHintNonGrokModelNoHit(t *testing.T) {
	// 非 grok 模型即便返回同样 body 文案也不应命中。
	statusErr := &modeladapter.HTTPStatusError{
		StatusCode: 400,
		Message:    "Client-side tools for multi-agent models require beta access",
		Body:       "Client-side tools for multi-agent models require beta access",
	}
	err := providerTerminalError{cause: &modeladapter.ChannelError{
		Cause:    statusErr,
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
		Model:    "gpt-5.6",
	}}
	if hint := knownProviderErrorHintForLocale(i18n.LocaleZhCN, err); hint != "" {
		t.Fatalf("expected no hint for non-grok model, got %q", hint)
	}
}

func TestKnownProviderErrorHintStatus500NoHit(t *testing.T) {
	// grok multi-agent 但状态码非 400（如 500）不命中——仅匹配已知的 capability 400。
	err := newGrokMultiAgentError(500, "internal error")
	if hint := knownProviderErrorHintForLocale(i18n.LocaleZhCN, err); hint != "" {
		t.Fatalf("expected no hint for 500 status, got %q", hint)
	}
}

func TestKnownProviderErrorHintNoChannelIdentityNoHit(t *testing.T) {
	// 没有 ChannelError 身份信息（未经 router 包装）时不命中。
	statusErr := &modeladapter.HTTPStatusError{
		StatusCode: 400,
		Body:       "Client-side tools for multi-agent models require beta access",
	}
	if hint := knownProviderErrorHintForLocale(i18n.LocaleZhCN, statusErr); hint != "" {
		t.Fatalf("expected no hint without channel identity, got %q", hint)
	}
}

func TestKnownProviderErrorHintNil(t *testing.T) {
	if hint := knownProviderErrorHintForLocale(i18n.LocaleZhCN, nil); hint != "" {
		t.Fatalf("expected empty hint for nil cause, got %q", hint)
	}
}

func TestKnownProviderErrorHintReadsCurrentLocale(t *testing.T) {
	// knownProviderErrorHint 应读取进程级 CurrentLocale；测试后复位避免串扰。
	t.Cleanup(func() { i18n.SetCurrentLocale(i18n.DefaultLocale) })

	err := newGrokMultiAgentError(400, "Client-side tools for multi-agent models require beta access")

	i18n.SetCurrentLocale(i18n.LocaleEnUS)
	if hint := knownProviderErrorHint(err); !strings.Contains(hint, "multi-agent variant") {
		t.Fatalf("expected en-US hint via CurrentLocale, got %q", hint)
	}

	i18n.SetCurrentLocale(i18n.LocaleZhCN)
	if hint := knownProviderErrorHint(err); !strings.Contains(hint, "Grok 多代理变体") {
		t.Fatalf("expected zh-CN hint via CurrentLocale, got %q", hint)
	}
}
