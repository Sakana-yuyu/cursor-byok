package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractCompactDescriptorsThroughMainPipeline(t *testing.T) {
	input := filepath.Join(t.TempDir(), "bundle.js")
	text := `{1:(t,e,r)=>{r.d(e,{Request:()=>Q,Reply:()=>R});var h=r(8592);class B extends h.HL{static $p(){return"agent.v1."}}class Q extends B{static $(){return["CompactRequest|1 id 9"]}}class R extends B{static $(){return["CompactReply|1 request #0",Q]}}},2:(t,e,r)=>{var n=r(1);const svc={typeName:"agent.v1.CompactService",methods:{run:{name:"Run",I:n.Request,O:n.Reply,kind:x.MethodKind.Unary}}};}}`
	if err := os.WriteFile(input, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := ExtractProtosToDir(input, out); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(out, "agent_v1.proto"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"message CompactRequest", "CompactRequest request = 1;", "rpc Run(CompactRequest) returns (CompactReply) {}"} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("生成结果缺少 %q", want)
		}
	}
}

func TestRequiredAgentShapesIgnorePrefixOnlyTypes(t *testing.T) {
	for _, body := range []string{
		"message ShellStreamStdout { string data = 1; }",
		"message ExecClientControlMessageMetadata { string value = 1; }",
	} {
		if err := validateRequiredAgentShapes("fixture.proto", body); err != nil {
			t.Fatalf("相似前缀不应触发其他消息的形状校验: %v", err)
		}
	}
}
