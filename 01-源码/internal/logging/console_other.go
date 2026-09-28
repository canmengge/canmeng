//go:build !windows

package logging

// consoleAttached 在非 Windows 平台恒为 true：进程总是从终端启动。
func consoleAttached() bool { return true }
