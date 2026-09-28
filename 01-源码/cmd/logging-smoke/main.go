// Command logging-smoke 是日志系统自检工具：初始化日志、写各级日志与阶段计时、
// 关闭并打印日志文件路径，用于确认「文件/控制台输出、分级、耗时、滚动」实际可用。
//
// 用法（在 pvfine-main 目录下）：
//
//	go run ./cmd/logging-smoke
//	PVFINE_LOG_LEVEL=debug go run ./cmd/logging-smoke     # 看更详细的输出
package main

import (
	"fmt"
	"os"
	"time"

	"pvfine/internal/logging"
)

func main() {
	handle := logging.Init()
	defer func() { _ = handle.Close() }() // 正常退出路径；下面也会显式 Close

	log := logging.For("smoke")
	log.Debug("调试级别日志（默认级别下不输出）", "k", "v")
	log.Info("日志自检开始")
	log.Warn("这是一条警告示例")
	log.Error("这是一条错误示例", "错误", "示例错误对象")

	fast := logging.StartStage("smoke", "快速阶段")
	time.Sleep(20 * time.Millisecond)
	fast.Done("条目", 42)

	slow := logging.StartStage("smoke", "较慢阶段")
	time.Sleep(320 * time.Millisecond)
	slow.Fail(fmt.Errorf("示例失败原因"))

	// 高频写入：验证异步队列不阻塞（10 万条应在毫秒级返回）
	start := time.Now()
	for i := 0; i < 100000; i++ {
		log.Debug("高频调试日志", "i", i)
	}
	elapsed := time.Since(start)

	log.Info("高频写入完成", "条数", 100000, "调用耗时", logging.FormatDuration(elapsed),
		"建议", "DEBUG 被级别过滤时几乎零成本")
	_ = handle.Close()

	fmt.Println("日志目录:", handle.Dir())
	fmt.Println("日志文件:", handle.FilePath())
	fmt.Println("丢弃条数:", handle.Dropped())
	if data, err := os.ReadFile(handle.FilePath()); err == nil {
		fmt.Printf("文件大小: %d 字节\n", len(data))
	}
}
