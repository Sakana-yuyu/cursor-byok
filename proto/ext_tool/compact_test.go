package main

import (
	"os"
	"strings"
	"testing"
)

func TestCompactRealBundle(t *testing.T) {
	path := os.Getenv("CURSOR_COMPACT_BUNDLE")
	if path == "" {
		t.Skip("未指定只读真实包路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	infos := buildModuleInfos(text)
	starts := buildModuleStarts(text)
	msgs, enums, err := extractCompactTypes(text, infos, extractMessages(text, starts, nil), extractEnums(text, starts), buildWebpackGraph(text, infos))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || len(enums) == 0 {
		t.Fatalf("真实包未解析到紧凑消息或枚举: %d/%d", len(msgs), len(enums))
	}
	fields := 0
	for _, msg := range msgs {
		fields += len(msg.Fields)
	}
	t.Logf("真实包紧凑消息=%d 枚举=%d 字段=%d", len(msgs), len(enums), fields)
}

func compactExtractForTest(text string) ([]Message, []Enum, error) {
	if strings.HasPrefix(text, "{") && !strings.Contains(text, "8592:") {
		text = `{8592:(t,e,n)=>{n.d(e,{QT:()=>a});function a(t,e,r,n,s){return t.util.setEnumType(u,e+r,c),u}},` + text[1:]
	}
	infos := buildModuleInfos(text)
	return extractCompactTypes(text, infos, nil, nil, buildWebpackGraph(text, infos))
}

func TestCompactEnumHelperRenaming(t *testing.T) {
	text := `{777:(m,out,load)=>{load.d(out,{Renamed:()=>make});function make(runtime,prefix,name,values,aliases){return runtime.util.setEnumType(result,prefix+name,entries),result}},9:(t,e,r)=>{var helper=r(777),E=(0,helper.Renamed)(x.proto3,"agent.v1.","Mode",[[0,"ZERO"]],1),F=(0,r(777).Renamed)(x.proto3,"agent.v1.","Other",[[0,"OTHER_ZERO"]]);}}`
	_, enums, err := compactExtractForTest(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(enums) != 2 || enums[0].Values[0].Name != "MODE_ZERO" || enums[1].Values[0].Name != "OTHER_ZERO" {
		t.Fatalf("重命名后应依据 helper 源码与导出图识别: %+v", enums)
	}
}

func TestCompactEnumCommaExpression(t *testing.T) {
	text := `{9:(t,e,r)=>{var h=r(8592),E=(x.proto3,x.proto3,(0,h.QT)(x.proto3,"agent.v1.","Mode",[[0,"ZERO"]],1));class B extends h.HL{static $p(){return"agent.v1."}}class M extends B{static $(){return["Msg|1 mode #0",E]}}}}`
	msgs, enums, err := compactExtractForTest(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(enums) != 1 || len(msgs) != 1 || msgs[0].Fields[0].Kind != "enum" {
		t.Fatalf("逗号表达式中枚举未识别: %+v/%+v", msgs, enums)
	}
}

func TestCompactRejectsEnumWithDifferentFinalValue(t *testing.T) {
	for _, suffix := range []string{`,Other)`, `)||Other`, `)+Other`} {
		text := `{9:(t,e,r)=>{var h=r(8592),E=(x.proto3,(0,h.QT)(x.proto3,"agent.v1.","Mode",[[0,"ZERO"]],1)` + suffix + `;}}`
		if _, _, err := compactExtractForTest(text); err == nil {
			t.Fatalf("应拒绝最终值不是 helper 结果的表达式: %s", suffix)
		}
	}
}

func TestCompactDescriptors(t *testing.T) {
	text := `{100:(t,e,n)=>{n.d(e,{Child:()=>C});var h=n(8592);const p="agent.v1.";class B extends h.HL{static $p(){return p}}class C extends B{static $(){return["Outer.Child|1 text 9"]}}var E=(0,h.QT)(x.proto3,p,"Outer.Mode",[[0,"UNSPECIFIED"],[1,"ON"]],1);class M extends B{static $(){return["Outer|1 id 13|2 child #0?|3 entries 9,#0|4 mode #1 choice|5 tags 9*|6 history #0*|7 explicit 8 ?|8 counters 9,5|9 modes 9,#1",C,E]}}}}`
	msgs, enums, err := compactExtractForTest(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || len(enums) != 1 {
		t.Fatalf("消息=%d 枚举=%d", len(msgs), len(enums))
	}
	if msgs[0].TypeName != "agent.v1.Outer.Child" || msgs[0].ShortName != "Outer.Child" {
		t.Fatalf("嵌套类型不正确: %+v", msgs[0])
	}
	f := msgs[1].Fields
	if len(f) != 9 || f[0].T != 13 || !f[1].Opt || f[1].Kind != "message" || f[1].T != "agent.v1.Outer.Child" {
		t.Fatalf("标量或引用不正确: %+v", f)
	}
	if f[2].Kind != "map" || f[2].MapKey != 9 || f[2].MapValueKind != "message" || f[2].MapValueT != "agent.v1.Outer.Child" {
		t.Fatalf("map 不正确: %+v", f[2])
	}
	if f[3].Kind != "enum" || f[3].T != "agent.v1.Outer.Mode" || f[3].Oneof != "choice" || !f[4].Repeated || !f[5].Repeated || !f[6].Opt {
		t.Fatalf("修饰符不正确: %+v", f)
	}
	if f[7].MapValueT != 5 || f[8].MapValueKind != "enum" {
		t.Fatalf("map 值不正确: %+v", f)
	}
	if enums[0].Values[1].Name != "MODE_ON" {
		t.Fatalf("枚举前缀不正确: %+v", enums[0])
	}
}

func TestCompactModuleIsolation(t *testing.T) {
	text := `{1:(t,e,n)=>{n.d(e,{Child:()=>C,Base:()=>B,Mode:()=>E});var h=n(8592);class B extends h.HL{static $p(){return"alpha.v1."}}class C extends B{static $(){return["Child"]}}var E=(0,h.QT)(x.proto3,"alpha.v1.","Status",[[0,"STATUS_ZERO"]]);},2:(t,e,n)=>{var h=n(8592),a=n(1);class B extends h.HL{static $p(){return"beta.v1."}}class C extends B{static $(){return["Child"]}}class M extends B{static $(){return["Parent|1 local #0|2 remote #1|3 status #2",C,a.Child,a.Mode]}}class D extends a.Base{static $(){return["Inherited"]}}}}`
	msgs, enums, err := compactExtractForTest(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 || len(enums) != 1 {
		t.Fatalf("类型数量: %d/%d", len(msgs), len(enums))
	}
	if msgs[2].Fields[0].T != "beta.v1.Child" || msgs[2].Fields[1].T != "alpha.v1.Child" || msgs[2].Fields[2].T != "alpha.v1.Status" || msgs[3].TypeName != "alpha.v1.Inherited" {
		t.Fatalf("模块隔离失败: %+v", msgs)
	}
	if enums[0].Values[0].Name != "STATUS_ZERO" {
		t.Fatal(enums[0])
	}
}

func TestCompactRejectsInvalidDescriptors(t *testing.T) {
	for _, desc := range []string{"Bad|1 value #1", "Bad|1 value #x", "Bad|1 value 11", "Bad|0 value 9", "Bad|1 value 9|1 other 9", "Bad|1 value 9 mystery extra", "Bad|1 value 2,9", "Bad|1 value 9,#0", "Bad|1 value 9* group"} {
		t.Run(desc, func(t *testing.T) {
			text := `class B extends x.HL{static $p(){return"agent.v1."}}class M extends B{static $(){return["` + desc + `",Missing]}}`
			if _, _, err := compactExtractForTest(text); err == nil {
				t.Fatalf("应该拒绝 %q", desc)
			}
		})
	}
}

func TestCompactRejectsMissingOrAmbiguousPackage(t *testing.T) {
	for _, text := range []string{
		`class M extends Missing{static $(){return["Msg"]}}`,
		`class B extends x.HL{static $p(){return p}}const p="agent.v1.",p="other.v1.";class M extends B{static $(){return["Msg"]}}`,
		`class B extends C{}class C extends B{}class M extends B{static $(){return["Msg"]}}`,
	} {
		if _, _, err := compactExtractForTest(text); err == nil {
			t.Fatalf("应该拒绝包名: %s", text)
		}
	}
}

func TestCompactEnumDirectRequireAndAliases(t *testing.T) {
	text := `{9:(t,e,r)=>{var E=(0,r(8592).QT)(x.proto3,"agent.v1.","HTTPStatus",[[0,"ZERO"],[1,"OK"]],1),F=(0,r(8592).QT)(x.proto3,"agent.v1.","Kind",[[0,"KIND_ZERO"],[1,"KIND_ONE"]],["Zero","One"]);}}`
	_, enums, err := compactExtractForTest(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(enums) != 2 || enums[0].Values[0].Name != "H_T_T_P_STATUS_ZERO" || enums[1].Values[1].Name != "KIND_ONE" {
		t.Fatalf("枚举名称不正确: %+v", enums)
	}
}

func TestCompactRejectsAmbiguousLocalReference(t *testing.T) {
	text := `class B extends x.HL{static $p(){return"agent.v1."}}class C extends B{static $(){return["One"]}}class C extends B{static $(){return["Two"]}}class M extends B{static $(){return["Msg|1 child #0",C]}}`
	if _, _, err := compactExtractForTest(text); err == nil || !strings.Contains(err.Error(), "C") {
		t.Fatalf("应该拒绝歧义引用: %v", err)
	}
}

func TestCompactRejectsDynamicDescriptor(t *testing.T) {
	text := `class B extends x.HL{static $p(){return"agent.v1."}}class M extends B{static $(){return["Msg"]+["Other"]}}`
	if _, _, err := compactExtractForTest(text); err == nil {
		t.Fatal("应拒绝数组后面的动态表达式")
	}
}

func TestCompactRejectsUnknownEnumHelper(t *testing.T) {
	text := `{9:(t,e,r)=>{var E=(0,unknown.QT)(x.proto3,"agent.v1.","Kind",[[0,"ZERO"]],1);}}`
	if _, _, err := compactExtractForTest(text); err == nil {
		t.Fatal("应拒绝无法证明来源的枚举 helper")
	}
}

func TestCompactRejectsInvalidEnumAliases(t *testing.T) {
	for _, aliases := range []string{`[dynamic]`, `["Zero","One"]`} {
		text := `{9:(t,e,r)=>{var E=(0,r(8592).QT)(x.proto3,"agent.v1.","Kind",[[0,"KIND_ZERO"]],` + aliases + `);}}`
		if _, _, err := compactExtractForTest(text); err == nil {
			t.Fatalf("应拒绝无效枚举别名: %s", aliases)
		}
	}
}
