package app

import "cursor/internal/autostart"

// mainWindowName 是主窗口的固定窗口名，前端据此区分主窗口与独立子窗口。
const mainWindowName = "main"

// hasLaunchFlag 判断启动参数中是否携带自动启动标记。
func hasLaunchFlag(args []string) bool {
	for _, arg := range args {
		if arg == autostart.SilentLaunchFlag {
			return true
		}
	}
	return false
}

// startHidden 决定本次启动是否隐藏主窗口：仅当用户开启「静默启动」
// 且进程由自动启动入口拉起（携带 --silent）时才隐藏；
// 手动双击/命令行启动始终显示主窗口。
func startHidden(silentStart bool, args []string) bool {
	return silentStart && hasLaunchFlag(args)
}
