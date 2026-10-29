package modelcontext

import (
	"reflect"
	"testing"
)

func TestNormalizeCustomThinkingEffort(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "adaptive", want: "adaptive"},
		{raw: " Deep-Think ", want: "deep-think"},
		{raw: "v2_reasoning", want: "v2_reasoning"},
		{raw: "none", want: "none"},
		{raw: "", want: ""},
		{raw: "max", want: ""},       // 标准档位不允许作自定义档位
		{raw: "disabled", want: ""},  // 同上
		{raw: "has space", want: ""}, // 含空白
		{raw: "has:colon", want: ""}, // 冒号破坏变体串拆分
		{raw: "-leading", want: ""},  // 必须字母数字开头
		{raw: "中文", want: ""},
	}
	for _, test := range tests {
		if got := NormalizeCustomThinkingEffort(test.raw); got != test.want {
			t.Fatalf("NormalizeCustomThinkingEffort(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestNormalizeCustomThinkingEffortsDedupesAndCaps(t *testing.T) {
	values := []string{"adaptive", "ADAPTIVE", "turbo", "max", "bad value", "a", "b", "c", "d", "e", "f", "g", "h", "i"}
	got := NormalizeCustomThinkingEfforts(values)
	// 去重（adaptive/ADAPTIVE）、剔除标准档位与非法项、限量 8 个。
	want := []string{"adaptive", "turbo", "a", "b", "c", "d", "e", "f"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if NormalizeCustomThinkingEfforts(nil) != nil {
		t.Fatal("nil input must stay nil")
	}
	if NormalizeCustomThinkingEfforts([]string{"max", "  "}) != nil {
		t.Fatal("all-invalid input must normalize to nil")
	}
}

func TestContainsCustomThinkingEffort(t *testing.T) {
	values := []string{"adaptive", "deep-think"}
	if !ContainsCustomThinkingEffort(values, " Deep-Think ") {
		t.Fatal("match should ignore case and whitespace")
	}
	if ContainsCustomThinkingEffort(values, "turbo") || ContainsCustomThinkingEffort(values, "") {
		t.Fatal("non-member or empty must not match")
	}
	if ContainsCustomThinkingEffort(nil, "adaptive") {
		t.Fatal("nil list must not match")
	}
}

func TestFirstInvalidCustomThinkingEffort(t *testing.T) {
	if got := FirstInvalidCustomThinkingEffort([]string{"adaptive", "bad value"}); got != "bad value" {
		t.Fatalf("first invalid = %q, want bad value", got)
	}
	if got := FirstInvalidCustomThinkingEffort([]string{"adaptive", "max"}); got != "" {
		t.Fatalf("standard-named entries are silently dropped, not invalid: got %q", got)
	}
}
