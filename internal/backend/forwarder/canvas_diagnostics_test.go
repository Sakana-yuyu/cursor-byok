package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

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
