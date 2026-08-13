# Win32 键鼠注入驱动器，用于驱动隔离抓包实例（cmd/isolated-cursor-e2e）里的 Cursor 窗口。
#
# 危险：本脚本通过 SendKeys / mouse_event 向**前台窗口**注入键鼠事件。抓包时屏幕上通常
# 同时存在你自己的 Cursor 和隔离实例，两者窗口标题相同，肉眼无法区分。唯一的防线是
# TargetPid 校验——除 raw-click 外的每个动作在注入前都会确认目标窗口/前台窗口属于
# TargetPid，不满足就抛 SAFETY_ABORT 而不是继续注入。请始终传隔离实例的 pid
# （isolated-cursor-e2e 启动日志里的 cursor_pid），不要传你自己的 Cursor。
#
# raw-click 是例外：它只校验坐标落在目标窗口矩形内，不校验前台归属。若有其它窗口
# 覆盖在目标窗口之上，点击会落到那个窗口。仅在确认目标窗口未被遮挡时使用。
#
# 坐标约定：click / send-prompt 的 X/Y 是窗口矩形的比例（0..1）；raw-click 是绝对屏幕坐标。
#
# 用法见 scripts/protocol-capture/README.md。

param(
    [Parameter(Mandatory = $true)][int]$TargetPid,
    [Parameter(Mandatory = $true)][string]$Action,
    [double]$X = 0,
    [double]$Y = 0,
    [string]$Text = "",
    [string]$Keys = "",
    [double]$PreX = 0,
    [double]$PreY = 0,
    [int]$Delay = 400
)

