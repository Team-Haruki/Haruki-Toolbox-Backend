package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	colorGreen   = "\033[32m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorYellow  = "\033[33m"
	colorRed     = "\033[31m"
	colorReset   = "\033[0m"
)

type logLevel int

const (
	DEBUG logLevel = iota
	INFO
	WARN
	ERROR
	CRITICAL
)

var levelNames = []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}
var levelColors = []string{colorBlue, colorGreen, colorYellow, colorRed, colorMagenta}

var levelMap = map[string]logLevel{
	"DEBUG":    DEBUG,
	"INFO":     INFO,
	"WARNING":  WARN,
	"WARN":     WARN,
	"ERROR":    ERROR,
	"CRITICAL": CRITICAL,
}

// globalLogLevel is used by NewLoggerFromGlobal to inherit log level
var globalLogLevel = INFO
var globalLogLevelMu sync.RWMutex

// globalFileWriter is the shared file writer for all loggers
var globalFileWriter io.Writer
var globalFileWriterMu sync.RWMutex

type Logger struct {
	name            string
	level           logLevel
	mu              sync.Mutex
	writer          io.Writer
	colored         bool
	useGlobalConfig bool
}

var DefaultLogger *Logger

func init() {
	DefaultLogger = NewLogger("HarukiDefaultLogger", "INFO", os.Stdout)
}

func SetDefaultLogger(l *Logger) {
	DefaultLogger = l
}

// SetGlobalLogLevel sets the global log level for NewLoggerFromGlobal
func SetGlobalLogLevel(level string) {
	globalLogLevelMu.Lock()
	defer globalLogLevelMu.Unlock()
	if lvl, ok := levelMap[strings.ToUpper(level)]; ok {
		globalLogLevel = lvl
	}
}

// GetGlobalLogLevel returns the current global log level as string
func GetGlobalLogLevel() string {
	globalLogLevelMu.RLock()
	defer globalLogLevelMu.RUnlock()
	return levelNames[globalLogLevel]
}

func getGlobalLogLevelValue() logLevel {
	globalLogLevelMu.RLock()
	defer globalLogLevelMu.RUnlock()
	return globalLogLevel
}

// SetGlobalFileWriter sets the global file writer for all loggers
func SetGlobalFileWriter(w io.Writer) {
	globalFileWriterMu.Lock()
	defer globalFileWriterMu.Unlock()
	globalFileWriter = w
}

func getGlobalFileWriter() io.Writer {
	globalFileWriterMu.RLock()
	defer globalFileWriterMu.RUnlock()
	if globalFileWriter != nil {
		return globalFileWriter
	}
	return os.Stdout
}

// OpenLogFile opens a log file for writing, creating directories if needed
func OpenLogFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

// multiWriter writes to multiple writers
type multiWriter struct {
	writers []io.Writer
}

func (mw *multiWriter) Write(p []byte) (n int, err error) {
	for _, w := range mw.writers {
		if w != nil {
			_, _ = w.Write(p)
		}
	}
	return len(p), nil
}

// NewMultiWriter creates a writer that writes to multiple destinations
func NewMultiWriter(writers ...io.Writer) io.Writer {
	filtered := make([]io.Writer, 0, len(writers))
	for _, w := range writers {
		if w != nil {
			filtered = append(filtered, w)
		}
	}
	if len(filtered) == 0 {
		return os.Stdout
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &multiWriter{writers: filtered}
}

func NewLogger(name string, level string, writer io.Writer) *Logger {
	if writer == nil {
		writer = os.Stdout
	}
	lvl, ok := levelMap[strings.ToUpper(level)]
	if !ok {
		lvl = INFO
	}
	return &Logger{
		name:    name,
		level:   lvl,
		writer:  writer,
		colored: writerSupportsColor(writer),
	}
}

// NewLoggerFromGlobal creates a logger using the global log level and file writer
func NewLoggerFromGlobal(name string) *Logger {
	return &Logger{
		name:            name,
		level:           INFO,
		writer:          nil,
		colored:         false,
		useGlobalConfig: true,
	}
}

func writerSupportsColor(writer io.Writer) bool {
	if writer == nil {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (l *Logger) logf(level logLevel, format string, args ...any) {
	writer := l.writer
	effectiveLevel := l.level
	colored := l.colored
	if l.useGlobalConfig {
		writer = getGlobalFileWriter()
		effectiveLevel = getGlobalLogLevelValue()
		colored = writerSupportsColor(writer)
	}
	if writer == nil {
		writer = os.Stdout
	}

	if level < effectiveLevel {
		return
	}
	msg := fmt.Sprintf(format, args...)
	now := time.Now().Format("2006-01-02 15:04:05.000")

	var ts string
	var lvlStr string
	var name string
	if colored {
		ts = fmt.Sprintf("%s[%s]%s", colorGreen, now, colorReset)
		lvlColor := levelColors[level]
		lvlStr = fmt.Sprintf("%s%s%s", lvlColor, levelNames[level], colorReset)
		name = fmt.Sprintf("[%s%s%s]", colorMagenta, l.name, colorReset)
	} else {
		ts = fmt.Sprintf("[%s]", now)
		lvlStr = levelNames[level]
		name = fmt.Sprintf("[%s]", l.name)
	}

	line := fmt.Sprintf("%s[%s]%s %s\n", ts, lvlStr, name, msg)

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprint(writer, line)
}

func (l *Logger) Debugf(format string, args ...any)    { l.logf(DEBUG, format, args...) }
func (l *Logger) Infof(format string, args ...any)     { l.logf(INFO, format, args...) }
func (l *Logger) Warnf(format string, args ...any)     { l.logf(WARN, format, args...) }
func (l *Logger) Errorf(format string, args ...any)    { l.logf(ERROR, format, args...) }
func (l *Logger) Criticalf(format string, args ...any) { l.logf(CRITICAL, format, args...) }
func (l *Logger) Exceptionf(format string, args ...any) {
	l.logf(ERROR, "[EXCEPTION] "+format, args...)
}

func Debugf(format string, args ...any)    { DefaultLogger.Debugf(format, args...) }
func Infof(format string, args ...any)     { DefaultLogger.Infof(format, args...) }
func Warnf(format string, args ...any)     { DefaultLogger.Warnf(format, args...) }
func Errorf(format string, args ...any)    { DefaultLogger.Errorf(format, args...) }
func Criticalf(format string, args ...any) { DefaultLogger.Criticalf(format, args...) }
