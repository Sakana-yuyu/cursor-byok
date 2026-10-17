package forwarder

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
)

func TestFormatCanvasDiagnosticsForModelPreservesSaveState(t *testing.T) {
	for _, test := range []struct {
		state string
		want  string
	}{
		{"pending", "Canvas save accepted: validation passed; the canvas publishes asynchronously."},
		{"invalid_path", "Canvas not saved: this is not a valid canvas save path; nothing was published."},
		{"unavailable", "Canvas not saved: the user agent store is unavailable; nothing was published."},
		{"too_large", "Canvas not saved: the canvas source exceeds the size limit; nothing was published."},
		{"typecheck_failed", "Canvas not saved: the type check failed; nothing was published."},
		{"compile_failed", "Canvas not saved: the canvas failed to compile; nothing was published."},
		{"future_state", "Canvas save state: future_state."},
	} {
		t.Run(test.state, func(t *testing.T) {
			for _, hasSuccess := range []bool{false, true} {
				result := &agentv1.CanvasDiagnosticsResult{Result: &agentv1.CanvasDiagnosticsResult_Error{
					Error: &agentv1.CanvasDiagnosticsError{Error: "diagnostics unavailable"},
				}}
				want := test.want
				if hasSuccess {
					result = canvasDiagnosticsSuccess("/canvas.tsx")
					want = "Canvas TypeScript check: no errors.\n\n" + want
				}
				result = canvasDiagnosticsWithSaveFields(t, result, "unverified-id", "", test.state, "")
				if got := formatCanvasDiagnosticsForModel(result); got != want {
					t.Errorf("hasSuccess=%v：got %q, want %q", hasSuccess, got, want)
				}
			}
		})
	}
}

func TestFormatCanvasDiagnosticsForModelSanitizesSaveMetadata(t *testing.T) {
	result := canvasDiagnosticsWithSaveFields(t, &agentv1.CanvasDiagnosticsResult{}, "arbitrary-id",
		"\n Title\x00\t"+strings.Repeat("界", 220), "\n pending\t", "\x7f note\r\n"+strings.Repeat("详", 520))
	got := formatCanvasDiagnosticsForModel(result)
	want := "Canvas save accepted: validation passed; the canvas publishes asynchronously.\n" +
		"Canvas title: Title " + strings.Repeat("界", 194) + "…\n" +
		"Canvas note: note " + strings.Repeat("详", 495) + "…"
	if got != want {
		t.Errorf("保存元数据未净化或限长：got %q, want %q", got, want)
	}
	if strings.Contains(got, "arbitrary-id") || strings.Contains(got, "http") {
		t.Errorf("不应从未经验证的 canvas_id 推导访问地址：%q", got)
	}
	unknown := canvasDiagnosticsWithSaveFields(t, &agentv1.CanvasDiagnosticsResult{}, "", "", strings.Repeat("x", 110), " \n detail\x00 text ")
	if got := formatCanvasDiagnosticsForModel(unknown); got != "Canvas save state: "+strings.Repeat("x", 100)+"…. detail text" {
		t.Errorf("未知状态未客观展示并限长：%q", got)
	}
	failed := canvasDiagnosticsWithSaveFields(t, &agentv1.CanvasDiagnosticsResult{}, "", "", "compile_failed", " \n compiler\x00 unavailable ")
	if got := formatCanvasDiagnosticsForModel(failed); got != "Canvas not saved: compiler unavailable" {
		t.Errorf("保存失败详情未保留：%q", got)
	}
}

func TestFormatCanvasDiagnosticsForModelBoundsSupplementaryCharacters(t *testing.T) {
	result := canvasDiagnosticsWithSaveFields(t, &agentv1.CanvasDiagnosticsResult{}, "", strings.Repeat("🙂", 101),
		"\ufeff pending\u00a0", strings.Repeat("🙂", 251))
	want := "Canvas save accepted: validation passed; the canvas publishes asynchronously.\n" +
		"Canvas title: " + strings.Repeat("🙂", 100) + "…\n" +
		"Canvas note: " + strings.Repeat("🙂", 250) + "…"
	if got := formatCanvasDiagnosticsForModel(result); got != want {
		t.Errorf("非 BMP 字符未按原生长度预算截断：%q", got)
	}
}

