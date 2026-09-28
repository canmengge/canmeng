//go:build windows

package logging

import "golang.org/x/sys/windows"

// consoleAttached 报告当前进程是否连着控制台。
//
// GUI 子系统（构建时 `-H windowsgui`，与项目自带 Taskfile 的正式构建一致）
// 双击启动时没有控制台，此时默认不再往 stderr 写日志：既没有输出对象，
// 也能让"控制台=true"这类字段如实反映实际行为。
// 从终端启动（有控制台）时照旧输出，方便排查。
func consoleAttached() bool {
	handle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return false
	}
	return handle != 0 && handle != windows.InvalidHandle
}
