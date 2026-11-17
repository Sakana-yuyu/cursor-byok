package main

import (
	"regexp"
	"testing"
)

// Cursor 3.27 的 webpack 输出把模块表写成方法简写 `123(t,r,e){`，入口也被压缩。
func TestBuildModuleInfosRecognizesMethodShorthandAndMinifiedEntry(t *testing.T) {
	text := `(()=>{var t={8055(t){var a=1},115(t,r,e){"use strict";r.X=void 0}},r={};` +
		`function e(n){const s=r[n];if(void 0!==s)return s.exports;const i=r[n]={exports:{}};return t[n].call(i.exports,i,i.exports,e),i.exports}` +
		`e.r=t=>{};let n={};(()=>{"use strict";e.r(n),e.d(n,{activate:()=>a})})()})();`
	infos := buildModuleInfos(text)
	byID := map[string]moduleInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}
	if info, ok := byID["8055"]; !ok || info.ModuleVar != "t" {
		t.Fatalf("module 8055 = %#v (ok=%t), want shorthand module with module var t", info, ok)
	}
	if info, ok := byID["115"]; !ok || info.ModuleVar != "t" || info.ExportsVar != "r" || info.RequireVar != "e" {
		t.Fatalf("module 115 = %#v (ok=%t), want params t,r,e", info, ok)
	}
	foundEntry := false
	for _, info := range infos {
		if info.ExportsVar == "n" && info.RequireVar == "e" && len(info.ID) > len("webpack:bootstrap:") && info.ID[:len("webpack:bootstrap:")] == "webpack:bootstrap:" {
			foundEntry = true
		}
	}
	if !foundEntry {
		t.Fatalf("minified bootstrap entry not recognized: %#v", infos)
	}
}

// 未声明 webpack require 运行时的同形代码不能被当作入口。
func TestMinifiedEntryRequiresRuntimeRequire(t *testing.T) {
	text := `let n={};(()=>{"use strict";e.r(n)})();`
	for _, info := range buildModuleInfos(text) {
		if info.ExportsVar == "n" {
			t.Fatalf("entry recognized without a webpack require function: %#v", info)
		}
	}
}

func TestCompactConstRecognizesCommaWrappedPackagePrefix(t *testing.T) {
	m := compactConstRE.FindStringSubmatch(`,A8t=(r.MethodKind.Unary,r.MethodKind.Unary,"aiserver.v1.");`)
	if m == nil || m[1] != "A8t" || m[2] != `"aiserver.v1."` {
		t.Fatalf("compactConstRE match = %#v", m)
	}
}

// 同名常量在嵌套作用域重复声明时，取使用点之前最近的包名前缀定义。
func TestCompactConstantPrefersNearestPackagePrefix(t *testing.T) {
	constants := compactConstants{0: {"i": {
		{value: "agent.v1.", pos: 10},
		{value: "not a prefix", pos: 50},
		{value: "aiserver.v1.", pos: 100},
		{value: "later.v1.", pos: 500},
	}}}
	got, err := compactConstant("i", 0, 200, constants)
	if err != nil || got != "aiserver.v1." {
		t.Fatalf("compactConstant() = %q, %v; want aiserver.v1.", got, err)
	}
	if _, err := compactConstant("i", 0, 5, constants); err == nil {
		t.Fatal("compactConstant() with no prior prefix definition should fail")
	}
}

func TestNewFieldListAcceptsExtraParen(t *testing.T) {
	re := regexp.MustCompile(`([\w$]+)\.typeName\s*=\s*"([\w.]+)"\s*[,;]\s*[\w$]+\.fields\s*=\s*\w+(?:\.\w+)*\.util\.newFieldList\s*\(\s*\(?\s*\(\s*\)\s*=>\s*\[`)
	for _, text := range []string{
		`o.typeName="google.protobuf.Timestamp",o.fields=i.proto3.util.newFieldList((()=>[`,
		`o.typeName="google.protobuf.Timestamp",o.fields=i.proto3.util.newFieldList(()=>[`,
	} {
		if !re.MatchString(text) {
			t.Fatalf("pattern did not match %q", text)
		}
	}
}

// 真实字段名以 unknown_ 开头时不应被当成占位字段；真正的占位形态仍要拦截。
func TestPlaceholderDetectionAllowsRealUnknownPrefixedFields(t *testing.T) {
	if placeholderRe.MatchString(`  string unknown_tool_error_template = 1;`) {
		t.Fatal("real field unknown_tool_error_template flagged as placeholder")
	}
	for _, line := range []string{`  bytes field_12 = 12;`, `  string unknown = 3;`, `  string unknown_4 = 4;`} {
		if !placeholderRe.MatchString(line) {
			t.Fatalf("placeholder %q not detected", line)
		}
	}
}