func canvasDiagnosticsWithSaveFields(t *testing.T, result *agentv1.CanvasDiagnosticsResult, id, title, state, detail string) *agentv1.CanvasDiagnosticsResult {
	t.Helper()
	wire, err := proto.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range []string{id, title, state, detail} {
		if value != "" {
			wire = protowire.AppendTag(wire, protowire.Number(index+3), protowire.BytesType)
			wire = protowire.AppendString(wire, value)
		}
	}
	if err := proto.Unmarshal(wire, result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIsCanvasFilePathMatchesClientRegex(t *testing.T) {
	matching := []string{
		"/home/u/proj/.cursor/projects/abc/canvases/report.canvas.tsx",
		`E:\proj\.cursor\projects\abc\canvases\report.canvas.tsx`,
		".cursor/projects/abc/canvases/report.canvas.tsx",
		"/home/u/proj/./.cursor/projects/abc/canvases/Report.Canvas.TSX",
		"/home/u/proj/tmp/../.cursor/projects/abc/canvases/report.canvas.tsx",
	}
	for _, path := range matching {
		if !isCanvasFilePath(path) {
			t.Errorf("isCanvasFilePath(%q) = false, want true", path)
		}
	}

	nonMatching := []string{
		"",
		"/home/u/proj/.cursor/projects/abc/canvases/report.tsx",
		"/home/u/proj/.cursor/projects/abc/report.canvas.tsx",
		"/home/u/proj/.cursor/projects/abc/canvases/nested/report.canvas.tsx",
		"/home/u/proj/cursor/projects/abc/canvases/report.canvas.tsx",
		"/home/u/proj/.cursor/skills/canvas/SKILL.md",
	}
	for _, path := range nonMatching {
		if isCanvasFilePath(path) {
			t.Errorf("isCanvasFilePath(%q) = true, want false", path)
		}
	}
}

func TestIsCanvasFilePathMatchesClientAgentStoreAndWorkfilePaths(t *testing.T) {
	for _, path := range []string{
		"/cursor/stores/user/canvases/report.canvas.tsx",
		"/cursor/stores/automation/canvases/job/report.canvas.tsx",
		`\cursor\stores\user\canvases\folder\report.canvas.tsx`,
		"/ws/files/canvases/12345678-abcd-1234-abcd-1234567890ab/source.canvas.tsx",
		`C:\workspace\files\canvases\12345678-ABCD-1234-ABCD-1234567890AB\SOURCE.Canvas.TSX`,
	} {
		if !isCanvasFilePath(path) {
			t.Errorf("真实客户端 Canvas 路径未触发诊断：%q", path)
		}
	}
	for _, path := range []string{
		"/ws/cursor/stores/user/canvases/report.canvas.tsx",
		"/cursor/stores/merchant/canvases/report.canvas.tsx",
		"/cursor/stores/user/canvases/a/b/report.canvas.tsx",
		"/cursor/stores/user/canvases/report.Canvas.TSX",
		"/files/canvases/12345678-abcd-1234-abcd-1234567890ab/source.canvas.tsx",
		"/ws/files/canvases/not-a-uuid/source.canvas.tsx",
		"/ws/files/canvases/12345678-abcd-1234-abcd-1234567890ab/report.canvas.tsx",
		"/ws/files/canvases/12345678-abcd-1234-abcd-1234567890ab/nested/source.canvas.tsx",
	} {
		if isCanvasFilePath(path) {
			t.Errorf("近似路径不应触发 Canvas 诊断：%q", path)
		}
	}
}

func TestShouldCollectCanvasDiagnosticsOnlyOnSuccessfulCanvasEdit(t *testing.T) {
	canvasPath := "/ws/.cursor/projects/p/canvases/a.canvas.tsx"
	success := buildSuccessfulEditResult(canvasPath, "", "x", "", 0, 0, "")
	if !shouldCollectCanvasDiagnostics(success, canvasPath) {
		t.Error("successful canvas edit must request diagnostics")
	}
	if shouldCollectCanvasDiagnostics(buildEditErrorResult(canvasPath, "boom"), canvasPath) {
		t.Error("failed canvas edit must not request diagnostics")
	}
	plain := buildSuccessfulEditResult("/ws/main.go", "", "x", "", 0, 0, "")
	if shouldCollectCanvasDiagnostics(plain, "/ws/main.go") {
		t.Error("non-canvas edit must not request diagnostics")
	}
	if shouldCollectCanvasDiagnostics(nil, canvasPath) {
		t.Error("missing edit result must not request diagnostics")
	}
}

func TestFormatCanvasDiagnosticsForModelMatchesClientOutput(t *testing.T) {
	canvasPath := "/ws/.cursor/projects/p/canvases/a.canvas.tsx"

	if got := formatCanvasDiagnosticsForModel(canvasDiagnosticsSuccess(canvasPath)); got != "Canvas TypeScript check: no errors." {
		t.Errorf("empty diagnostics = %q", got)
	}

	// INFORMATION/HINT are dropped exactly like the client filter does.
	informational := canvasDiagnosticsSuccess(canvasPath,
		diagnostic(agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_INFORMATION, 3, 4, "just saying", "ts"),
	)
	if got := formatCanvasDiagnosticsForModel(informational); got != "Canvas TypeScript check: no errors." {
		t.Errorf("informational-only diagnostics = %q", got)
	}

	single := canvasDiagnosticsSuccess(canvasPath,
		diagnostic(agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR, 12, 5, "Type 'string' is not assignable to type 'number'.", "ts"),
	)
	wantSingle := "Canvas TypeScript check: 1 issue in " + canvasPath + ":\n" +
		"  [ERROR] L12:5 - Type 'string' is not assignable to type 'number'. (ts)"
	if got := formatCanvasDiagnosticsForModel(single); got != wantSingle {
		t.Errorf("single diagnostic = %q, want %q", got, wantSingle)
	}

	multiple := canvasDiagnosticsSuccess(canvasPath,
		diagnostic(agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR, 1, 1, "boom", "ts"),
		diagnostic(agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_WARNING, 2, 2, "meh", ""),
	)
	wantMultiple := "Canvas TypeScript check: 2 issues in " + canvasPath + ":\n" +
		"  [ERROR] L1:1 - boom (ts)\n" +
		"  [WARNING] L2:2 - meh"
	if got := formatCanvasDiagnosticsForModel(multiple); got != wantMultiple {
		t.Errorf("multiple diagnostics = %q, want %q", got, wantMultiple)
	}
}

// 诊断不可用时必须返回空串，调用方据此跳过诊断而不是把写入判为失败。
func TestFormatCanvasDiagnosticsForModelSkipsOnFailure(t *testing.T) {
	if got := formatCanvasDiagnosticsForModel(nil); got != "" {
		t.Errorf("nil result = %q, want empty", got)
	}
	timeout := buildCanvasDiagnosticsTimeoutMessage("exec-1", 7, "/ws/.cursor/projects/p/canvases/a.canvas.tsx")
	if got := formatCanvasDiagnosticsForModel(timeout.GetCanvasDiagnosticsResult()); got != "" {
		t.Errorf("timeout result = %q, want empty", got)
	}
	if timeout.GetId() != 7 || timeout.GetExecId() != "exec-1" {
		t.Errorf("timeout message identity = (%d,%q)", timeout.GetId(), timeout.GetExecId())
	}
}

func TestAppendCanvasDiagnosticsToToolResult(t *testing.T) {
	if got := appendCanvasDiagnosticsToToolResult(`{"success":{}}`, ""); got != `{"success":{}}` {
		t.Errorf("empty diagnostics changed result = %q", got)
	}
	if got := appendCanvasDiagnosticsToToolResult(`{"success":{}}`, "Canvas TypeScript check: no errors."); got != "{\"success\":{}}\n\nCanvas TypeScript check: no errors." {
		t.Errorf("appended result = %q", got)
	}
	if got := appendCanvasDiagnosticsToToolResult("", "Canvas TypeScript check: no errors."); got != "Canvas TypeScript check: no errors." {
		t.Errorf("empty base result = %q", got)
	}
}

func canvasDiagnosticsSuccess(path string, diagnostics ...*agentv1.Diagnostic) *agentv1.CanvasDiagnosticsResult {
	return &agentv1.CanvasDiagnosticsResult{
		Result: &agentv1.CanvasDiagnosticsResult_Success{
			Success: &agentv1.CanvasDiagnosticsSuccess{Path: path, Diagnostics: diagnostics},
		},
	}
}

func diagnostic(severity agentv1.DiagnosticSeverity, line uint32, column uint32, message string, source string) *agentv1.Diagnostic {
	return &agentv1.Diagnostic{
		Severity: severity,
		Range: &agentv1.Range{
			Start: &agentv1.Position{Line: line, Column: column},
			End:   &agentv1.Position{Line: line, Column: column},
		},
		Message: message,
		Source:  source,
	}
}
