package bootstrap

import (
	"io"
	"os"
	"strings"
)

func openMainLogWriter(mainLogPath string) (io.Writer, func() error, error) {
	if strings.TrimSpace(mainLogPath) == "" {
		return os.Stdout, func() error { return nil }, nil
	}

	logFile, err := os.OpenFile(mainLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, err
	}

	writer := io.MultiWriter(os.Stdout, logFile)
	cleanup := func() error {
		return logFile.Close()
	}
	return writer, cleanup, nil
}
