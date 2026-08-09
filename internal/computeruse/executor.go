package computeruse

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// Action 是一个 ComputerUse 动作的归一化表示，与平台无关。
// 由 forwarder 注入点从 agentv1.ComputerUseAction 转换而来，保持 computeruse 包独立于 proto 生成代码。
type Action struct {
	Type         string  // mouse_move / click / mouse_down / mouse_up / drag / scroll / type / key / wait / screenshot / cursor_position
	X            int
	Y            int
	Button       string  // left / right / middle
	Count        int     // click 次数
	Direction    string  // scroll: up/down
	Amount       int     // scroll 量
	Text         string  // type 文本
	Key          string  // key 描述
	DurationMs   int     // wait 时长 / key 按住时长
	ModifierKeys string  // 修饰键（当前并入 key 处理，保留字段）
	Path         []Point // drag 路径
}

// Point 是一个屏幕坐标。
type Point struct{ X, Y int }

// Executor 是 ComputerUse 动作执行器的抽象，支持桌面（Win32）与浏览器（MCP 转发）两种后端。
type Executor interface {
	// Execute 按顺序执行 actions，末尾自动截图，返回带截图的结果。
	Execute(actions []Action) Result
}

// DesktopExecutor 操作真实桌面（Win32 截图 + SendInput 鼠标键盘）。
type DesktopExecutor struct{}

// Execute 实现 Executor：按顺序执行 actions，末尾自动截图。
func (DesktopExecutor) Execute(actions []Action) Result {
	return executeDesktop(actions)
}

// Result 是执行动作序列的产出，对应 agentv1.ComputerUseResult 的关键字段。
// ScreenshotBase64 为最终截图的 PNG base64（无 data: 前缀），供 vision 模型直接读取。
type Result struct {
	Success           bool
	ScreenshotBase64  string
	ActionCount       int
	DurationMs        int64
	Error             string
	Log               string
}

// Execute 是便捷函数，等价于 DesktopExecutor{}.Execute(actions)（向后兼容）。
func Execute(actions []Action) Result {
	return executeDesktop(actions)
}

// executeDesktop 是桌面执行器的核心实现（Win32）。
func executeDesktop(actions []Action) Result {
	start := time.Now()
	if len(actions) == 0 {
		return Result{Error: "no actions"}
	}

	var logLines []string
	for i, action := range actions {
		if err := executeAction(action); err != nil {
			return Result{
				Success: false,
				ActionCount: i,
				DurationMs: time.Since(start).Milliseconds(),
				Error: fmt.Sprintf("action %d (%s) failed: %v", i, action.Type, err),
				Log: strings.Join(logLines, "\n"),
			}
		}
		logLines = append(logLines, fmt.Sprintf("[%d] %s ok", i, describeAction(action)))
	}

	// 末尾截图（若最后一个动作不是 screenshot，补一张让模型看到执行后的状态）。
	var screenshot string
	last := actions[len(actions)-1]
	if last.Type == "screenshot" || shouldAutoScreenshot(actions) {
		png, err := CaptureScreen(0)
		if err != nil {
			return Result{
				Success: false,
				ActionCount: len(actions),
				DurationMs: time.Since(start).Milliseconds(),
				Error: fmt.Sprintf("actions ok but final screenshot failed: %v", err),
				Log: strings.Join(logLines, "\n"),
			}
		}
		screenshot = base64.StdEncoding.EncodeToString(png)
	}

	return Result{
		Success:          true,
		ScreenshotBase64: screenshot,
		ActionCount:      len(actions),
		DurationMs:       time.Since(start).Milliseconds(),
		Log:             strings.Join(logLines, "\n"),
	}
}

// shouldAutoScreenshot 决定执行完动作序列后是否补一张截图：
// 只要有任何状态改变类动作（点击/输入/滚动/按键/拖拽），就截图让模型看到结果。
func shouldAutoScreenshot(actions []Action) bool {
	for _, a := range actions {
		switch a.Type {
		case "click", "mouse_down", "mouse_up", "drag", "scroll", "type", "key":
			return true
		}
	}
	return false
}

func executeAction(action Action) error {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "mouse_move", "move":
		return MouseMove(action.X, action.Y)
	case "click":
		return MouseClick(action.X, action.Y, normalizeButton(action.Button), normalizeCount(action.Count))
	case "mouse_down":
		return MouseDown(normalizeButton(action.Button))
	case "mouse_up":
		return MouseUp(normalizeButton(action.Button))
	case "drag":
		return executeDrag(action)
	case "scroll":
		return Scroll(action.X, action.Y, normalizeDirection(action.Direction), normalizeAmount(action.Amount))
	case "type":
		return KeyType(action.Text)
	case "key":
		// modifier_keys 与 key 合并处理：若 modifier_keys 非空，拼成 "mod1+mod2+key"。
		key := strings.TrimSpace(action.Key)
		if mod := strings.TrimSpace(action.ModifierKeys); mod != "" {
			if key != "" {
				key = mod + "+" + key
			} else {
				key = mod
			}
		}
		if action.DurationMs > 0 {
			// 长按：按下→等待→抬起
			return KeyPressHold(key, action.DurationMs)
		}
		return KeyPress(key)
	case "wait":
		d := action.DurationMs
		if d <= 0 {
			d = 100
		}
		time.Sleep(time.Duration(d) * time.Millisecond)
		return nil
	case "screenshot", "":
		// 截图在 Execute 末尾统一处理；单独的 screenshot 动作此处不做事。
		return nil
	case "cursor_position":
		// cursor_position 是查询类，执行端无需移动；结果由 Execute 末尾截图体现。
		return nil
	default:
		return fmt.Errorf("unsupported action type: %s", action.Type)
	}
}

func executeDrag(action Action) error {
	if len(action.Path) < 1 {
		return MouseClick(action.X, action.Y, "left", 1)
	}
	start := action.Path[0]
	if err := MouseMove(start.X, start.Y); err != nil {
		return err
	}
	if err := MouseDown(normalizeButton(action.Button)); err != nil {
		return err
	}
	for _, pt := range action.Path[1:] {
		if err := MouseMove(pt.X, pt.Y); err != nil {
			return err
		}
		sleepBrief()
	}
	return MouseUp(normalizeButton(action.Button))
}

func normalizeButton(button string) string {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "right":
		return "right"
	case "middle":
		return "middle"
	default:
		return "left"
	}
}

func normalizeCount(count int) int {
	if count < 1 {
		return 1
	}
	return count
}

func normalizeDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "up":
		return "up"
	default:
		return "down"
	}
}

func normalizeAmount(amount int) int {
	if amount < 1 {
		return 1
	}
	return amount
}

func describeAction(action Action) string {
	switch action.Type {
	case "click":
		return fmt.Sprintf("click(%d,%d,%s,×%d)", action.X, action.Y, normalizeButton(action.Button), normalizeCount(action.Count))
	case "type":
		text := action.Text
		if len([]rune(text)) > 20 {
			text = string([]rune(text)[:20]) + "..."
		}
		return fmt.Sprintf("type(%q)", text)
	case "key":
		return fmt.Sprintf("key(%s)", action.Key)
	default:
		return action.Type
	}
}
