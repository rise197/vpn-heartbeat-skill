// Package logger — 结构化日志（分级、带时间戳、颜色输出）
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level 日志级别
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

func (l Level) String() string {
	switch l {
	case DEBUG: return "DEBUG"
	case INFO:  return "INFO"
	case WARN:  return "WARN"
	case ERROR: return "ERROR"
	default:   return "????"
	}
}

func (l Level) Color() string {
	switch l {
	case DEBUG: return "\033[36m" // cyan
	case INFO:  return "\033[32m" // green
	case WARN:  return "\033[33m" // yellow
	case ERROR: return "\033[31m" // red
	default:   return "\033[0m"
	}
}

// Logger 分级日志器
type Logger struct {
	mu    sync.Mutex
	name  string
	level Level
	out   io.Writer
}

var defaultLogger = &Logger{name: "vpn-skill", level: INFO, out: os.Stdout}

// New 创建命名日志器
func New(name string) *Logger {
	return &Logger{name: name, level: INFO, out: os.Stdout}
}

// SetLevel 设置最低输出级别
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

func (l *Logger) log(level Level, format string, args ...any) {
	if level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(l.out, "%s[%s] %-5s [%s] %s\033[0m\n",
		level.Color(), ts, level.String(), l.name, msg)
}

func (l *Logger) Debug(f string, a ...any) { l.log(DEBUG, f, a...) }
func (l *Logger) Info(f string, a ...any)  { l.log(INFO, f, a...) }
func (l *Logger) Warn(f string, a ...any)  { l.log(WARN, f, a...) }
func (l *Logger) Error(f string, a ...any) { l.log(ERROR, f, a...) }

// 便捷全局函数
func Debug(f string, a ...any) { defaultLogger.Debug(f, a...) }
func Info(f string, a ...any)  { defaultLogger.Info(f, a...) }
func Warn(f string, a ...any)  { defaultLogger.Warn(f, a...) }
func Error(f string, a ...any) { defaultLogger.Error(f, a...) }
