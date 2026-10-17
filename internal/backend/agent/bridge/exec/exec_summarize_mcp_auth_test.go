package execbridge

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
)

func TestConvertMcpResultPreservesAuthenticationRequirement(t *testing.T) {
	for _, needsAuth := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_error", true: "authentication_required"}[needsAuth], func(t *testing.T) {
			failure := &agentv1.McpError{Error: "upstream authorization expired"}
			wire, err := proto.Marshal(failure)
			if err != nil {
				t.Fatal(err)
			}
			wire = protowire.AppendTag(wire, 2, protowire.VarintType)
			wire = protowire.AppendVarint(wire, uint64(map[bool]int{false: 0, true: 1}[needsAuth]))
			if err := proto.Unmarshal(wire, failure); err != nil {
				t.Fatal(err)
			}
			converted := convertMcpResult(&agentv1.McpResult{Result: &agentv1.McpResult_Error{Error: failure}})
			got := summarizeMcpResult(converted)
			if !strings.Contains(got, "upstream authorization expired") {
				t.Fatalf("原始 MCP 错误丢失：%q", got)
			}
			if needsAuth && !strings.Contains(strings.ToLower(got), "authentication required") {
				t.Fatalf("认证要求未进入模型可见错误：%q", got)
			}
			if !needsAuth && got != "upstream authorization expired" {
				t.Fatalf("普通错误语义被改变：%q", got)
			}
		})
	}
}
