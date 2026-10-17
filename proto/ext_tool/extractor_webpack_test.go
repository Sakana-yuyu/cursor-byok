package main

import (
	"strings"
	"testing"
)

func TestWebpackModuleParametersResolveExports(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"旧版箭头函数", `{10:(e,t,n)=>{n.d(t,{K:()=>Local});},30:(e,t,n)=>{var ns=n(10);}}`},
		{"新版箭头函数", `{10:(t,e,r)=>{r.d(e,{K:()=>Local});},30:(t,e,r)=>{var ns=r(10);}}`},
		{"逗号表达式只取末项", `{10:(t,e,r)=>{e.K=Local;},20:(t,e,r)=>{e.K=Other;},30:(t,e,r)=>{var before=r(20),ns=(r(20),r(10));}}`},
		{"另一种参数顺序", `{10:(e,t,r)=>{r.d(t,{K:()=>Local});},30:(e,t,r)=>{var ns=r(10);}}`},
		{"函数声明含空白", `{10: function ( module, exports, load ) {load.d(exports,{K:()=>Local});},30: function(module,exports,load){const ns=load(10);}}`},
		{"双参数CommonJS", `{10:(t,e)=>{e.K=Local;},30:(t,e,r)=>{var ns=r(10);}}`},
		{"module.exports属性", `{10:(mod)=>{mod.exports.K=Local;},30:(t,e,r)=>{var ns=r(10);}}`},
		{"CommonJS星号重导出", `{10:(t,e)=>{e.K=Local;},20:(t,e,r)=>{var s=this&&this.__exportStar||function(t,e){};s(r(10),e);},30:(t,e,r)=>{var ns=r(20);}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			infos := buildModuleInfos(tc.body)
			graph := buildWebpackGraph(tc.body, infos)
			if len(infos) < 2 {
				t.Fatalf("未识别模块边界: %+v", infos)
			}
			resolver := newTypeResolver([]Message{{TypeName: "test.Real", VarName: "Local", ModuleStart: infos[0].Start}}, nil, nil)
			got, err := graph.resolveQualifiedRef("ns", "K", infos[len(infos)-1].Start, resolver, "", "message")
			if err != nil || got != "test.Real" {
				t.Fatalf("解析结果 = %q, %v", got, err)
			}
		})
	}
}

func TestWebpackModuleParametersKeepStrictResolution(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"旧变量不能冒充导出对象", `{10:(t,e,r)=>{r.d(t,{K:()=>Local});t.K=Local;},30:(t,e,r)=>{var ns=r(10);}}`, `has no export "K"`},
		{"旧变量不能冒充require", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=n(10);}}`, `has no namespace import "ns"`},
		{"双参数模块不能冒充require", `{10:(t,e)=>{e.K=Local;},30:(t,e)=>{var ns=r(10);}}`, `has no namespace import "ns"`},
		{"直接导入后取属性拒绝", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=r(10).Other;}}`, `has no namespace import "ns"`},
		{"直接导入后调用拒绝", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=r(10)();}}`, `has no namespace import "ns"`},
		{"直接导入后二元表达式拒绝", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=r(10)||Fallback;}}`, `has no namespace import "ns"`},
		{"逗号导入后取属性拒绝", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=(r(20),r(10)).Other;}}`, `has no namespace import "ns"`},
		{"逗号导入后调用拒绝", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=(r(20),r(10))();}}`, `has no namespace import "ns"`},
		{"逗号导入后二元表达式拒绝", `{10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=(r(20),r(10))||Fallback;}}`, `has no namespace import "ns"`},
		{"重复导出拒绝", `{10:(t,e,r)=>{r.d(e,{K:()=>Local,K:()=>Other});},30:(t,e,r)=>{var ns=r(10);}}`, `has multiple targets`},
		{"导出getter属性访问拒绝", `{10:(t,e,r)=>{r.d(e,{K:()=>Local.Other});},30:(t,e,r)=>{var ns=r(10);}}`, `has no export "K"`},
		{"导出getter调用拒绝", `{10:(t,e,r)=>{r.d(e,{K:()=>Local()});},30:(t,e,r)=>{var ns=r(10);}}`, `has no export "K"`},
		{"导出getter运算拒绝", `{10:(t,e,r)=>{r.d(e,{K:()=>Local||Fallback});},30:(t,e,r)=>{var ns=r(10);}}`, `has no export "K"`},
		{"缺失目标拒绝", `{10:(t,e,r)=>{e.K=Missing;},30:(t,e,r)=>{var ns=r(10);}}`, `with no internal definition`},
		{"跨模块同名不能补缺", `{10:(t,e,r)=>{e.K=Missing;},20:(t,e,r)=>{e.K=Missing;},30:(t,e,r)=>{var ns=r(10);}}`, `with no internal definition`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			infos := buildModuleInfos(tc.body)
			graph := buildWebpackGraph(tc.body, infos)
			messages := []Message{{TypeName: "test.Real", VarName: "Local", ModuleStart: infos[0].Start}}
			if len(infos) == 3 {
				messages = append(messages, Message{TypeName: "test.Wrong", VarName: "Missing", ModuleStart: infos[1].Start})
			}
			resolver := newTypeResolver(messages, nil, nil)
			_, err := graph.resolveQualifiedRef("ns", "K", infos[len(infos)-1].Start, resolver, "", "message")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("预期错误包含 %q，实际 %v", tc.want, err)
			}
		})
	}
}

func TestWebpackBootstrapGetsIndependentScope(t *testing.T) {
	text := `var __webpack_modules__={10:(t,e,r)=>{e.K=Local;},30:(t,e,r)=>{var ns=r(999);}};
