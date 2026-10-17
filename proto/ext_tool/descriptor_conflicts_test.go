package main

import (
	"strings"
	"testing"
)

func TestCompactRejectsConflictingFullNames(t *testing.T) {
	for _, second := range []string{"Duplicate|2 new_field 5", "Duplicate|1 old_field 5"} {
		text := `class B extends x.HL{static $p(){return"agent.v1."}}class A extends B{static $(){return["Duplicate|1 old_field 9"]}}class C extends B{static $(){return["` + second + `"]}}`
		if _, _, err := compactExtractForTest(text); err == nil {
			t.Fatal("相同全限定名的冲突描述必须拒绝")
		}
	}
}

func TestCompactRejectsFakeRequireHelper(t *testing.T) {
	text := `{9:(t,e,r)=>{function wrong(id){return {QT:Other}}var E=(0,wrong(8592).QT)(x.proto3,"agent.v1.","FakeEnum",[[0,"ZERO"]],1);}}`
	if _, _, err := compactExtractForTest(text); err == nil || !strings.Contains(err.Error(), "helper") {
		t.Fatalf("只能通过所属模块的真实 require 调用: %v", err)
	}
}

func TestDescriptorConflictsCompareResolvedTypes(t *testing.T) {
	messages := []Message{
		{TypeName: "agent.v1.Child", VarName: "C", Package: "agent.v1"},
		{TypeName: "agent.v1.Parent", Package: "agent.v1", Fields: []Field{{No: 1, Name: "child", Kind: "message", T: "C"}}},
		{TypeName: "agent.v1.Parent", Package: "agent.v1", Fields: []Field{{No: 1, Name: "child", Kind: "message", T: "agent.v1.Child"}}},
	}
	if err := validateDescriptorConflicts(messages, nil, newTypeResolver(messages, nil, nil), nil); err != nil {
		t.Fatal(err)
	}
	enums := []Enum{
		{TypeName: "agent.v1.Mode", Values: []EnumValue{{No: 0, Name: "ZERO"}}},
		{TypeName: "agent.v1.Mode", Values: []EnumValue{{No: 0, Name: "OTHER"}}},
	}
	if err := validateDescriptorConflicts(messages, enums, nil, nil); err == nil {
		t.Fatal("同名枚举的冲突必须拒绝")
	}
}
