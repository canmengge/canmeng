package services

import "pvfine/internal/logging"

// LogService 为前端「输出日志」面板服务。
//
// 日志的**实时**推送走事件通道（`app:log`，由 main 在创建应用后注册），
// 这里只负责两件事：
//   - History：面板打开/刷新时补齐"启动以来"的那段日志（实时事件早于订阅就丢了）；
//   - Clear：清空服务端历史缓冲（只影响面板能补到的历史，不动日志文件）。
type LogService struct{}

func NewLogService() *LogService { return &LogService{} }

// History 返回最近的结构化日志（最多 512 条，旧的在前）。
func (s *LogService) History() []logging.Entry { return logging.History() }

// Clear 清空服务端历史缓冲；后续日志照常推送。
func (s *LogService) Clear() { logging.ClearHistory() }