$ErrorActionPreference = "Stop"

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class W {
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
    [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
}
"@

function Get-TargetWindow {
    param([int]$ProcessId)
    $p = Get-Process -Id $ProcessId -ErrorAction Stop
    if ($p.MainWindowHandle -eq 0) { throw "target pid $ProcessId has no main window" }
    return $p.MainWindowHandle
}

$hwnd = Get-TargetWindow -ProcessId $TargetPid

# Safety: the handle must belong to the target pid.
$ownerPid = 0
[void][W]::GetWindowThreadProcessId($hwnd, [ref]$ownerPid)
if ($ownerPid -ne $TargetPid) { throw "window owner pid $ownerPid != target $TargetPid" }

$rect = New-Object W+RECT
[void][W]::GetWindowRect($hwnd, [ref]$rect)
$width = $rect.Right - $rect.Left
$height = $rect.Bottom - $rect.Top

function Test-TargetFocused {
    $fg = [W]::GetForegroundWindow()
    $fgPid = 0
    [void][W]::GetWindowThreadProcessId($fg, [ref]$fgPid)
    return ($fgPid -eq $TargetPid)
}

function Focus-Target {
    for ($attempt = 0; $attempt -lt 6; $attempt++) {
        [void][W]::ShowWindow($hwnd, 9)   # SW_RESTORE
        [void][W]::BringWindowToTop($hwnd)
        [void][W]::SetForegroundWindow($hwnd)
        Start-Sleep -Milliseconds 350
        if (Test-TargetFocused) { return }
        Start-Sleep -Milliseconds 500
    }
    throw "SAFETY_ABORT could not focus target $TargetPid"
}

switch ($Action) {
    "rect" {
        "rect left=$($rect.Left) top=$($rect.Top) width=$width height=$height"
    }
    "focus" {
        Focus-Target
        "focused pid=$TargetPid hwnd=$hwnd"
    }
    "click" {
        Focus-Target
        # X/Y are fractions (0..1) of the window rect.
        $sx = [int]($rect.Left + $X * $width)
        $sy = [int]($rect.Top + $Y * $height)
        if ($sx -lt $rect.Left -or $sx -gt $rect.Right -or $sy -lt $rect.Top -or $sy -gt $rect.Bottom) {
            throw "SAFETY_ABORT click point outside target window"
        }
        [void][W]::SetCursorPos($sx, $sy)
        Start-Sleep -Milliseconds 120
        [W]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
        [W]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
        Start-Sleep -Milliseconds $Delay
        "clicked screen=$sx,$sy"
    }
    "keys" {
        Focus-Target
        Add-Type -AssemblyName System.Windows.Forms
        [System.Windows.Forms.SendKeys]::SendWait($Keys)
        Start-Sleep -Milliseconds $Delay
        "keys sent"
    }
    "paste" {
        Focus-Target
        $ok = $false
        for ($i = 0; $i -lt 8; $i++) {
            try { Set-Clipboard -Value $Text; $ok = $true; break } catch { Start-Sleep -Milliseconds 400 }
        }
        if (-not $ok) { throw "clipboard unavailable" }
        Start-Sleep -Milliseconds 250
        Add-Type -AssemblyName System.Windows.Forms
        [System.Windows.Forms.SendKeys]::SendWait("^v")
        Start-Sleep -Milliseconds $Delay
        "pasted chars=$($Text.Length)"
    }
    "type" {
        Focus-Target
        Add-Type -AssemblyName System.Windows.Forms
        $escaped = $Text -replace '([\+\^%~\(\)\{\}\[\]])', '{$1}'
        foreach ($chunk in ($escaped -split '(?<=\G.{40})')) {
            if ($chunk -ne "") {
                [System.Windows.Forms.SendKeys]::SendWait($chunk)
                Start-Sleep -Milliseconds 60
            }
        }
        Start-Sleep -Milliseconds $Delay
        "typed chars=$($Text.Length)"
    }
    "raw-click" {
        # Absolute screen coordinates; no focus stealing. Must land inside the target window rect.
        $sx = [int]$X
        $sy = [int]$Y
        if ($sx -lt $rect.Left -or $sx -gt $rect.Right -or $sy -lt $rect.Top -or $sy -gt $rect.Bottom) {
            throw "SAFETY_ABORT point $sx,$sy outside target rect $($rect.Left),$($rect.Top),$($rect.Right),$($rect.Bottom)"
        }
        [void][W]::SetCursorPos($sx, $sy)
        Start-Sleep -Milliseconds 150
        [W]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
        [W]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
        Start-Sleep -Milliseconds $Delay
        "raw clicked $sx,$sy"
    }
    "send-prompt" {
        # Atomic: focus target -> [optional pre-click] -> click chat input -> [optional keys] -> paste -> Enter.
        $ok = $false
        for ($i = 0; $i -lt 8; $i++) {
            try { Set-Clipboard -Value $Text; $ok = $true; break } catch { Start-Sleep -Milliseconds 400 }
        }
        if (-not $ok) { throw "clipboard unavailable" }
        Focus-Target
        if ($PreX -gt 0) {
            $px = [int]($rect.Left + $PreX * $width)
            $py = [int]($rect.Top + $PreY * $height)
            [void][W]::SetCursorPos($px, $py)
            Start-Sleep -Milliseconds 120
            [W]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
            [W]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
            Start-Sleep -Milliseconds 1200
        }
        $sx = [int]($rect.Left + $X * $width)
        $sy = [int]($rect.Top + $Y * $height)
        [void][W]::SetCursorPos($sx, $sy)
        Start-Sleep -Milliseconds 120
        [W]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
        [W]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
        Start-Sleep -Milliseconds 400
        if (-not (Test-TargetFocused)) { Focus-Target }
        Add-Type -AssemblyName System.Windows.Forms
        if ($Keys -ne "") {
            [System.Windows.Forms.SendKeys]::SendWait($Keys)
            Start-Sleep -Milliseconds 900
        }
        [System.Windows.Forms.SendKeys]::SendWait("^v")
        Start-Sleep -Milliseconds 900
        if (-not (Test-TargetFocused)) { throw "SAFETY_ABORT focus changed before submit" }
        [System.Windows.Forms.SendKeys]::SendWait("{ENTER}")
        Start-Sleep -Milliseconds $Delay
        "prompt submitted chars=$($Text.Length)"
    }
    "fg-info" {
        $fg = [W]::GetForegroundWindow()
        $fgPid = 0
        [void][W]::GetWindowThreadProcessId($fg, [ref]$fgPid)
        $name = (Get-Process -Id $fgPid -ErrorAction SilentlyContinue).ProcessName
        "foreground hwnd=$fg pid=$fgPid name=$name target=$TargetPid"
    }
    "fg-paste" {
        # Sends to whatever currently has focus, but only if it belongs to the target process.
        $fg = [W]::GetForegroundWindow()
        $fgPid = 0
        [void][W]::GetWindowThreadProcessId($fg, [ref]$fgPid)
        if ($fgPid -ne $TargetPid) { throw "SAFETY_ABORT foreground pid $fgPid != target $TargetPid" }
        $ok = $false
        for ($i = 0; $i -lt 8; $i++) {
            try { Set-Clipboard -Value $Text; $ok = $true; break } catch { Start-Sleep -Milliseconds 400 }
        }
        if (-not $ok) { throw "clipboard unavailable" }
        Start-Sleep -Milliseconds 250
        Add-Type -AssemblyName System.Windows.Forms
        [System.Windows.Forms.SendKeys]::SendWait("^v")
        Start-Sleep -Milliseconds $Delay
        "fg pasted chars=$($Text.Length) pid=$fgPid"
    }
    "fg-keys" {
        $fg = [W]::GetForegroundWindow()
        $fgPid = 0
        [void][W]::GetWindowThreadProcessId($fg, [ref]$fgPid)
        if ($fgPid -ne $TargetPid) { throw "SAFETY_ABORT foreground pid $fgPid != target $TargetPid" }
        Add-Type -AssemblyName System.Windows.Forms
        [System.Windows.Forms.SendKeys]::SendWait($Keys)
        Start-Sleep -Milliseconds $Delay
        "fg keys sent pid=$fgPid"
    }
    default { throw "unknown action $Action" }
}