function __webpack_require__(id){return __webpack_modules__[id];}
var __webpack_exports__={};(()=>{"use strict";__webpack_require__.r(__webpack_exports__);var ns=__webpack_require__(10);const ServiceMarker={};})();`
	infos := buildModuleInfos(text)
	if len(infos) != 3 {
		t.Fatalf("预期两个数字模块和一个入口作用域，实际 %+v", infos)
	}
	entry := infos[2]
	if entry.ID == "10" || entry.ID == "30" || entry.Start != strings.Index(text, `(()=>{"use strict"`) {
		t.Fatalf("入口应使用独立 ID 与 IIFE 起点，实际 %+v", entry)
	}
	starts := buildModuleStarts(text)
	if len(starts) != len(infos) || starts[2] != entry.Start || moduleStartForPos(starts, strings.Index(text, "ServiceMarker")) != entry.Start {
		t.Fatalf("入口描述符未归属独立作用域: %v", starts)
	}
	graph := buildWebpackGraph(text, infos)
	resolver := newTypeResolver([]Message{{TypeName: "test.Real", VarName: "Local", ModuleStart: infos[0].Start}}, nil, nil)
	got, err := graph.resolveQualifiedRef("ns", "K", entry.Start, resolver, "", "message")
	if err != nil || got != "test.Real" {
		t.Fatalf("入口导入解析结果 = %q, %v", got, err)
	}
	if targets := graph.namespaceImports["30:ns"]; len(targets) != 1 || targets[0] != "999" {
		t.Fatalf("入口导入污染了数字模块: %v", targets)
	}
}

func TestWebpackBootstrapRequiresExplicitRuntime(t *testing.T) {
	cases := []string{
		`var modules={10:(t,e,r)=>{}};var __webpack_exports__={};(()=>{"use strict";__webpack_require__.r(__webpack_exports__);})();`,
		`var modules={10:(t,e,r)=>{}};function __webpack_require__(id){};(()=>{"use strict";__webpack_require__.r(__webpack_exports__);})();`,
		`var modules={10:(t,e,r)=>{}};function __webpack_require__(id){};var __webpack_exports__={};(()=>{"use strict";ordinary();})();`,
	}
	for _, text := range cases {
		if infos := buildModuleInfos(text); len(infos) != 1 {
			t.Fatalf("缺少明确 webpack 入口证据时不应扩展作用域: %+v", infos)
		}
	}
}
