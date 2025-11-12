package coordinator

import (
	"log"
)

// stdLogger 使用标准库日志器实现 Logger 接口。
type stdLogger struct{}

// Infof 通过标准日志器输出普通信息。
func (stdLogger) Infof(format string, args ...any) {
	log.Printf("[INFO] "+format, args...)
}

// Warnf 输出警告信息。
func (stdLogger) Warnf(format string, args ...any) {
	log.Printf("[WARN] "+format, args...)
}

// Errorf 输出错误信息。
func (stdLogger) Errorf(format string, args ...any) {
	log.Printf("[ERROR] "+format, args...)
}

// defaultLogger 在未传入 Logger 时返回默认实现。
func defaultLogger(l Logger) Logger {
	if l != nil {
		return l
	}
	return stdLogger{}
}
